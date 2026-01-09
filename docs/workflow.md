# TrustBridge End-to-End Workflow Guide

This document provides a comprehensive guide for both **Providers** (model publishers) and **Consumers** (model deployers) using the TrustBridge platform.

---

## Table of Contents

1. [Overview](#overview)
2. [Architecture](#architecture)
3. [Provider Workflow](#provider-workflow)
4. [Consumer Workflow](#consumer-workflow)
5. [E2E Demo](#e2e-demo)
6. [Environment Variables](#environment-variables)
7. [Security Considerations](#security-considerations)
8. [Troubleshooting](#troubleshooting)

---

## Overview

TrustBridge enables secure distribution of large AI model weights with:

- **Encryption at rest**: Model weights encrypted using AES-256-GCM chunked format (tbenc/v1)
- **Contract-based licensing**: Authorization required before model access
- **Runtime isolation**: Model server not externally accessible
- **Audit trail**: All inference requests logged for compliance
- **Usage billing**: Integration with Azure Marketplace metering

### Workflow Summary

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                           PROVIDER WORKFLOW                                  │
│                                                                             │
│   Model Weights ──► Encrypt ──► Upload ──► Register ──► Build ──► Package  │
│   (.safetensors)    (tbenc)     (Blob)     (Control    (Docker)   (Managed │
│                                             Plane)                 App)     │
└─────────────────────────────────────────────────────────────────────────────┘
                                    │
                                    ▼
                        ┌─────────────────────┐
                        │    Control Plane    │
                        │  (External Service) │
                        └─────────────────────┘
                                    │
                                    ▼
┌─────────────────────────────────────────────────────────────────────────────┐
│                           CONSUMER WORKFLOW                                  │
│                                                                             │
│   Deploy ──► Authorize ──► Hydrate ──► Decrypt ──► Ready ──► Inference     │
│   (Azure)    (License)     (Download)   (FIFO)     (Proxy)   (Requests)    │
└─────────────────────────────────────────────────────────────────────────────┘
```

---

## Architecture

### Components

| Component | Description | Technology |
|-----------|-------------|------------|
| **Provider CLI** | Encrypts, uploads, registers model assets | Python 3.10+, Typer |
| **Sentinel** | Security sidecar - authorization, decryption, proxy | Go 1.21+ |
| **Runtime** | Model inference server (vLLM/Triton) | Docker, CUDA |
| **Control Plane** | Contract validation, key management | External service |
| **Azure Blob** | Encrypted asset storage | Azure Storage |

### Security Model

```
┌────────────────────────────────────────────────────────────────┐
│                     Consumer Environment                        │
│  ┌──────────────────────────────────────────────────────────┐  │
│  │                    GPU VM / AKS Node                      │  │
│  │                                                           │  │
│  │   ┌─────────────────┐      ┌─────────────────────────┐   │  │
│  │   │    Sentinel     │      │       Runtime           │   │  │
│  │   │                 │      │                         │   │  │
│  │   │  Port 8000 ◄────┼──────┤  Port 8081 (localhost)  │   │  │
│  │   │  (Public API)   │ FIFO │  (Internal Only)        │   │  │
│  │   │                 │──────►                         │   │  │
│  │   │  - Auth         │      │  - vLLM/Triton          │   │  │
│  │   │  - Proxy        │      │  - Model Inference      │   │  │
│  │   │  - Audit        │      │                         │   │  │
│  │   │  - Billing      │      │                         │   │  │
│  │   └─────────────────┘      └─────────────────────────┘   │  │
│  │                                                           │  │
│  │   /dev/shm/model-pipe  ◄── Decrypted weights (FIFO)      │  │
│  │   /mnt/resource/       ◄── Encrypted weights only        │  │
│  └──────────────────────────────────────────────────────────┘  │
│                                                                 │
│  NSG Rules:                                                     │
│  - Allow: Port 8000 (Sentinel API)                             │
│  - Block: Port 8081 (Runtime - internal only)                  │
└────────────────────────────────────────────────────────────────┘
```

---

## Provider Workflow

Providers prepare and publish encrypted model packages for consumer deployment.

### Prerequisites

- Python 3.10+ with pip
- Azure subscription with:
  - Storage account (Blob)
  - Container Registry (ACR)
  - Control Plane access
- Docker (for building images)

### Step 1: Install Provider CLI

```bash
cd src/cli
pip install -r requirements.txt

# Verify installation
python -m trustbridge_cli.main --help
```

### Step 2: Encrypt Model Weights

Convert plaintext model weights to the secure tbenc/v1 format.

```bash
trustbridge encrypt \
  --asset-id "my-model-v1" \
  --in /path/to/model.safetensors \
  --out model.tbenc \
  --manifest model.manifest.json \
  --chunk-bytes 4194304  # 4MB chunks (default)
```

**Output:**
- `model.tbenc` - Encrypted model file
- `model.manifest.json` - Metadata including SHA256 checksum
- Decryption key (hex) - printed to stdout, save securely!

**Manifest format:**
```json
{
  "format": "tbenc/v1",
  "algo": "aes-256-gcm-chunked",
  "chunk_bytes": 4194304,
  "plaintext_bytes": 53821440000,
  "sha256_ciphertext": "abc123...",
  "asset_id": "my-model-v1",
  "weights_filename": "model.tbenc",
  "allow_finetune": true
}
```

### Step 3: Upload to Azure Blob Storage

Upload encrypted artifacts to your Azure storage account.

```bash
trustbridge upload \
  --storage-account "mystorageaccount" \
  --container "models" \
  --asset-id "my-model-v1" \
  --encrypted-file model.tbenc \
  --manifest model.manifest.json
```

**Result:**
- `models/my-model-v1/model.tbenc` in Blob storage
- `models/my-model-v1/model.manifest.json` in Blob storage

### Step 4: Register with Control Plane

Register the asset and decryption key with the Control Plane.

```bash
trustbridge register \
  --edc-endpoint "https://controlplane.example.com" \
  --asset-id "my-model-v1" \
  --manifest model.manifest.json \
  --key-hex "<64-character-hex-key>"
```

**What this does:**
- Registers asset metadata (size, hash, blob URLs)
- Stores decryption key securely in Control Plane vault
- Associates asset with provider account

### Step 5: Build Docker Image

Build and push the sentinel + runtime Docker image.

```bash
trustbridge build \
  --registry "myacr.azurecr.io" \
  --image-name "trustbridge-runtime" \
  --tag "v1.0.0" \
  --sentinel-binary ./sentinel
```

**Image contents:**
- Base: vLLM/Triton runtime
- Sentinel binary
- Runtime entrypoint script
- NO model weights (downloaded at runtime)

### Step 6: Generate Managed App Package

Create the Azure Managed App deployment package.

```bash
trustbridge package \
  --asset-id "my-model-v1" \
  --edc-endpoint "https://controlplane.example.com" \
  --image "myacr.azurecr.io/trustbridge-runtime:v1.0.0" \
  --output-zip managed-app.zip
```

**Package contents:**
- `mainTemplate.json` - ARM deployment template
- `createUiDefinition.json` - Azure Portal UI
- Metadata files for marketplace

### Step 7: Publish (Optional - All-in-One)

Run the complete workflow with a single command:

```bash
trustbridge publish \
  --asset-id "my-model-v1" \
  --in /path/to/model.safetensors \
  --storage-account "mystorageaccount" \
  --container "models" \
  --edc-endpoint "https://controlplane.example.com" \
  --registry "myacr.azurecr.io" \
  --output-zip managed-app.zip
```

---

## Consumer Workflow

Consumers deploy and use encrypted models in their Azure environment.

### Prerequisites

- Azure subscription with GPU quota
- Valid contract ID from model provider
- Access to Azure Marketplace (or direct package)

### Step 1: Deploy Managed App

**Option A: Azure Marketplace**
1. Find the model in Azure Marketplace
2. Click "Create"
3. Fill in deployment parameters:
   - VM Size (e.g., `Standard_NC24ads_A100_v4`)
   - Contract ID (from provider)
   - Region

**Option B: Direct ARM Deployment**
```bash
az deployment group create \
  --resource-group "my-rg" \
  --template-file mainTemplate.json \
  --parameters \
    vmSize="Standard_NC24ads_A100_v4" \
    contractId="contract-123" \
    assetId="my-model-v1" \
    edcEndpoint="https://controlplane.example.com"
```

### Step 2: Sentinel Startup Sequence

The sentinel container automatically executes:

```
Boot → Authorize → Hydrate → Decrypt → Ready
```

**State transitions:**

| State | Description | Duration |
|-------|-------------|----------|
| `Boot` | Load configuration, initialize | ~1s |
| `Authorize` | Call Control Plane API | ~2-5s |
| `Hydrate` | Download encrypted weights | Varies (network) |
| `Decrypt` | Decrypt to FIFO, signal runtime | ~30s per GB |
| `Ready` | Proxy accepting requests | Indefinite |

### Step 3: Monitor Health

Check sentinel status via health endpoints:

```bash
# Liveness check
curl http://<vm-ip>:8000/health
# Returns 200 when Ready, 503 otherwise

# Readiness check
curl http://<vm-ip>:8000/readiness
# Returns 200 when Decrypt or Ready

# Detailed status
curl http://<vm-ip>:8000/status
# Returns JSON with state, uptime, asset_id
```

**Example status response:**
```json
{
  "state": "Ready",
  "asset_id": "my-model-v1",
  "contract_id": "contract-123",
  "uptime_seconds": 3600,
  "requests_processed": 1250
}
```

### Step 4: Send Inference Requests

Once sentinel reaches `Ready` state, send inference requests:

```bash
# OpenAI-compatible API (if using vLLM)
curl -X POST http://<vm-ip>:8000/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{
    "model": "my-model-v1",
    "messages": [
      {"role": "user", "content": "Hello, how are you?"}
    ]
  }'
```

**Request flow:**
1. Request hits Sentinel (port 8000)
2. Sentinel validates and audits
3. Request forwarded to Runtime (localhost:8081)
4. Response returned through Sentinel
5. Audit log entry created

### Step 5: View Audit Logs

Sentinel generates JSON audit logs for all requests:

```json
{
  "ts": "2026-01-08T10:00:00Z",
  "contract_id": "contract-123",
  "asset_id": "my-model-v1",
  "method": "POST",
  "path": "/v1/chat/completions",
  "req_sha256": "abc123...",
  "status": 200,
  "latency_ms": 450
}
```

Access logs via:
- Container logs: `docker logs sentinel`
- Azure Monitor (if configured)
- Log file in container

---

## E2E Demo

Test the complete workflow locally using Docker Compose.

### Prerequisites

- Docker and Docker Compose
- Python 3.10+
- Go 1.21+

### Quick Start

```bash
# Navigate to E2E directory
cd e2e

# Run complete E2E workflow
make e2e
```

### Step-by-Step

```bash
# 1. Generate test data (16MB deterministic file)
make e2e-generate-plain

# 2. Encrypt test weights
make e2e-encrypt

# 3. Start all services
make e2e-up

# 4. Run automated tests
make e2e-test

# 5. Clean up
make e2e-down
```

### E2E Services

| Service | Port | Description |
|---------|------|-------------|
| `blob-server` | 9000 | Serves encrypted artifacts |
| `controlplane` | 8080 | Mock authorization API |
| `sentinel` | 8000 | Security sidecar |
| `runtime-mock` | 8081* | Mock inference server |

*Port 8081 is only accessible within Docker network

### Manual Testing

```bash
# Check sentinel health
curl http://localhost:8000/health

# Send test request
curl -X POST http://localhost:8000/v1/demo \
  -H "Content-Type: application/json" \
  -d '{"test": "data"}'

# Check runtime received decrypted data
curl http://localhost:8000/plaintext-hash
```

### Test Authorization Denial

```bash
# Start with denied contract
TB_CONTRACT_ID=contract-deny docker compose up sentinel

# Verify sentinel blocks access (should return 503 or never reach Ready)
curl http://localhost:8000/health  # Should fail
```

---

## Environment Variables

### Sentinel Configuration

| Variable | Required | Default | Description |
|----------|----------|---------|-------------|
| `TB_CONTRACT_ID` | Yes | - | Contract identifier |
| `TB_ASSET_ID` | Yes | - | Asset identifier |
| `TB_EDC_ENDPOINT` | Yes | - | Control Plane URL |
| `TB_TARGET_DIR` | No | `/mnt/resource/trustbridge` | Encrypted download path |
| `TB_PIPE_PATH` | No | `/dev/shm/model-pipe` | FIFO path for decrypted data |
| `TB_READY_SIGNAL` | No | `/dev/shm/weights/ready.signal` | Runtime ready signal file |
| `TB_RUNTIME_URL` | No | `http://127.0.0.1:8081` | Runtime endpoint |
| `TB_PUBLIC_ADDR` | No | `0.0.0.0:8000` | Sentinel listen address |
| `TB_HEALTH_ADDR` | No | `0.0.0.0:8001` | Health endpoint address |
| `TB_DOWNLOAD_CONCURRENCY` | No | `4` | Parallel download threads |
| `TB_DOWNLOAD_CHUNK_BYTES` | No | `8388608` | Download chunk size |
| `TB_LOG_LEVEL` | No | `info` | Logging level |

### Billing Configuration

| Variable | Required | Default | Description |
|----------|----------|---------|-------------|
| `TB_BILLING_ENABLED` | No | `false` | Enable Azure metering |
| `TB_BILLING_INTERVAL` | No | `60s` | Report interval |
| `TB_BILLING_RESOURCE_ID` | If billing | - | Azure resource ID |
| `TB_BILLING_PLAN_ID` | If billing | - | Billing plan ID |
| `TB_BILLING_DIMENSION` | If billing | - | Usage dimension |

---

## Security Considerations

### Provider Responsibilities

1. **Key Management**
   - Store decryption keys securely
   - Never commit keys to version control
   - Rotate keys if compromised

2. **Access Control**
   - Use private blob containers
   - Restrict Control Plane registration API
   - Audit key access

3. **Asset Integrity**
   - Verify SHA256 before upload
   - Use secure channels for key transmission

### Consumer Responsibilities

1. **Network Security**
   - Ensure port 8081 is never exposed
   - Use NSG rules to restrict access
   - Consider private endpoints for blob access

2. **Contract Management**
   - Keep contract IDs confidential
   - Monitor authorization failures
   - Review audit logs regularly

3. **Runtime Security**
   - Use managed identity where possible
   - Rotate credentials regularly
   - Monitor for unusual access patterns

### Security Invariants

The following must **always** be true:

- [ ] No plaintext weights on persistent disk
- [ ] Runtime port (8081) not externally accessible
- [ ] Decryption keys never logged
- [ ] SAS URLs not logged in production
- [ ] FIFO permissions are 0600
- [ ] Audit logs capture all access

---

## Troubleshooting

### Common Issues

#### Sentinel stuck in "Authorize" state

**Symptoms:** Health returns 503, logs show authorization retries

**Causes:**
- Invalid contract ID
- Control Plane unreachable
- Contract expired/inactive

**Solutions:**
1. Verify contract ID is correct
2. Check network connectivity to Control Plane
3. Contact provider to verify contract status

#### Download failures

**Symptoms:** Sentinel stuck in "Hydrate" state, download errors in logs

**Causes:**
- SAS URL expired
- Network connectivity issues
- Insufficient disk space

**Solutions:**
1. Check `TB_TARGET_DIR` has sufficient space
2. Verify network access to blob storage
3. Sentinel will auto-retry with new SAS on expiry

#### Decryption failures

**Symptoms:** Sentinel fails in "Decrypt" state, GCM authentication errors

**Causes:**
- Corrupted download (hash mismatch)
- Wrong decryption key
- Tampered ciphertext

**Solutions:**
1. Re-download the encrypted asset
2. Verify SHA256 matches manifest
3. Contact provider if issue persists

#### Runtime not receiving data

**Symptoms:** Runtime logs show "waiting for model", sentinel is Ready

**Causes:**
- FIFO not created
- Permission issues on FIFO
- Runtime not reading from correct path

**Solutions:**
1. Verify `TB_PIPE_PATH` matches runtime config
2. Check FIFO exists: `ls -la /dev/shm/model-pipe`
3. Verify runtime is configured to read from FIFO

### Debug Commands

```bash
# Check sentinel logs
docker logs sentinel

# Check sentinel state
curl http://localhost:8000/status

# Verify encrypted file downloaded
ls -la /mnt/resource/trustbridge/

# Check FIFO exists
ls -la /dev/shm/model-pipe

# Verify no plaintext on disk
find /mnt/resource/trustbridge -type f -name "*.tbenc" -o -name "*.json"
# Should only show .tbenc and .json files, no plaintext

# Test network connectivity
curl -I http://controlplane:8080/api/v1/health
```

### Getting Help

- Check logs for specific error messages
- Review the [implementation.md](../implementation.md) specification
- File issues at project repository

---

## Appendix: API Reference

### Control Plane API

**POST /api/v1/license/authorize**

Request:
```json
{
  "contract_id": "contract-123",
  "asset_id": "my-model-v1",
  "hw_id": "<hardware-fingerprint>",
  "client_version": "sentinel/1.0.0"
}
```

Response (authorized):
```json
{
  "status": "authorized",
  "sas_url": "https://storage.blob.core.windows.net/...",
  "manifest_url": "https://storage.blob.core.windows.net/...",
  "decryption_key_hex": "abc123...",
  "expires_at": "2026-01-08T12:00:00Z"
}
```

Response (denied):
```json
{
  "status": "denied",
  "reason": "contract_inactive"
}
```

### Sentinel Health API

**GET /health**
- Returns 200 if state is Ready
- Returns 503 otherwise

**GET /readiness**
- Returns 200 if state >= Decrypt
- Returns 503 otherwise

**GET /status**
```json
{
  "state": "Ready",
  "asset_id": "my-model-v1",
  "contract_id": "contract-123",
  "uptime_seconds": 3600,
  "requests_processed": 1250
}
```

---

*Last updated: January 2026*
