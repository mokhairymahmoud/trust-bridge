"""
EDC/Control Plane registration command.

This command registers encrypted assets with the external Control Plane,
storing metadata and decryption keys for authorized consumer access.
"""

import json
from pathlib import Path
from typing import Optional

import typer
from typing_extensions import Annotated

from ..common import (
    console,
    success,
    error,
    info,
    warning,
    display_table,
    redact_key,
    validate_file_exists,
    validate_hex_string,
    validate_url,
    retry_with_backoff,
    TrustBridgeError,
    ControlPlaneError,
    ValidationError,
    NetworkError,
)


def _load_manifest(manifest_path: Path) -> dict:
    """
    Load and validate manifest file.

    Args:
        manifest_path: Path to manifest JSON file

    Returns:
        Manifest data dictionary

    Raises:
        ValidationError: If manifest is invalid
    """
    try:
        with open(manifest_path, "r") as f:
            manifest = json.load(f)

        # Validate required fields
        required_fields = [
            "format",
            "algo",
            "chunk_bytes",
            "plaintext_bytes",
            "sha256_ciphertext",
            "asset_id",
        ]

        missing_fields = [f for f in required_fields if f not in manifest]
        if missing_fields:
            raise ValidationError(
                "Manifest is missing required fields",
                details=f"Missing: {', '.join(missing_fields)}",
            )

        return manifest

    except json.JSONDecodeError as e:
        raise ValidationError(
            f"Invalid JSON in manifest file",
            details=str(e),
        )
    except ValidationError:
        raise
    except Exception as e:
        raise ValidationError(
            f"Failed to load manifest",
            details=str(e),
        )


@retry_with_backoff(
    max_attempts=3,
    retryable_exceptions=(NetworkError,),
    initial_delay=2.0,
)
def _register_with_controlplane(
    endpoint: str,
    asset_id: str,
    manifest: dict,
    key_hex: str,
    blob_url_base: str,
) -> dict:
    """
    Register asset with Control Plane API.

    Args:
        endpoint: Control Plane API endpoint
        asset_id: Asset identifier
        manifest: Manifest data
        key_hex: Decryption key (hex)
        blob_url_base: Base URL for blobs

    Returns:
        API response dictionary

    Raises:
        ControlPlaneError: If registration fails
        NetworkError: If network error occurs (will be retried)
    """
    try:
        import requests

        # Construct blob URLs
        encrypted_url = f"{blob_url_base}/models/{asset_id}/model.tbenc"
        manifest_url = f"{blob_url_base}/models/{asset_id}/model.manifest.json"

        # Prepare request payload
        payload = {
            "asset_id": asset_id,
            "blob_urls": {
                "ciphertext": encrypted_url,
                "manifest": manifest_url,
            },
            "metadata": {
                "format": manifest["format"],
                "algo": manifest["algo"],
                "chunk_bytes": manifest["chunk_bytes"],
                "plaintext_bytes": manifest["plaintext_bytes"],
                "sha256_ciphertext": manifest["sha256_ciphertext"],
            },
            "decryption_key_hex": key_hex,
        }

        # Make API request
        register_url = f"{endpoint}/api/v1/assets/register"

        info(f"Registering asset with Control Plane at {register_url}...")

        response = requests.post(
            register_url,
            json=payload,
            headers={
                "Content-Type": "application/json",
                "User-Agent": "trustbridge-cli/0.1.0",
            },
            timeout=30,
        )

        # Handle response
        if response.status_code == 200 or response.status_code == 201:
            return response.json()

        elif response.status_code == 400:
            # Validation error
            try:
                error_data = response.json()
                error_message = error_data.get("message", "Validation failed")
                error_details = error_data.get("details", response.text)
            except:
                error_message = "Validation failed"
                error_details = response.text

            raise ControlPlaneError(
                error_message,
                details=error_details,
            )

        elif response.status_code in (401, 403):
            # Authentication/authorization error
            raise ControlPlaneError(
                "Authentication or authorization failed",
                details="Ensure you have valid credentials and permissions for the Control Plane API",
            )

        elif response.status_code >= 500:
            # Server error - retryable
            raise NetworkError(
                f"Control Plane server error (HTTP {response.status_code})"
            )

        else:
            # Other error
            raise ControlPlaneError(
                f"Registration failed with HTTP {response.status_code}",
                details=response.text,
            )

    except requests.exceptions.Timeout:
        raise NetworkError("Request timed out after 30 seconds")

    except requests.exceptions.ConnectionError as e:
        raise NetworkError(f"Connection error: {e}")

    except (ControlPlaneError, NetworkError):
        raise

    except Exception as e:
        raise ControlPlaneError(
            "Failed to register asset",
            details=str(e),
        )


def command(
    edc_endpoint: Annotated[
        str,
        typer.Option(
            "--edc-endpoint",
            help="Control Plane / EDC API endpoint URL",
            envvar="TB_EDC_ENDPOINT",
        ),
    ] = "https://controlplane.trustbridge.io",
    asset_id: Annotated[
        str,
        typer.Option(
            "--asset-id",
            help="Asset identifier (must match encryption and upload)",
        ),
    ] = ...,
    manifest_file: Annotated[
        Path,
        typer.Option(
            "--manifest",
            help="Path to manifest.json file",
            exists=True,
            file_okay=True,
            dir_okay=False,
        ),
    ] = Path("./encrypted/model.manifest.json"),
    key_hex: Annotated[
        str,
        typer.Option(
            "--key-hex",
            help="Decryption key in hexadecimal (64 characters)",
        ),
    ] = ...,
    blob_url_base: Annotated[
        Optional[str],
        typer.Option(
            "--blob-url",
            help="Base blob storage URL (e.g., https://myaccount.blob.core.windows.net/models)",
        ),
    ] = None,
    storage_account: Annotated[
        Optional[str],
        typer.Option(
            "--storage-account",
            help="Azure storage account name (alternative to --blob-url)",
        ),
    ] = None,
    container: Annotated[
        str,
        typer.Option(
            "--container",
            help="Storage container name",
        ),
    ] = "models",
) -> None:
    """
    Register asset with Control Plane / EDC.

    This command registers your encrypted asset with the TrustBridge Control Plane,
    making it available for authorized consumers to access. The Control Plane stores:
      - Asset metadata (format, size, hash)
      - Blob URLs for encrypted data
      - Decryption key (securely stored in vault)

    The Control Plane is an EXTERNAL service (not part of this project).
    It handles authorization and issues SAS URLs to authorized consumers.

    Prerequisites:
        - Asset must be encrypted (trustbridge encrypt)
        - Assets must be uploaded to Azure Blob (trustbridge upload)
        - You must have the decryption key from encryption step

    Examples:

        # Register with blob URL
        trustbridge register --asset-id llama-3-70b-v1 \\
            --key-hex a1b2c3d4... \\
            --blob-url https://myaccount.blob.core.windows.net/models

        # Register with storage account (auto-constructs blob URL)
        trustbridge register --asset-id llama-3-70b-v1 \\
            --key-hex a1b2c3d4... \\
            --storage-account myaccount \\
            --container models

        # Register with custom Control Plane endpoint
        trustbridge register --asset-id my-model \\
            --edc-endpoint https://my-controlplane.example.com \\
            --key-hex a1b2c3d4... \\
            --storage-account myaccount

    Security:
        The decryption key is transmitted securely to the Control Plane
        and stored in a vault. It will only be released to authorized consumers
        based on contract validation.
    """
    try:
        # Validate inputs
        info("Validating inputs...")
        validate_file_exists(manifest_file)
        validate_hex_string(key_hex, expected_bytes=32)  # 32 bytes = 64 hex chars
        validate_url(edc_endpoint, require_https=True)

        # Construct blob URL base if needed
        if not blob_url_base:
            if not storage_account:
                raise ValidationError(
                    "Either --blob-url or --storage-account is required",
                    details="Provide the base URL for your blob storage",
                )
            blob_url_base = (
                f"https://{storage_account}.blob.core.windows.net/{container}"
            )

        # Load manifest
        manifest = _load_manifest(manifest_file)

        # Verify asset ID matches manifest
        if manifest.get("asset_id") != asset_id:
            warning(
                f"Asset ID mismatch: CLI argument '{asset_id}' != "
                f"manifest '{manifest.get('asset_id')}'. Using CLI argument."
            )

        # Display registration parameters (with redacted key)
        console.console.print()
        display_table(
            "Registration Parameters",
            [
                ("Control Plane Endpoint", edc_endpoint),
                ("Asset ID", asset_id),
                ("Manifest File", str(manifest_file)),
                ("Decryption Key", redact_key(key_hex)),
                ("Blob URL Base", blob_url_base),
                ("Plaintext Size", f"{manifest['plaintext_bytes'] / (1024**3):.2f} GB"),
                ("Ciphertext Hash", manifest["sha256_ciphertext"][:16] + "..."),
            ],
        )
        console.console.print()

        # Register with Control Plane
        response = _register_with_controlplane(
            endpoint=edc_endpoint,
            asset_id=asset_id,
            manifest=manifest,
            key_hex=key_hex,
            blob_url_base=blob_url_base,
        )

        # Display results
        console.console.print()
        success("Asset registered successfully with Control Plane!")

        if response:
            info("Response from Control Plane:")
            console.console.print_json(json.dumps(response, indent=2))

        console.console.print()
        info("Next steps:")
        console.console.print(
            "  1. Build Docker image: trustbridge build ..."
        )
        console.console.print(
            "  2. Package for Azure Marketplace: trustbridge package ..."
        )

        warning(
            "The decryption key has been sent to the Control Plane. "
            "Ensure you trust this endpoint!"
        )

    except TrustBridgeError as e:
        error(e.message, details=e.details)
        raise typer.Exit(1)
    except Exception as e:
        error(f"Unexpected error during registration", details=str(e))
        raise typer.Exit(1)
