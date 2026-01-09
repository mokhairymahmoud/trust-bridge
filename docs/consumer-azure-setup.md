# Consumer Azure Setup Guide

This guide walks you through setting up Azure resources required to deploy and run encrypted AI models from TrustBridge providers.

---

## Table of Contents

1. [Prerequisites](#prerequisites)
2. [Step 1: Create Resource Group](#step-1-create-resource-group)
3. [Step 2: Request GPU Quota](#step-2-request-gpu-quota)
4. [Step 3: Create Virtual Network](#step-3-create-virtual-network)
5. [Step 4: Create Network Security Group](#step-4-create-network-security-group)
6. [Step 5: Create GPU Virtual Machine](#step-5-create-gpu-virtual-machine)
7. [Step 6: Install Docker & NVIDIA Drivers](#step-6-install-docker--nvidia-drivers)
8. [Step 7: Configure Managed Identity](#step-7-configure-managed-identity)
9. [Step 8: Deploy TrustBridge Runtime](#step-8-deploy-trustbridge-runtime)
10. [Step 9: Verify Deployment](#step-9-verify-deployment)
11. [Cost Estimation](#cost-estimation)
12. [Cleanup](#cleanup)

---

## Prerequisites

Before starting, ensure you have:

- [ ] Azure account with an active subscription
- [ ] Azure CLI installed (`az --version` should return 2.50+)
- [ ] Logged into Azure CLI (`az login`)
- [ ] Sufficient permissions to create resources (Contributor role or higher)
- [ ] **Valid Contract ID** from the model provider
- [ ] **Asset ID** of the model you want to deploy

### Install Azure CLI (if needed)

```bash
# macOS
brew install azure-cli

# Linux (Ubuntu/Debian)
curl -sL https://aka.ms/InstallAzureCLIDeb | sudo bash

# Windows (PowerShell)
winget install Microsoft.AzureCLI
```

### Login to Azure

```bash
az login
az account show  # Verify correct subscription

# Set subscription if needed
az account set --subscription "<subscription-id>"
```

---

## Step 1: Create Resource Group

Create a dedicated resource group for your TrustBridge deployment.

```bash
# Set variables (customize these)
export TB_LOCATION="eastus"
export TB_RESOURCE_GROUP="trustbridge-consumer-rg"

# Create resource group
az group create \
  --name $TB_RESOURCE_GROUP \
  --location $TB_LOCATION

# Verify creation
az group show --name $TB_RESOURCE_GROUP --output table
```

**Expected output:**
```
Name                    Location    Status
----------------------  ----------  ---------
trustbridge-consumer-rg eastus      Succeeded
```

---

## Step 2: Request GPU Quota

GPU quotas are limited by default. Check your current quota and request increases if needed.

### Check Current GPU Quota

```bash
# List all VM usage in your region
az vm list-usage --location $TB_LOCATION --output table | grep -i "NC\|ND\|NV"

# Check specific GPU family
az vm list-usage --location $TB_LOCATION \
  --query "[?contains(name.localizedValue, 'NC')]" \
  --output table
```

### Recommended GPU VM Sizes

| VM Size | GPUs | GPU Memory | vCPUs | RAM | Use Case |
|---------|------|------------|-------|-----|----------|
| `Standard_NC4as_T4_v3` | 1x T4 | 16 GB | 4 | 28 GB | Small models, testing |
| `Standard_NC24ads_A100_v4` | 1x A100 | 80 GB | 24 | 220 GB | Large models |
| `Standard_NC48ads_A100_v4` | 2x A100 | 160 GB | 48 | 440 GB | Very large models |
| `Standard_ND96asr_v4` | 8x A100 | 640 GB | 96 | 900 GB | Massive models |

### Request Quota Increase

If your quota is insufficient:

1. Go to [Azure Portal](https://portal.azure.com)
2. Navigate to **Subscriptions** → Select your subscription
3. Click **Usage + quotas** in the left menu
4. Filter by **Compute** and search for your desired VM family
5. Click **Request increase**
6. Fill in justification and submit

**Note:** Quota increases may take 1-3 business days to process.

### Alternative: Check Available Regions

```bash
# Find regions with available GPU capacity
az vm list-skus --resource-type virtualMachines \
  --query "[?contains(name, 'NC24ads_A100')].{Name:name, Locations:locationInfo[0].location}" \
  --output table
```

---

## Step 3: Create Virtual Network

Create a virtual network for secure deployment.

```bash
# Set network variables
export TB_VNET_NAME="trustbridge-vnet"
export TB_SUBNET_NAME="trustbridge-subnet"

# Create virtual network
az network vnet create \
  --name $TB_VNET_NAME \
  --resource-group $TB_RESOURCE_GROUP \
  --location $TB_LOCATION \
  --address-prefix 10.0.0.0/16 \
  --subnet-name $TB_SUBNET_NAME \
  --subnet-prefix 10.0.1.0/24

# Verify
az network vnet show \
  --name $TB_VNET_NAME \
  --resource-group $TB_RESOURCE_GROUP \
  --output table
```

---

## Step 4: Create Network Security Group

Create and configure NSG rules to secure the deployment.

```bash
# Set NSG name
export TB_NSG_NAME="trustbridge-nsg"

# Create NSG
az network nsg create \
  --name $TB_NSG_NAME \
  --resource-group $TB_RESOURCE_GROUP \
  --location $TB_LOCATION

# Rule 1: Allow SSH (for management)
az network nsg rule create \
  --nsg-name $TB_NSG_NAME \
  --resource-group $TB_RESOURCE_GROUP \
  --name AllowSSH \
  --priority 100 \
  --access Allow \
  --direction Inbound \
  --protocol Tcp \
  --source-address-prefixes "*" \
  --source-port-ranges "*" \
  --destination-address-prefixes "*" \
  --destination-port-ranges 22

# Rule 2: Allow Sentinel API (port 8000)
az network nsg rule create \
  --nsg-name $TB_NSG_NAME \
  --resource-group $TB_RESOURCE_GROUP \
  --name AllowSentinelAPI \
  --priority 110 \
  --access Allow \
  --direction Inbound \
  --protocol Tcp \
  --source-address-prefixes "*" \
  --source-port-ranges "*" \
  --destination-address-prefixes "*" \
  --destination-port-ranges 8000

# Rule 3: BLOCK Runtime port (port 8081) - CRITICAL SECURITY RULE
az network nsg rule create \
  --nsg-name $TB_NSG_NAME \
  --resource-group $TB_RESOURCE_GROUP \
  --name DenyRuntimeExternal \
  --priority 200 \
  --access Deny \
  --direction Inbound \
  --protocol Tcp \
  --source-address-prefixes "*" \
  --source-port-ranges "*" \
  --destination-address-prefixes "*" \
  --destination-port-ranges 8081

# Associate NSG with subnet
az network vnet subnet update \
  --vnet-name $TB_VNET_NAME \
  --name $TB_SUBNET_NAME \
  --resource-group $TB_RESOURCE_GROUP \
  --network-security-group $TB_NSG_NAME

# Verify NSG rules
az network nsg rule list \
  --nsg-name $TB_NSG_NAME \
  --resource-group $TB_RESOURCE_GROUP \
  --output table
```

**Expected output:**
```
Name                  Priority  Access  Direction
--------------------  --------  ------  ---------
AllowSSH              100       Allow   Inbound
AllowSentinelAPI      110       Allow   Inbound
DenyRuntimeExternal   200       Deny    Inbound
```

### Restrict SSH Access (Recommended for Production)

```bash
# Get your current public IP
MY_IP=$(curl -s ifconfig.me)

# Update SSH rule to only allow your IP
az network nsg rule update \
  --nsg-name $TB_NSG_NAME \
  --resource-group $TB_RESOURCE_GROUP \
  --name AllowSSH \
  --source-address-prefixes $MY_IP/32

echo "SSH restricted to: $MY_IP"
```

---

## Step 5: Create GPU Virtual Machine

Create a GPU-enabled VM to run the TrustBridge runtime.

### Option A: Using T4 GPU (Cost-Effective for Testing)

```bash
# Set VM variables
export TB_VM_NAME="trustbridge-gpu-vm"
export TB_VM_SIZE="Standard_NC4as_T4_v3"
export TB_ADMIN_USER="azureuser"

# Create public IP
az network public-ip create \
  --name "${TB_VM_NAME}-ip" \
  --resource-group $TB_RESOURCE_GROUP \
  --location $TB_LOCATION \
  --allocation-method Static \
  --sku Standard

# Create NIC
az network nic create \
  --name "${TB_VM_NAME}-nic" \
  --resource-group $TB_RESOURCE_GROUP \
  --location $TB_LOCATION \
  --vnet-name $TB_VNET_NAME \
  --subnet $TB_SUBNET_NAME \
  --network-security-group $TB_NSG_NAME \
  --public-ip-address "${TB_VM_NAME}-ip"

# Create VM
az vm create \
  --name $TB_VM_NAME \
  --resource-group $TB_RESOURCE_GROUP \
  --location $TB_LOCATION \
  --nics "${TB_VM_NAME}-nic" \
  --size $TB_VM_SIZE \
  --image "Canonical:0001-com-ubuntu-server-jammy:22_04-lts-gen2:latest" \
  --admin-username $TB_ADMIN_USER \
  --generate-ssh-keys \
  --os-disk-size-gb 256 \
  --storage-sku Premium_LRS

# Get public IP
export TB_VM_IP=$(az network public-ip show \
  --name "${TB_VM_NAME}-ip" \
  --resource-group $TB_RESOURCE_GROUP \
  --query ipAddress -o tsv)

echo "VM created successfully!"
echo "Public IP: $TB_VM_IP"
echo "SSH: ssh $TB_ADMIN_USER@$TB_VM_IP"
```

### Option B: Using A100 GPU (Production Workloads)

```bash
export TB_VM_SIZE="Standard_NC24ads_A100_v4"

# Follow the same steps as Option A with this VM size
```

### Add Data Disk for Model Storage

```bash
# Create and attach a data disk for encrypted model storage
az vm disk attach \
  --vm-name $TB_VM_NAME \
  --resource-group $TB_RESOURCE_GROUP \
  --name "${TB_VM_NAME}-data" \
  --size-gb 512 \
  --sku Premium_LRS \
  --new

echo "Data disk attached. Format and mount it on the VM."
```

---

## Step 6: Install Docker & NVIDIA Drivers

SSH into the VM and install required software.

```bash
# SSH into the VM
ssh $TB_ADMIN_USER@$TB_VM_IP
```

Once connected, run the following commands:

### Install NVIDIA Drivers

```bash
# Update system
sudo apt-get update && sudo apt-get upgrade -y

# Install NVIDIA drivers
sudo apt-get install -y linux-headers-$(uname -r)
distribution=$(. /etc/os-release;echo $ID$VERSION_ID)
curl -s -L https://nvidia.github.io/nvidia-docker/gpgkey | sudo apt-key add -
curl -s -L https://nvidia.github.io/nvidia-docker/$distribution/nvidia-docker.list | sudo tee /etc/apt/sources.list.d/nvidia-docker.list

sudo apt-get update
sudo apt-get install -y nvidia-driver-535

# Reboot to load drivers
sudo reboot
```

After reboot, reconnect and verify:

```bash
# Verify NVIDIA driver
nvidia-smi
```

**Expected output:**
```
+-----------------------------------------------------------------------------+
| NVIDIA-SMI 535.xx.xx    Driver Version: 535.xx.xx    CUDA Version: 12.x     |
|-------------------------------+----------------------+----------------------+
| GPU  Name        Persistence-M| Bus-Id        Disp.A | Volatile Uncorr. ECC |
| Fan  Temp  Perf  Pwr:Usage/Cap|         Memory-Usage | GPU-Util  Compute M. |
|===============================+======================+======================|
|   0  Tesla T4            Off  | 00000000:00:1E.0 Off |                    0 |
| N/A   30C    P8     9W /  70W |      0MiB / 15360MiB |      0%      Default |
+-------------------------------+----------------------+----------------------+
```

### Install Docker

```bash
# Install Docker
curl -fsSL https://get.docker.com -o get-docker.sh
sudo sh get-docker.sh

# Add user to docker group
sudo usermod -aG docker $USER

# Install NVIDIA Container Toolkit
distribution=$(. /etc/os-release;echo $ID$VERSION_ID) \
  && curl -s -L https://nvidia.github.io/nvidia-docker/gpgkey | sudo apt-key add - \
  && curl -s -L https://nvidia.github.io/nvidia-docker/$distribution/nvidia-docker.list | sudo tee /etc/apt/sources.list.d/nvidia-docker.list

sudo apt-get update
sudo apt-get install -y nvidia-container-toolkit

# Restart Docker
sudo systemctl restart docker

# Logout and login to apply docker group
exit
```

Reconnect and verify:

```bash
# Test Docker with GPU
docker run --rm --gpus all nvidia/cuda:12.0-base-ubuntu22.04 nvidia-smi
```

### Format and Mount Data Disk

```bash
# Find the data disk (usually /dev/sdc)
lsblk

# Format the disk
sudo mkfs.ext4 /dev/sdc

# Create mount point
sudo mkdir -p /mnt/resource/trustbridge

# Mount the disk
sudo mount /dev/sdc /mnt/resource/trustbridge

# Add to fstab for persistence
echo '/dev/sdc /mnt/resource/trustbridge ext4 defaults 0 2' | sudo tee -a /etc/fstab

# Set permissions
sudo chmod 755 /mnt/resource/trustbridge

# Verify
df -h /mnt/resource/trustbridge
```

---

## Step 7: Configure Managed Identity

Set up managed identity for secure authentication.

### Enable System-Assigned Managed Identity

```bash
# From your local machine (exit SSH first)
az vm identity assign \
  --name $TB_VM_NAME \
  --resource-group $TB_RESOURCE_GROUP

# Get the principal ID
export TB_IDENTITY_ID=$(az vm identity show \
  --name $TB_VM_NAME \
  --resource-group $TB_RESOURCE_GROUP \
  --query principalId -o tsv)

echo "Managed Identity Principal ID: $TB_IDENTITY_ID"
```

### Grant Storage Access (If Provider Uses Azure Storage)

```bash
# If you need to access provider's storage account
# Provider must grant this access
az role assignment create \
  --assignee $TB_IDENTITY_ID \
  --role "Storage Blob Data Reader" \
  --scope "/subscriptions/<provider-subscription>/resourceGroups/<provider-rg>/providers/Microsoft.Storage/storageAccounts/<provider-storage>"
```

---

## Step 8: Deploy TrustBridge Runtime

SSH back into the VM to deploy the runtime.

```bash
ssh $TB_ADMIN_USER@$TB_VM_IP
```

### Set Environment Variables

```bash
# TrustBridge configuration - GET THESE FROM YOUR PROVIDER
export TB_CONTRACT_ID="your-contract-id"      # From provider
export TB_ASSET_ID="model-asset-id"           # From provider
export TB_EDC_ENDPOINT="https://controlplane.example.com"  # From provider

# Local configuration
export TB_TARGET_DIR="/mnt/resource/trustbridge"
export TB_PIPE_PATH="/dev/shm/model-pipe"
export TB_READY_SIGNAL="/dev/shm/weights/ready.signal"
export TB_RUNTIME_URL="http://127.0.0.1:8081"
export TB_PUBLIC_ADDR="0.0.0.0:8000"
```

### Create Docker Compose File

```bash
mkdir -p ~/trustbridge && cd ~/trustbridge

cat > docker-compose.yml <<'EOF'
version: '3.8'

services:
  sentinel:
    image: ${TB_IMAGE:-provider.azurecr.io/trustbridge-runtime:latest}
    container_name: sentinel
    environment:
      - TB_CONTRACT_ID=${TB_CONTRACT_ID}
      - TB_ASSET_ID=${TB_ASSET_ID}
      - TB_EDC_ENDPOINT=${TB_EDC_ENDPOINT}
      - TB_TARGET_DIR=/mnt/resource/trustbridge
      - TB_PIPE_PATH=/dev/shm/model-pipe
      - TB_READY_SIGNAL=/dev/shm/weights/ready.signal
      - TB_RUNTIME_URL=http://127.0.0.1:8081
      - TB_PUBLIC_ADDR=0.0.0.0:8000
      - TB_LOG_LEVEL=info
    ports:
      - "8000:8000"
    volumes:
      - /mnt/resource/trustbridge:/mnt/resource/trustbridge
      - /dev/shm:/dev/shm
    deploy:
      resources:
        reservations:
          devices:
            - driver: nvidia
              count: all
              capabilities: [gpu]
    restart: unless-stopped
    healthcheck:
      test: ["CMD", "curl", "-f", "http://localhost:8000/health"]
      interval: 30s
      timeout: 10s
      retries: 3
      start_period: 300s
EOF
```

### Login to Provider's Container Registry

```bash
# The provider will give you credentials or access
docker login provider.azurecr.io -u <username> -p <password>

# Or using Azure CLI if you have access
az acr login --name provideracr
```

### Start the Runtime

```bash
# Pull the image
docker compose pull

# Start the runtime
docker compose up -d

# Check logs
docker compose logs -f sentinel
```

---

## Step 9: Verify Deployment

### Check Sentinel Status

```bash
# From the VM
curl http://localhost:8000/health
curl http://localhost:8000/status
```

### From External Machine

```bash
# Check health
curl http://$TB_VM_IP:8000/health

# Get status
curl http://$TB_VM_IP:8000/status

# Expected status when ready:
# {"state":"Ready","asset_id":"...","contract_id":"...","uptime_seconds":...}
```

### Monitor Startup Progress

```bash
# Watch logs during startup
docker compose logs -f sentinel

# Expected sequence:
# [INFO] State: Boot -> Authorize
# [INFO] State: Authorize -> Hydrate  
# [INFO] Downloading encrypted model...
# [INFO] State: Hydrate -> Decrypt
# [INFO] Decrypting to FIFO...
# [INFO] State: Decrypt -> Ready
# [INFO] Proxy accepting requests on :8000
```

### Test Inference (When Ready)

```bash
# Send a test request (adjust based on model API)
curl -X POST http://$TB_VM_IP:8000/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{
    "model": "'"$TB_ASSET_ID"'",
    "messages": [
      {"role": "user", "content": "Hello, how are you?"}
    ],
    "max_tokens": 100
  }'
```

### Verify Security

```bash
# Ensure runtime port is NOT accessible externally
curl http://$TB_VM_IP:8081/health
# Should timeout or be refused

# Check no plaintext on disk
find /mnt/resource/trustbridge -type f | head -20
# Should only show .tbenc and .json files
```

---

## Cost Estimation

Estimated monthly costs (varies by region and usage):

### Testing Configuration (T4 GPU)

| Resource | SKU | Estimated Cost |
|----------|-----|----------------|
| VM | NC4as_T4_v3 | ~$350/month (continuous) |
| OS Disk | 256 GB Premium SSD | ~$38/month |
| Data Disk | 512 GB Premium SSD | ~$75/month |
| Public IP | Static Standard | ~$4/month |
| Bandwidth | ~100 GB egress | ~$9/month |
| **Total** | | **~$480/month** |

### Production Configuration (A100 GPU)

| Resource | SKU | Estimated Cost |
|----------|-----|----------------|
| VM | NC24ads_A100_v4 | ~$3,500/month (continuous) |
| OS Disk | 256 GB Premium SSD | ~$38/month |
| Data Disk | 1 TB Premium SSD | ~$135/month |
| Public IP | Static Standard | ~$4/month |
| Bandwidth | ~500 GB egress | ~$44/month |
| **Total** | | **~$3,720/month** |

### Cost Optimization Tips

1. **Stop VM when not in use**
   ```bash
   az vm deallocate --name $TB_VM_NAME --resource-group $TB_RESOURCE_GROUP
   ```

2. **Use Spot instances for testing** (up to 90% discount)
   ```bash
   az vm create ... --priority Spot --eviction-policy Deallocate --max-price 0.5
   ```

3. **Reserved instances** for production (up to 72% savings on 3-year commitment)

---

## Cleanup

To remove all resources when done testing:

### Stop Services First

```bash
# SSH into VM
ssh $TB_ADMIN_USER@$TB_VM_IP

# Stop Docker services
cd ~/trustbridge
docker compose down

# Exit VM
exit
```

### Delete Resources

```bash
# Delete entire resource group (removes ALL resources)
az group delete \
  --name $TB_RESOURCE_GROUP \
  --yes \
  --no-wait

echo "Resource group deletion initiated. This may take a few minutes."
```

**Warning:** This permanently deletes:
- Virtual Machine and all disks
- Virtual Network and NSG
- Public IP address
- All downloaded/cached data

### Alternative: Stop VM to Save Costs

```bash
# Deallocate VM (stops billing for compute, keeps disks)
az vm deallocate \
  --name $TB_VM_NAME \
  --resource-group $TB_RESOURCE_GROUP

# Start VM later
az vm start \
  --name $TB_VM_NAME \
  --resource-group $TB_RESOURCE_GROUP
```

---

## Troubleshooting

### Sentinel Stuck in "Authorize" State

```bash
# Check logs
docker compose logs sentinel | grep -i "authorize\|error"

# Verify environment variables
docker compose exec sentinel env | grep TB_

# Verify network access to control plane
curl -I $TB_EDC_ENDPOINT/api/v1/health
```

**Common causes:**
- Invalid contract ID
- Network firewall blocking outbound
- Control plane unreachable

### Download/Hydrate Failures

```bash
# Check disk space
df -h /mnt/resource/trustbridge

# Check network connectivity
curl -I https://storage.blob.core.windows.net

# Check logs for specific errors
docker compose logs sentinel | grep -i "download\|hydrate\|error"
```

### GPU Not Detected

```bash
# Verify NVIDIA driver
nvidia-smi

# Check Docker GPU access
docker run --rm --gpus all nvidia/cuda:12.0-base-ubuntu22.04 nvidia-smi

# Reinstall NVIDIA Container Toolkit if needed
sudo apt-get install --reinstall nvidia-container-toolkit
sudo systemctl restart docker
```

### Cannot Connect to Sentinel API

```bash
# From the VM - check if sentinel is running
docker compose ps
curl http://localhost:8000/health

# Check NSG rules
az network nsg rule list \
  --nsg-name $TB_NSG_NAME \
  --resource-group $TB_RESOURCE_GROUP \
  --output table

# Verify public IP
az network public-ip show \
  --name "${TB_VM_NAME}-ip" \
  --resource-group $TB_RESOURCE_GROUP \
  --query ipAddress -o tsv
```

### High Latency / Slow Performance

```bash
# Check GPU utilization
nvidia-smi -l 1

# Check system resources
htop

# Verify model is loaded in GPU memory
docker compose exec sentinel nvidia-smi
```

---

## Quick Reference

### Essential Commands

```bash
# SSH into VM
ssh azureuser@<vm-ip>

# Check sentinel health
curl http://localhost:8000/health

# View logs
docker compose logs -f sentinel

# Restart sentinel
docker compose restart sentinel

# Stop all services
docker compose down

# Start all services
docker compose up -d
```

### Environment Variables Summary

```bash
# Required (from provider)
TB_CONTRACT_ID="contract-xxx"
TB_ASSET_ID="model-xxx"
TB_EDC_ENDPOINT="https://controlplane.example.com"

# Optional (defaults shown)
TB_TARGET_DIR="/mnt/resource/trustbridge"
TB_PIPE_PATH="/dev/shm/model-pipe"
TB_RUNTIME_URL="http://127.0.0.1:8081"
TB_PUBLIC_ADDR="0.0.0.0:8000"
TB_LOG_LEVEL="info"
```

---

## Next Steps

1. **Monitor Usage** - Set up Azure Monitor for alerts and metrics
2. **Scale Up** - Consider AKS for multi-node deployments
3. **Integrate** - Connect your application to the inference API
4. **Secure** - Enable Private Link for enhanced security

See [workflow.md](workflow.md) for the complete consumer workflow.

---

*Last updated: January 2026*
