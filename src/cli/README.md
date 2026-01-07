# TrustBridge Provider CLI

Command-line interface for publishing secure model distributions to the TrustBridge platform and Azure Marketplace.

## Overview

The TrustBridge CLI enables AI model providers to:
- **Encrypt** model weights using secure chunked AES-256-GCM (tbenc/v1 format)
- **Upload** encrypted assets to Azure Blob Storage
- **Register** assets with the TrustBridge Control Plane
- **Build** Docker images with sentinel and runtime
- **Package** Azure Managed Applications for Marketplace distribution

## Installation

### Prerequisites

- Python 3.10 or higher
- Azure CLI (`az`) - for Azure authentication
- Docker - for building container images
- Compiled sentinel binary (from Go source)

### Install Dependencies

```bash
cd src/cli
pip install -r requirements.txt
```

## Quick Start

### Full Publish Workflow

The fastest way to publish a model is using the `publish` command:

```bash
python -m trustbridge_cli.main publish model.safetensors \
    --asset-id llama-3-70b-v1 \
    --storage-account mystorageaccount \
    --registry myregistry.azurecr.io \
    --sentinel-binary ./bin/sentinel
```

This orchestrates all steps: encrypt → upload → register → build → package

### Individual Commands

For more control, run each step individually:

#### 1. Encrypt Model Weights

```bash
python -m trustbridge_cli.main encrypt model.safetensors \
    --asset-id llama-3-70b-v1 \
    --out ./encrypted
```

**Outputs:**
- `./encrypted/model.tbenc` - Encrypted ciphertext
- `./encrypted/model.manifest.json` - Manifest with metadata
- **Decryption key** (printed to console) - **SAVE THIS!**

#### 2. Upload to Azure Blob

```bash
python -m trustbridge_cli.main upload \
    --storage-account mystorageaccount \
    --container models \
    --asset-id llama-3-70b-v1 \
    --encrypted-file ./encrypted/model.tbenc \
    --manifest ./encrypted/model.manifest.json
```

**Prerequisites:** Azure authentication (`az login`)

#### 3. Register with Control Plane

```bash
python -m trustbridge_cli.main register \
    --asset-id llama-3-70b-v1 \
    --key-hex <64-char-hex-key> \
    --storage-account mystorageaccount \
    --manifest ./encrypted/model.manifest.json
```

**Note:** The decryption key from step 1 is required here.

#### 4. Build Docker Image

```bash
python -m trustbridge_cli.main build \
    --registry myregistry.azurecr.io \
    --image-name trustbridge-runtime \
    --tag v1.0 \
    --sentinel-binary ./bin/sentinel
```

**Prerequisites:**
- Docker running
- Registry authentication (`docker login myregistry.azurecr.io`)

#### 5. Package Managed App

```bash
python -m trustbridge_cli.main package \
    --asset-id llama-3-70b-v1 \
    --image myregistry.azurecr.io/trustbridge-runtime:v1.0 \
    --output ./trustbridge-app.zip
```

**Output:** ZIP package ready for Azure Marketplace

## Configuration

### Environment Variables

Create a `.env` file or set environment variables:

```bash
export AZURE_STORAGE_ACCOUNT=mystorageaccount
export DOCKER_REGISTRY=myregistry.azurecr.io
export TB_EDC_ENDPOINT=https://controlplane.trustbridge.io
```

See `.env.example` for all available variables.

### Configuration File

Optionally create `~/.trustbridge/config.toml`:

```toml
[azure]
storage_account = "mystorageaccount"
container = "models"
registry = "myregistry.azurecr.io"

[controlplane]
endpoint = "https://controlplane.trustbridge.io"

[encryption]
default_chunk_bytes = 4194304  # 4MB
```

## Commands Reference

### Global Options

- `--help` - Show help message
- `--version` - Show version

### encrypt

Encrypt model weights to tbenc/v1 format.

```bash
trustbridge encrypt <input-file> --asset-id <id> [options]
```

**Options:**
- `--out` - Output directory (default: `./encrypted`)
- `--chunk-bytes` - Chunk size in bytes (default: 4MB)

### upload

Upload encrypted assets to Azure Blob Storage.

```bash
trustbridge upload --storage-account <account> --asset-id <id> [options]
```

**Options:**
- `--container` - Container name (default: `models`)
- `--encrypted-file` - Path to .tbenc file
- `--manifest` - Path to manifest.json

### register

Register asset with Control Plane / EDC.

```bash
trustbridge register --asset-id <id> --key-hex <key> [options]
```

**Options:**
- `--edc-endpoint` - Control Plane URL
- `--storage-account` - Storage account (for blob URL construction)
- `--manifest` - Path to manifest.json

### build

Build and push Docker image.

```bash
trustbridge build --registry <registry> --sentinel-binary <path> [options]
```

**Options:**
- `--image-name` - Image name (default: `trustbridge-runtime`)
- `--tag` - Image tag (default: `latest`)
- `--base-image` - Base image (default: `nvcr.io/nvidia/vllm:latest`)
- `--no-push` - Build only, don't push

### package

Generate Azure Managed App package.

```bash
trustbridge package --asset-id <id> --image <full-image> [options]
```

**Options:**
- `--output` - Output ZIP path
- `--edc-endpoint` - Control Plane URL

### publish

Orchestrate full workflow.

```bash
trustbridge publish <input-file> --asset-id <id> \
    --storage-account <account> --registry <registry> \
    --sentinel-binary <path> [options]
```

**Options:**
- All options from individual commands
- `--skip-encrypt`, `--skip-upload`, etc. - Skip specific steps
- `--yes` - Skip confirmation prompt

## Authentication

### Azure

The CLI uses `DefaultAzureCredential` which tries (in order):

1. Environment variables (`AZURE_CLIENT_ID`, `AZURE_CLIENT_SECRET`, `AZURE_TENANT_ID`)
2. Managed identity (when running in Azure)
3. Azure CLI (`az login`)
4. Visual Studio Code
5. Azure PowerShell

**Recommended:** Use `az login` for development.

### Docker Registry

Authenticate before building:

```bash
# Azure Container Registry
az acr login --name myregistry

# Docker Hub
docker login

# Other registries
docker login myregistry.example.com
```

## Troubleshooting

### "Docker daemon not running"

Start Docker Desktop or the Docker service.

### "Azure authentication failed"

Run `az login` to authenticate with Azure.

### "Container not found"

Create the storage container:

```bash
az storage container create --name models --account-name mystorageaccount
```

### "No module named 'typer'"

Install dependencies:

```bash
pip install -r requirements.txt
```

## Security Best Practices

1. **Never commit decryption keys** to version control
2. **Store keys securely** in Azure Key Vault or similar
3. **Use managed identities** when running in Azure
4. **Don't log SAS URLs** (they contain auth tokens)
5. **Rotate keys regularly** for long-term deployments

## Examples

### Publishing a LLaMA 3 70B Model

```bash
# Full workflow
python -m trustbridge_cli.main publish llama-3-70b.safetensors \
    --asset-id llama-3-70b-v1 \
    --storage-account prodmodels \
    --registry prodregistry.azurecr.io \
    --sentinel-binary ./bin/sentinel \
    --output ./publish-llama-70b \
    --yes

# Resume after failure (skip completed steps)
python -m trustbridge_cli.main publish llama-3-70b.safetensors \
    --asset-id llama-3-70b-v1 \
    --storage-account prodmodels \
    --registry prodregistry.azurecr.io \
    --sentinel-binary ./bin/sentinel \
    --skip-encrypt --skip-upload --skip-register
```

### Custom Chunk Size for Large Models

```bash
python -m trustbridge_cli.main encrypt large-model.safetensors \
    --asset-id gpt-4-175b-v1 \
    --chunk-bytes 16777216  # 16MB chunks for faster processing
```

### Using Custom Control Plane

```bash
python -m trustbridge_cli.main publish model.safetensors \
    --asset-id my-model-v1 \
    --storage-account myaccount \
    --registry myregistry.azurecr.io \
    --sentinel-binary ./bin/sentinel \
    --edc-endpoint https://my-controlplane.example.com
```

## Development

### Running Tests

```bash
pytest src/cli/trustbridge_cli/tests/
```

### Code Coverage

```bash
pytest --cov=trustbridge_cli --cov-report=html
```

## Support

- **Documentation:** https://github.com/trustbridge/trustbridge
- **Issues:** https://github.com/trustbridge/trustbridge/issues
- **Control Plane:** Contact your TrustBridge administrator

## License

See LICENSE file in project root.
