"""
Encrypt command - Encrypt model weights using tbenc/v1 format.

This command wraps the crypto_tbenc module to provide a user-friendly
CLI interface for encrypting model weights with chunked AES-256-GCM.
"""

from pathlib import Path
from typing import Optional

import typer
from typing_extensions import Annotated

from ..common import (
    console,
    success,
    error,
    info,
    progress,
    display_key_warning,
    display_table,
    validate_asset_id,
    validate_chunk_size,
    validate_file_exists,
    validate_disk_space,
    TrustBridgeError,
    CryptoError,
)
from ..crypto_tbenc import (
    encrypt_and_generate_manifest,
    DEFAULT_CHUNK_SIZE,
)


def _do_encryption(
    input_path: Path,
    output_dir: Path,
    asset_id: str,
    chunk_bytes: int,
) -> tuple[str, Path, Path]:
    """
    Internal function to perform encryption.

    Args:
        input_path: Path to plaintext file
        output_dir: Output directory for encrypted files
        asset_id: Asset identifier
        chunk_bytes: Chunk size for encryption

    Returns:
        Tuple of (key_hex, encrypted_path, manifest_path)

    Raises:
        CryptoError: If encryption fails
    """
    try:
        # Perform encryption - the function handles directory creation
        key_bytes, encrypted_path, manifest_path = encrypt_and_generate_manifest(
            input_path=input_path,
            output_dir=output_dir,
            asset_id=asset_id,
            chunk_bytes=chunk_bytes,
        )

        # Convert key bytes to hex string
        key_hex = key_bytes.hex()

        return key_hex, encrypted_path, manifest_path

    except Exception as e:
        raise CryptoError(
            "Encryption failed",
            details=str(e),
        )


def command(
    input_file: Annotated[
        Path,
        typer.Argument(
            help="Path to plaintext model weights file",
            exists=True,
            file_okay=True,
            dir_okay=False,
            readable=True,
        ),
    ],
    asset_id: Annotated[
        str,
        typer.Option(
            "--asset-id",
            help="Unique asset identifier (alphanumeric, hyphens, underscores)",
        ),
    ],
    output_dir: Annotated[
        Optional[Path],
        typer.Option(
            "--out",
            "-o",
            help="Output directory for encrypted files (default: ./encrypted)",
        ),
    ] = None,
    chunk_bytes: Annotated[
        int,
        typer.Option(
            "--chunk-bytes",
            "-c",
            help="Chunk size for encryption in bytes (recommended: 4MB-16MB)",
        ),
    ] = DEFAULT_CHUNK_SIZE,
) -> None:
    """
    Encrypt model weights using tbenc/v1 format.

    This command encrypts your model weights file using chunked AES-256-GCM
    encryption, generating an encrypted file, manifest, and decryption key.

    The encryption process:
    1. Validates inputs (file exists, asset ID format, chunk size)
    2. Checks available disk space
    3. Encrypts file chunk-by-chunk with AES-256-GCM
    4. Generates manifest with ciphertext hash
    5. Outputs decryption key (MUST BE SAVED!)

    Examples:

        # Basic encryption
        trustbridge encrypt model.safetensors --asset-id llama-3-70b-v1

        # Custom output directory
        trustbridge encrypt model.safetensors --asset-id my-model \\
            --out ./encrypted

        # Custom chunk size (8MB)
        trustbridge encrypt model.safetensors --asset-id my-model \\
            --chunk-bytes 8388608

    Output Files:
        - model.tbenc: Encrypted ciphertext
        - model.manifest.json: Manifest with metadata and hash
        - Decryption key: Printed to console (64 hex characters)

    Security:
        The decryption key is displayed ONCE and must be saved securely.
        Without this key, the encrypted data cannot be decrypted.
    """
    try:
        # Set default output directory
        if output_dir is None:
            output_dir = Path("./encrypted")

        # Validate inputs
        info(f"Validating inputs...")
        validate_file_exists(input_file, allow_empty=False)
        validate_asset_id(asset_id)
        validate_chunk_size(chunk_bytes)

        # Check disk space (estimate: encrypted file will be ~same size + overhead)
        input_size = input_file.stat().st_size
        validate_disk_space(output_dir, input_size, safety_margin=1.3)

        # Display encryption parameters
        console.print()
        display_table(
            "Encryption Parameters",
            [
                ("Input File", str(input_file)),
                ("File Size", f"{input_size / (1024**3):.2f} GB" if input_size > 1024**3 else f"{input_size / (1024**2):.2f} MB"),
                ("Asset ID", asset_id),
                ("Chunk Size", f"{chunk_bytes / (1024**2):.0f} MB"),
                ("Output Directory", str(output_dir)),
            ],
        )
        console.print()

        # Perform encryption
        with progress(f"Encrypting {input_file.name}..."):
            key_hex, encrypted_path, manifest_path = _do_encryption(
                input_path=input_file,
                output_dir=output_dir,
                asset_id=asset_id,
                chunk_bytes=chunk_bytes,
            )

        # Display results
        encrypted_size = encrypted_path.stat().st_size
        console.print()
        display_table(
            "Encryption Results",
            [
                ("Encrypted File", str(encrypted_path)),
                ("Encrypted Size", f"{encrypted_size / (1024**3):.2f} GB" if encrypted_size > 1024**3 else f"{encrypted_size / (1024**2):.2f} MB"),
                ("Manifest", str(manifest_path)),
            ],
        )

        # Display decryption key with warning
        display_key_warning(key_hex)

        success(f"Encryption completed successfully!")
        console.print()
        info("Next steps:")
        console.print("  1. Save the decryption key in a secure location")
        console.print("  2. Upload encrypted assets: trustbridge upload ...")
        console.print("  3. Register with Control Plane: trustbridge register ...")

    except TrustBridgeError as e:
        error(e.message, details=e.details)
        raise typer.Exit(1)
    except Exception as e:
        error(f"Unexpected error during encryption", details=str(e))
        raise typer.Exit(1)
