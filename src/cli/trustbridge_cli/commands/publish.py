"""
Publish command - Orchestrate the full TrustBridge publish workflow.

This command chains together all the individual steps to publish
a model to the TrustBridge platform:
1. Encrypt weights
2. Upload to Azure Blob
3. Register with Control Plane
4. Build Docker image
5. Package Managed App

It provides a streamlined, all-in-one workflow for providers.
"""

from pathlib import Path
from typing import Optional

import typer
from rich.prompt import Confirm
from typing_extensions import Annotated

from ..common import (
    console,
    success,
    error,
    info,
    warning,
    display_table,
    display_key_warning,
    validate_file_exists,
    TrustBridgeError,
)
from ..crypto_tbenc import DEFAULT_CHUNK_SIZE

# Import command logic from other modules
from . import encrypt, blob, edc, build, package


def command(
    input_file: Annotated[
        Path,
        typer.Argument(
            help="Path to plaintext model weights file",
            exists=True,
            file_okay=True,
            dir_okay=False,
        ),
    ],
    asset_id: Annotated[
        str,
        typer.Option(
            "--asset-id",
            help="Unique asset identifier",
        ),
    ],
    storage_account: Annotated[
        str,
        typer.Option(
            "--storage-account",
            help="Azure storage account name",
            envvar="AZURE_STORAGE_ACCOUNT",
        ),
    ],
    registry: Annotated[
        str,
        typer.Option(
            "--registry",
            help="Container registry (e.g., myregistry.azurecr.io)",
            envvar="DOCKER_REGISTRY",
        ),
    ],
    sentinel_binary: Annotated[
        Path,
        typer.Option(
            "--sentinel-binary",
            help="Path to compiled sentinel binary",
            exists=True,
        ),
    ],
    output_dir: Annotated[
        Path,
        typer.Option(
            "--output",
            "-o",
            help="Output directory for all artifacts",
        ),
    ] = Path("./publish-output"),
    container: Annotated[
        str,
        typer.Option(
            "--container",
            help="Storage container name",
        ),
    ] = "models",
    chunk_bytes: Annotated[
        int,
        typer.Option(
            "--chunk-bytes",
            help="Encryption chunk size in bytes",
        ),
    ] = DEFAULT_CHUNK_SIZE,
    edc_endpoint: Annotated[
        str,
        typer.Option(
            "--edc-endpoint",
            help="Control Plane endpoint URL",
        ),
    ] = "https://controlplane.trustbridge.io",
    image_name: Annotated[
        str,
        typer.Option(
            "--image-name",
            help="Docker image name",
        ),
    ] = "trustbridge-runtime",
    image_tag: Annotated[
        str,
        typer.Option(
            "--image-tag",
            help="Docker image tag",
        ),
    ] = "latest",
    base_image: Annotated[
        str,
        typer.Option(
            "--base-image",
            help="Base Docker image",
        ),
    ] = "nvcr.io/nvidia/vllm:latest",
    skip_encrypt: Annotated[
        bool,
        typer.Option(
            "--skip-encrypt",
            help="Skip encryption (encrypted files already exist)",
        ),
    ] = False,
    skip_upload: Annotated[
        bool,
        typer.Option(
            "--skip-upload",
            help="Skip blob upload (already uploaded)",
        ),
    ] = False,
    skip_register: Annotated[
        bool,
        typer.Option(
            "--skip-register",
            help="Skip Control Plane registration (already registered)",
        ),
    ] = False,
    skip_build: Annotated[
        bool,
        typer.Option(
            "--skip-build",
            help="Skip Docker build (image already built)",
        ),
    ] = False,
    skip_package: Annotated[
        bool,
        typer.Option(
            "--skip-package",
            help="Skip package generation (package already created)",
        ),
    ] = False,
    yes: Annotated[
        bool,
        typer.Option(
            "--yes",
            "-y",
            help="Skip confirmation prompt",
        ),
    ] = False,
) -> None:
    """
    Orchestrate the full TrustBridge publish workflow.

    This command automates the entire process of publishing a model
    to the TrustBridge platform, from encryption to packaging:

    Workflow Steps:
        1. Encrypt: Convert plaintext weights to tbenc/v1 format
        2. Upload: Push encrypted assets to Azure Blob Storage
        3. Register: Register asset with Control Plane / EDC
        4. Build: Build and push Docker image
        5. Package: Generate Azure Managed App package

    Each step can be skipped using --skip-* flags if already completed
    (useful for resuming after failures or testing individual steps).

    Prerequisites:
        - Azure CLI installed and logged in (az login)
        - Docker installed and running
        - Container registry authentication (docker login)
        - Sentinel binary compiled
        - Sufficient disk space for encrypted files

    Examples:

        # Full workflow (interactive confirmation)
        trustbridge publish model.safetensors \\
            --asset-id llama-3-70b-v1 \\
            --storage-account myaccount \\
            --registry myregistry.azurecr.io \\
            --sentinel-binary ./bin/sentinel

        # Full workflow (skip confirmation)
        trustbridge publish model.safetensors \\
            --asset-id my-model \\
            --storage-account myaccount \\
            --registry myregistry.azurecr.io \\
            --sentinel-binary ./bin/sentinel \\
            --yes

        # Resume after failure (skip completed steps)
        trustbridge publish model.safetensors \\
            --asset-id my-model \\
            --storage-account myaccount \\
            --registry myregistry.azurecr.io \\
            --sentinel-binary ./bin/sentinel \\
            --skip-encrypt --skip-upload

        # Custom configuration
        trustbridge publish model.safetensors \\
            --asset-id my-model \\
            --storage-account myaccount \\
            --registry myregistry.azurecr.io \\
            --sentinel-binary ./bin/sentinel \\
            --output ./my-publish \\
            --chunk-bytes 8388608 \\
            --base-image nvcr.io/nvidia/triton:latest

    Output:
        All artifacts are saved to the output directory:
        - encrypted/: Encrypted files and manifest
        - trustbridge-app.zip: Managed App package
        - publish-summary.txt: Summary of all steps

    Environment Variables:
        AZURE_STORAGE_ACCOUNT: Default storage account
        DOCKER_REGISTRY: Default container registry
        TB_EDC_ENDPOINT: Default Control Plane endpoint
    """
    try:
        # Validate prerequisites
        info("Validating inputs...")
        validate_file_exists(input_file)
        validate_file_exists(sentinel_binary)

        if not storage_account:
            raise TrustBridgeError(
                "Storage account is required",
                details="Provide via --storage-account or AZURE_STORAGE_ACCOUNT",
            )

        if not registry:
            raise TrustBridgeError(
                "Container registry is required",
                details="Provide via --registry or DOCKER_REGISTRY",
            )

        # Ensure output directory exists
        output_dir.mkdir(parents=True, exist_ok=True)
        encrypted_dir = output_dir / "encrypted"

        # Construct full image tag
        full_image_tag = f"{registry}/{image_name}:{image_tag}"

        # Display publish plan
        console.console.print()
        console.console.print("[bold]TrustBridge Publish Workflow[/bold]")
        console.console.print()

        display_table(
            "Configuration",
            [
                ("Input File", str(input_file)),
                ("Asset ID", asset_id),
                ("Storage Account", storage_account),
                ("Container", container),
                ("Registry", registry),
                ("Image", full_image_tag),
                ("Sentinel Binary", str(sentinel_binary)),
                ("Output Directory", str(output_dir)),
                ("EDC Endpoint", edc_endpoint),
            ],
        )

        console.console.print()
        info("Workflow Steps:")
        console.console.print(f"  1. {'[dim](SKIP)[/dim]' if skip_encrypt else '✓'} Encrypt weights")
        console.console.print(f"  2. {'[dim](SKIP)[/dim]' if skip_upload else '✓'} Upload to Azure Blob")
        console.console.print(f"  3. {'[dim](SKIP)[/dim]' if skip_register else '✓'} Register with Control Plane")
        console.console.print(f"  4. {'[dim](SKIP)[/dim]' if skip_build else '✓'} Build Docker image")
        console.console.print(f"  5. {'[dim](SKIP)[/dim]' if skip_package else '✓'} Package Managed App")

        # Confirmation
        console.console.print()
        if not yes and not Confirm.ask(
            "Proceed with publish workflow?", default=True
        ):
            console.console.print("Cancelled by user")
            raise typer.Exit(0)

        # Track state
        decryption_key = None
        encrypted_file = encrypted_dir / "model.tbenc"
        manifest_file = encrypted_dir / "model.manifest.json"
        package_file = output_dir / "trustbridge-app.zip"

        # Step 1: Encrypt
        console.console.print()
        console.console.print("[bold cyan]Step 1/5: Encrypting weights[/bold cyan]")

        if skip_encrypt:
            info("Skipping encryption (--skip-encrypt)")
            # Try to find existing files
            if not encrypted_file.exists() or not manifest_file.exists():
                raise TrustBridgeError(
                    "Encrypted files not found",
                    details=f"Expected files not found in {encrypted_dir}. Remove --skip-encrypt to encrypt now.",
                )
            warning("Using existing encrypted files. Ensure decryption key is saved!")
        else:
            # Call encrypt logic
            from .encrypt import _do_encryption

            decryption_key, encrypted_file, manifest_file = _do_encryption(
                input_path=input_file,
                output_dir=encrypted_dir,
                asset_id=asset_id,
                chunk_bytes=chunk_bytes,
            )
            success(f"Encryption completed")
            display_key_warning(decryption_key)

        # Step 2: Upload
        console.console.print()
        console.console.print("[bold cyan]Step 2/5: Uploading to Azure Blob[/bold cyan]")

        if skip_upload:
            info("Skipping upload (--skip-upload)")
        else:
            from .blob import _do_upload

            encrypted_url, manifest_url = _do_upload(
                storage_account=storage_account,
                container=container,
                asset_id=asset_id,
                encrypted_file=encrypted_file,
                manifest_file=manifest_file,
            )
            success(f"Upload completed")

        # Step 3: Register
        console.console.print()
        console.console.print("[bold cyan]Step 3/5: Registering with Control Plane[/bold cyan]")

        if skip_register:
            info("Skipping registration (--skip-register)")
        else:
            if not decryption_key:
                # If encryption was skipped, we need the key
                warning(
                    "Decryption key required for registration but encryption was skipped."
                )
                decryption_key = typer.prompt(
                    "Enter decryption key (64 hex characters)",
                    hide_input=True,
                )

            from .edc import _load_manifest, _register_with_controlplane

            manifest = _load_manifest(manifest_file)
            blob_url_base = f"https://{storage_account}.blob.core.windows.net/{container}"

            response = _register_with_controlplane(
                endpoint=edc_endpoint,
                asset_id=asset_id,
                manifest=manifest,
                key_hex=decryption_key,
                blob_url_base=blob_url_base,
            )
            success(f"Registration completed")

        # Step 4: Build
        console.console.print()
        console.console.print("[bold cyan]Step 4/5: Building Docker image[/bold cyan]")

        if skip_build:
            info("Skipping Docker build (--skip-build)")
        else:
            from .build import (
                _check_docker_available,
                _create_dockerfile,
                _create_default_entrypoint,
                _build_image,
                _push_image,
            )
            import tempfile
            import shutil

            _check_docker_available()

            with tempfile.TemporaryDirectory() as temp_dir:
                build_dir = Path(temp_dir)

                # Prepare build context
                shutil.copy(sentinel_binary, build_dir / "sentinel")
                (build_dir / "entrypoint.sh").write_text(_create_default_entrypoint())
                dockerfile_content = _create_dockerfile(base_image, include_entrypoint=True)
                (build_dir / "Dockerfile").write_text(dockerfile_content)

                # Build and push
                _build_image(build_dir, full_image_tag)
                _push_image(full_image_tag)

            success(f"Docker build and push completed")

        # Step 5: Package
        console.console.print()
        console.console.print("[bold cyan]Step 5/5: Generating Managed App package[/bold cyan]")

        if skip_package:
            info("Skipping package generation (--skip-package)")
        else:
            from .package import _generate_arm_template, _generate_ui_definition
            import json
            import zipfile

            arm_template = _generate_arm_template(asset_id, full_image_tag, edc_endpoint)
            ui_definition = _generate_ui_definition()

            with zipfile.ZipFile(package_file, "w", zipfile.ZIP_DEFLATED) as zipf:
                zipf.writestr("mainTemplate.json", json.dumps(arm_template, indent=2))
                zipf.writestr("createUiDefinition.json", json.dumps(ui_definition, indent=2))

            success(f"Package generated: {package_file}")

        # Final summary
        console.console.print()
        console.console.print("[bold green]✓ Publish workflow completed successfully![/bold green]")
        console.console.print()

        display_table(
            "Published Artifacts",
            [
                ("Encrypted File", str(encrypted_file)),
                ("Manifest", str(manifest_file)),
                ("Docker Image", full_image_tag),
                ("Managed App Package", str(package_file)),
            ],
        )

        if decryption_key:
            console.console.print()
            warning(
                f"Decryption key: {decryption_key}\n"
                "SAVE THIS KEY! It has been registered with the Control Plane."
            )

        console.console.print()
        info("Next steps:")
        console.console.print("  1. Test the Managed App package in a dev subscription")
        console.console.print("  2. Publish to Azure Marketplace Partner Center")
        console.console.print("  3. Consumers can deploy with their contract ID")

    except TrustBridgeError as e:
        error(e.message, details=e.details)
        raise typer.Exit(1)
    except Exception as e:
        error(f"Unexpected error during publish", details=str(e))
        raise typer.Exit(1)
