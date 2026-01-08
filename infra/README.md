# TrustBridge Azure Infrastructure

This directory contains Azure deployment templates for the TrustBridge secure inference platform.

## Files

| File | Description |
|------|-------------|
| `main.bicep` | Azure Bicep template for GPU VM deployment |
| `createUiDefinition.json` | Azure Portal UI definition for Managed App |
| `cloud-init/init.sh` | VM bootstrap script (Docker, NVIDIA, containers) |
| `scripts/validate-deployment.sh` | Post-deployment validation script |
| `scripts/deploy-test.sh` | Interactive deployment test script |

## Quick Start

### Prerequisites

1. Azure CLI installed and logged in
2. SSH key pair (will be generated if not exists)
3. Container images pushed to Azure Container Registry

### Deploy Test Environment

```bash
# Interactive deployment with prompts
./scripts/deploy-test.sh trustbridge-test-rg eastus
```

### Manual Deployment

```bash
# Create resource group
az group create --name trustbridge-rg --location eastus

# Deploy infrastructure
az deployment group create \
  --resource-group trustbridge-rg \
  --template-file main.bicep \
  --parameters \
    namePrefix=trustbridge \
    contractId=your-contract-id \
    assetId=your-asset-id \
    edcEndpoint=https://your-controlplane.com \
    sentinelImage=youracr.azurecr.io/sentinel:latest \
    runtimeImage=youracr.azurecr.io/runtime:latest \
    acrName=youracr \
    adminSshKey="$(cat ~/.ssh/id_rsa.pub)"
```

### Validate Deployment

```bash
# Get VM IP from deployment outputs
VM_IP=$(az deployment group show \
  --name <deployment-name> \
  --resource-group trustbridge-rg \
  --query "properties.outputs.vmPublicIp.value" -o tsv)

# Run validation
./scripts/validate-deployment.sh $VM_IP azureuser ~/.ssh/id_rsa
```

## Architecture

```
┌─────────────────────────────────────────────────────────┐
│                     Azure VM (GPU)                       │
│  ┌─────────────────────────────────────────────────────┤
│  │  NSG Rules:                                          │
│  │    - Port 8000: ALLOW (Sentinel API)                │
│  │    - Port 8001: ALLOW (Health endpoint)             │
│  │    - Port 8081: DENY  (Runtime - SECURITY)          │
│  │    - Port 22:   ALLOW (SSH, restricted)             │
│  ├─────────────────────────────────────────────────────┤
│  │                                                      │
│  │  ┌────────────────┐     ┌────────────────┐          │
│  │  │   Sentinel     │     │    Runtime     │          │
│  │  │   :8000/:8001  │────▶│  127.0.0.1:8081│          │
│  │  │                │     │   (localhost)   │          │
│  │  └───────┬────────┘     └────────┬───────┘          │
│  │          │                       │                   │
│  │          ▼                       ▼                   │
│  │  ┌─────────────────────────────────────┐            │
│  │  │     /dev/shm/trustbridge (FIFO)     │            │
│  │  │     Shared memory for decrypted     │            │
│  │  │     model weights (never on disk)   │            │
│  │  └─────────────────────────────────────┘            │
│  │                                                      │
│  │  ┌─────────────────────────────────────┐            │
│  │  │  /mnt/resource/trustbridge          │            │
│  │  │  Ephemeral storage for encrypted    │            │
│  │  │  downloads only                     │            │
│  │  └─────────────────────────────────────┘            │
│  │                                                      │
└──┴──────────────────────────────────────────────────────┘
```

## Security Invariants

The deployment enforces these security requirements:

1. **Runtime isolation**: Port 8081 is explicitly blocked at the NSG level
2. **No plaintext on disk**: Model weights only exist in shared memory (FIFO/tmpfs)
3. **Ephemeral storage**: Encrypted downloads use VM ephemeral disk
4. **Managed identity**: VM uses system-assigned identity for ACR authentication
5. **SSH restrictions**: SSH can be limited to specific source IPs

## Validation Tests

The `validate-deployment.sh` script checks:

- ✓ SSH connectivity
- ✓ Docker service running
- ✓ NVIDIA GPU detection
- ✓ Container status
- ✓ Health endpoint (internal & external)
- ✓ Runtime port blocked externally (SECURITY)
- ✓ No plaintext files on disk (SECURITY)
- ✓ FIFO configuration
- ✓ Systemd service status
- ✓ Configuration files present

## Troubleshooting

### View cloud-init logs
```bash
ssh azureuser@$VM_IP 'sudo cat /var/log/trustbridge-init.log'
```

### View container logs
```bash
ssh azureuser@$VM_IP 'cd /opt/trustbridge && docker compose logs -f'
```

### Check container status
```bash
ssh azureuser@$VM_IP 'docker ps -a'
```

### Restart services
```bash
ssh azureuser@$VM_IP 'sudo systemctl restart trustbridge'
```

## Cleanup

```bash
# Delete all resources
az group delete --name trustbridge-rg --yes --no-wait
```

## Portal Deployment (Managed App)

For Azure Marketplace deployment, the `createUiDefinition.json` provides a guided wizard:

1. **Basics**: VM name prefix, contract ID
2. **VM Settings**: GPU size, SSH credentials
3. **Network**: Public IP, SSH source restrictions
4. **Containers**: Image references, ACR name
5. **TrustBridge**: Asset ID, Control Plane endpoint, billing
6. **Summary**: Review and deploy

## Parameters Reference

| Parameter | Required | Default | Description |
|-----------|----------|---------|-------------|
| `namePrefix` | Yes | trustbridge | Resource naming prefix |
| `contractId` | Yes | - | TrustBridge contract ID |
| `assetId` | Yes | - | TrustBridge asset ID |
| `edcEndpoint` | Yes | - | Control Plane URL |
| `sentinelImage` | Yes | - | Sentinel container image |
| `runtimeImage` | Yes | - | Runtime container image |
| `vmSize` | No | Standard_NC24ads_A100_v4 | GPU VM size |
| `acrName` | No | "" | ACR name (if using private registry) |
| `adminSshKey` | Yes | - | SSH public key |
| `adminUsername` | No | azureuser | VM admin username |
| `enablePublicIp` | No | true | Enable public IP |
| `sshAllowedSourceAddresses` | No | * | SSH source IP filter |
| `billingEnabled` | No | true | Enable Marketplace billing |
| `logLevel` | No | info | Sentinel log level |
