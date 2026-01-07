"""
Blob upload command - Upload encrypted assets to Azure Blob Storage.

This command handles uploading encrypted model weights and manifests
to Azure Blob Storage with proper path structure and error handling.
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
    warning,
    display_table,
    validate_file_exists,
    retry_with_backoff,
    TrustBridgeError,
    AzureError,
    NetworkError,
)


def _get_azure_credential():
    """
    Get Azure credential using DefaultAzureCredential.

    Returns:
        Azure credential object

    Raises:
        AzureError: If authentication fails
    """
    try:
        from azure.identity import DefaultAzureCredential

        return DefaultAzureCredential()
    except ImportError:
        raise AzureError(
            "Azure SDK not installed",
            details="Run: pip install azure-identity azure-storage-blob",
        )
    except Exception as e:
        raise AzureError(
            "Failed to authenticate with Azure",
            details=(
                f"Ensure you're logged in with 'az login' or have environment credentials set.\n"
                f"Error: {e}"
            ),
        )


@retry_with_backoff(max_attempts=3, retryable_exceptions=(NetworkError, Exception))
def _upload_blob(
    blob_client,
    file_path: Path,
    show_progress: bool = True,
) -> None:
    """
    Upload a single file to Azure Blob Storage with retry logic.

    Args:
        blob_client: Azure BlobClient instance
        file_path: Path to file to upload
        show_progress: Whether to show progress (default: True)

    Raises:
        NetworkError: If upload fails due to network issues
    """
    try:
        file_size = file_path.stat().st_size

        if show_progress:
            from rich.progress import (
                Progress,
                BarColumn,
                TextColumn,
                TransferSpeedColumn,
                TimeRemainingColumn,
            )

            with Progress(
                TextColumn("[bold blue]{task.description}"),
                BarColumn(),
                "[progress.percentage]{task.percentage:>3.1f}%",
                "•",
                TransferSpeedColumn(),
                "•",
                TimeRemainingColumn(),
                console=console.console,
            ) as progress:
                task = progress.add_task(
                    f"Uploading {file_path.name}", total=file_size
                )

                def progress_callback(current, total):
                    progress.update(task, completed=current)

                with open(file_path, "rb") as data:
                    blob_client.upload_blob(
                        data,
                        overwrite=True,
                        max_concurrency=4,
                    )
                    progress.update(task, completed=file_size)
        else:
            with open(file_path, "rb") as data:
                blob_client.upload_blob(data, overwrite=True, max_concurrency=4)

    except Exception as e:
        # Check if it's a network-related error
        error_str = str(e).lower()
        if any(
            keyword in error_str
            for keyword in ["timeout", "connection", "network", "socket"]
        ):
            raise NetworkError(f"Network error during upload: {e}")
        raise


def _do_upload(
    storage_account: str,
    container: str,
    asset_id: str,
    encrypted_file: Path,
    manifest_file: Path,
) -> tuple[str, str]:
    """
    Internal function to perform blob upload.

    Args:
        storage_account: Azure storage account name
        container: Container name
        asset_id: Asset identifier
        encrypted_file: Path to encrypted file
        manifest_file: Path to manifest file

    Returns:
        Tuple of (encrypted_blob_url, manifest_blob_url)

    Raises:
        AzureError: If upload fails
    """
    try:
        from azure.storage.blob import BlobServiceClient
        from azure.core.exceptions import ResourceNotFoundError, HttpResponseError

        # Get credentials
        credential = _get_azure_credential()

        # Create blob service client
        account_url = f"https://{storage_account}.blob.core.windows.net"
        blob_service_client = BlobServiceClient(
            account_url=account_url, credential=credential
        )

        # Get container client
        try:
            container_client = blob_service_client.get_container_client(container)
            # Check if container exists
            container_client.get_container_properties()
        except ResourceNotFoundError:
            raise AzureError(
                f"Container '{container}' not found in storage account '{storage_account}'",
                details=f"Create it with: az storage container create --name {container} --account-name {storage_account}",
            )
        except HttpResponseError as e:
            if e.status_code == 403:
                raise AzureError(
                    f"Access denied to container '{container}'",
                    details="Ensure you have Storage Blob Data Contributor role or appropriate permissions",
                )
            raise

        # Define blob paths
        encrypted_blob_name = f"models/{asset_id}/model.tbenc"
        manifest_blob_name = f"models/{asset_id}/model.manifest.json"

        # Upload encrypted file
        info(f"Uploading encrypted file to {encrypted_blob_name}...")
        encrypted_blob_client = container_client.get_blob_client(encrypted_blob_name)
        _upload_blob(encrypted_blob_client, encrypted_file)

        # Upload manifest
        info(f"Uploading manifest to {manifest_blob_name}...")
        manifest_blob_client = container_client.get_blob_client(manifest_blob_name)
        _upload_blob(manifest_blob_client, manifest_file, show_progress=False)

        # Get blob URLs (without SAS)
        encrypted_url = f"{account_url}/{container}/{encrypted_blob_name}"
        manifest_url = f"{account_url}/{container}/{manifest_blob_name}"

        return encrypted_url, manifest_url

    except AzureError:
        raise
    except ImportError:
        raise AzureError(
            "Azure Storage SDK not installed",
            details="Run: pip install azure-storage-blob",
        )
    except Exception as e:
        raise AzureError(f"Upload failed", details=str(e))


def command(
    storage_account: Annotated[
        str,
        typer.Option(
            "--storage-account",
            help="Azure storage account name",
            envvar="AZURE_STORAGE_ACCOUNT",
        ),
    ],
    container: Annotated[
        str,
        typer.Option(
            "--container",
            help="Storage container name",
            envvar="AZURE_STORAGE_CONTAINER",
        ),
    ] = "models",
    asset_id: Annotated[
        str,
        typer.Option(
            "--asset-id",
            help="Asset identifier (must match encryption asset ID)",
        ),
    ] = ...,
    encrypted_file: Annotated[
        Path,
        typer.Option(
            "--encrypted-file",
            help="Path to encrypted .tbenc file",
            exists=True,
            file_okay=True,
            dir_okay=False,
        ),
    ] = Path("./encrypted/model.tbenc"),
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
) -> None:
    """
    Upload encrypted assets to Azure Blob Storage.

    This command uploads your encrypted model weights and manifest to
    Azure Blob Storage with the following structure:

        models/<asset-id>/model.tbenc
        models/<asset-id>/model.manifest.json

    Prerequisites:
        - Azure CLI installed and logged in (az login), OR
        - Azure credentials set in environment variables, OR
        - Managed identity (when running in Azure)

    Examples:

        # Upload with explicit storage account
        trustbridge upload --storage-account myaccount \\
            --asset-id llama-3-70b-v1

        # Upload with environment variables
        export AZURE_STORAGE_ACCOUNT=myaccount
        export AZURE_STORAGE_CONTAINER=models
        trustbridge upload --asset-id llama-3-70b-v1

        # Upload custom files
        trustbridge upload --storage-account myaccount \\
            --asset-id my-model \\
            --encrypted-file ./output/encrypted.tbenc \\
            --manifest ./output/manifest.json

    Output:
        Blob URLs for the uploaded files (without SAS tokens).
        These URLs will be used during asset registration.

    Authentication:
        Uses DefaultAzureCredential which tries in order:
        1. Environment variables (AZURE_CLIENT_ID, AZURE_CLIENT_SECRET, AZURE_TENANT_ID)
        2. Managed identity
        3. Azure CLI credentials (az login)
        4. Visual Studio Code credentials
        5. Azure PowerShell credentials
    """
    try:
        # Validate inputs
        info("Validating inputs...")
        validate_file_exists(encrypted_file)
        validate_file_exists(manifest_file)

        if not storage_account:
            raise AzureError(
                "Storage account name is required",
                details="Provide via --storage-account or AZURE_STORAGE_ACCOUNT environment variable",
            )

        # Display upload parameters
        console.console.print()
        display_table(
            "Upload Parameters",
            [
                ("Storage Account", storage_account),
                ("Container", container),
                ("Asset ID", asset_id),
                ("Encrypted File", str(encrypted_file)),
                (
                    "Encrypted Size",
                    f"{encrypted_file.stat().st_size / (1024**3):.2f} GB",
                ),
                ("Manifest File", str(manifest_file)),
            ],
        )
        console.console.print()

        # Perform upload
        encrypted_url, manifest_url = _do_upload(
            storage_account=storage_account,
            container=container,
            asset_id=asset_id,
            encrypted_file=encrypted_file,
            manifest_file=manifest_file,
        )

        # Display results
        console.console.print()
        display_table(
            "Upload Results",
            [
                ("Encrypted Blob URL", encrypted_url),
                ("Manifest Blob URL", manifest_url),
            ],
        )

        success("Upload completed successfully!")
        console.console.print()
        info("Next steps:")
        console.console.print(
            "  1. Save these blob URLs for asset registration"
        )
        console.console.print(
            "  2. Register with Control Plane: trustbridge register ..."
        )

        # Warn about blob URLs
        warning(
            "These URLs do not include SAS tokens. "
            "The Control Plane will generate SAS URLs for authorized consumers."
        )

    except TrustBridgeError as e:
        error(e.message, details=e.details)
        raise typer.Exit(1)
    except Exception as e:
        error(f"Unexpected error during upload", details=str(e))
        raise typer.Exit(1)
