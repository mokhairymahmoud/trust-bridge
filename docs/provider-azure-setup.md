# Provider Azure Setup Guide

This guide walks you through setting up Azure resources required to publish and distribute encrypted AI models using TrustBridge.

---

## Table of Contents

1. [Prerequisites](#prerequisites)
2. [Step 1: Create Resource Group](#step-1-create-resource-group)
3. [Step 2: Create Storage Account](#step-2-create-storage-account)
4. [Step 3: Create Blob Container](#step-3-create-blob-container)
5. [Step 4: Create Azure Container Registry](#step-4-create-azure-container-registry)
6. [Step 5: Create Key Vault (Optional)](#step-5-create-key-vault-optional)
7. [Step 6: Configure Access & Permissions](#step-6-configure-access--permissions)
8. [Step 7: Verify Setup](#step-7-verify-setup)
9. [Cost Estimation](#cost-estimation)
10. [Cleanup](#cleanup)

---

## Prerequisites

Before starting, ensure you have:

- [ ] Azure account with an active subscription
- [ ] Azure CLI installed (`az --version` should return 2.50+)
- [ ] Logged into Azure CLI (`az login`)
- [ ] Sufficient permissions to create resources (Contributor role or higher)

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
```

---

## Step 1: Create Resource Group

Create a dedicated resource group for TrustBridge provider resources.

```bash
# Set variables (customize these)
export TB_LOCATION="eastus"
export TB_RESOURCE_GROUP="trustbridge-provider-rg"

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
trustbridge-provider-rg eastus      Succeeded
```

---

## Step 2: Create Storage Account

Create a storage account to host encrypted model files.

```bash
# Set storage account name (must be globally unique, 3-24 lowercase chars/numbers)
export TB_STORAGE_ACCOUNT="tbprovider$(openssl rand -hex 4)"

# Create storage account
az storage account create \
  --name $TB_STORAGE_ACCOUNT \
  --resource-group $TB_RESOURCE_GROUP \
  --location $TB_LOCATION \
  --sku Standard_LRS \
  --kind StorageV2 \
  --access-tier Hot \
  --allow-blob-public-access false \
  --min-tls-version TLS1_2

# Get storage account key (save this securely)
export TB_STORAGE_KEY=$(az storage account keys list \
  --account-name $TB_STORAGE_ACCOUNT \
  --resource-group $TB_RESOURCE_GROUP \
  --query '[0].value' -o tsv)

# Verify
az storage account show \
  --name $TB_STORAGE_ACCOUNT \
  --resource-group $TB_RESOURCE_GROUP \
  --output table
```

**Expected output:**
```
Name              ResourceGroup            Location    Kind       Sku
----------------  -----------------------  ----------  ---------  ------------
tbproviderXXXX    trustbridge-provider-rg  eastus      StorageV2  Standard_LRS
```

### Save Storage Connection String

```bash
# Get connection string for CLI usage
export TB_STORAGE_CONNECTION=$(az storage account show-connection-string \
  --name $TB_STORAGE_ACCOUNT \
  --resource-group $TB_RESOURCE_GROUP \
  --query connectionString -o tsv)

echo "Storage Account: $TB_STORAGE_ACCOUNT"
echo "Save these values securely!"
```

---

## Step 3: Create Blob Container

Create a container to store encrypted model artifacts.

```bash
# Set container name
export TB_CONTAINER="models"

# Create container
az storage container create \
  --name $TB_CONTAINER \
  --account-name $TB_STORAGE_ACCOUNT \
  --account-key $TB_STORAGE_KEY \
  --public-access off

# Verify
az storage container list \
  --account-name $TB_STORAGE_ACCOUNT \
  --account-key $TB_STORAGE_KEY \
  --output table
```

**Expected output:**
```
Name    Lease Status    Last Modified
------  --------------  -------------------------
models  unlocked        2026-01-09T10:00:00+00:00
```

### Test Upload (Optional)

```bash
# Create test file
echo "test content" > /tmp/test.txt

# Upload test file
az storage blob upload \
  --account-name $TB_STORAGE_ACCOUNT \
  --account-key $TB_STORAGE_KEY \
  --container-name $TB_CONTAINER \
  --name "test/test.txt" \
  --file /tmp/test.txt

# List blobs
az storage blob list \
  --account-name $TB_STORAGE_ACCOUNT \
  --account-key $TB_STORAGE_KEY \
  --container-name $TB_CONTAINER \
  --output table

# Delete test file
az storage blob delete \
  --account-name $TB_STORAGE_ACCOUNT \
  --account-key $TB_STORAGE_KEY \
  --container-name $TB_CONTAINER \
  --name "test/test.txt"
```

---

## Step 4: Create Azure Container Registry

Create a container registry to store Docker images.

```bash
# Set registry name (must be globally unique, 5-50 alphanumeric chars)
export TB_ACR_NAME="tbprovideracr$(openssl rand -hex 4)"

# Create registry (Basic tier for testing, Standard/Premium for production)
az acr create \
  --name $TB_ACR_NAME \
  --resource-group $TB_RESOURCE_GROUP \
  --location $TB_LOCATION \
  --sku Basic \
  --admin-enabled true

# Get login credentials
export TB_ACR_USERNAME=$(az acr credential show \
  --name $TB_ACR_NAME \
  --query "username" -o tsv)

export TB_ACR_PASSWORD=$(az acr credential show \
  --name $TB_ACR_NAME \
  --query "passwords[0].value" -o tsv)

# Get login server
export TB_ACR_SERVER=$(az acr show \
  --name $TB_ACR_NAME \
  --query "loginServer" -o tsv)

# Verify
az acr show --name $TB_ACR_NAME --output table
```

**Expected output:**
```
NAME               RESOURCE GROUP           LOCATION    SKU    LOGIN SERVER
-----------------  -----------------------  ----------  -----  ---------------------------
tbprovideracrXXXX  trustbridge-provider-rg  eastus      Basic  tbprovideracrxxxx.azurecr.io
```

### Login to Registry

```bash
# Docker login to ACR
az acr login --name $TB_ACR_NAME

# Or using Docker directly
docker login $TB_ACR_SERVER -u $TB_ACR_USERNAME -p $TB_ACR_PASSWORD
```

### Test Image Push (Optional)

```bash
# Pull a small test image
docker pull hello-world

# Tag for ACR
docker tag hello-world $TB_ACR_SERVER/test/hello-world:latest

# Push to ACR
docker push $TB_ACR_SERVER/test/hello-world:latest

# List repositories
az acr repository list --name $TB_ACR_NAME --output table

# Delete test image
az acr repository delete --name $TB_ACR_NAME --repository test/hello-world --yes
```

---

## Step 5: Create Key Vault (Optional)

Create a Key Vault to securely store decryption keys and secrets.

```bash
# Set vault name (must be globally unique, 3-24 chars)
export TB_KEYVAULT_NAME="tb-provider-kv-$(openssl rand -hex 4)"

# Create Key Vault
az keyvault create \
  --name $TB_KEYVAULT_NAME \
  --resource-group $TB_RESOURCE_GROUP \
  --location $TB_LOCATION \
  --sku standard \
  --enable-rbac-authorization false

# Verify
az keyvault show --name $TB_KEYVAULT_NAME --output table
```

### Store a Test Secret

```bash
# Store a test secret
az keyvault secret set \
  --vault-name $TB_KEYVAULT_NAME \
  --name "test-secret" \
  --value "test-value"

# Retrieve secret
az keyvault secret show \
  --vault-name $TB_KEYVAULT_NAME \
  --name "test-secret" \
  --query "value" -o tsv

# Delete test secret
az keyvault secret delete \
  --vault-name $TB_KEYVAULT_NAME \
  --name "test-secret"
```

---

## Step 6: Configure Access & Permissions

### Option A: Service Principal (Recommended for CI/CD)

Create a service principal for automated deployments.

```bash
# Create service principal with Contributor role
export TB_SP=$(az ad sp create-for-rbac \
  --name "trustbridge-provider-sp" \
  --role Contributor \
  --scopes /subscriptions/$(az account show --query id -o tsv)/resourceGroups/$TB_RESOURCE_GROUP \
  --query "{clientId:appId, clientSecret:password, tenantId:tenant}" -o json)

echo "$TB_SP" | jq .

# Save output securely - you'll need:
# - appId (client ID)
# - password (client secret)
# - tenant (tenant ID)
```

### Option B: Managed Identity (For Azure VMs/Services)

If running from an Azure VM or service:

```bash
# Enable system-assigned managed identity (example for VM)
az vm identity assign \
  --name <vm-name> \
  --resource-group $TB_RESOURCE_GROUP

# Assign Storage Blob Data Contributor role
az role assignment create \
  --assignee-object-id <managed-identity-object-id> \
  --role "Storage Blob Data Contributor" \
  --scope /subscriptions/$(az account show --query id -o tsv)/resourceGroups/$TB_RESOURCE_GROUP/providers/Microsoft.Storage/storageAccounts/$TB_STORAGE_ACCOUNT
```

### Grant ACR Push Permissions

```bash
# Get ACR resource ID
export TB_ACR_ID=$(az acr show --name $TB_ACR_NAME --query id -o tsv)

# Grant AcrPush role to service principal
az role assignment create \
  --assignee <service-principal-client-id> \
  --role AcrPush \
  --scope $TB_ACR_ID
```

---

## Step 7: Verify Setup

Run these commands to verify all resources are correctly configured.

```bash
echo "=== TrustBridge Provider Setup Summary ==="
echo ""
echo "Resource Group:     $TB_RESOURCE_GROUP"
echo "Location:           $TB_LOCATION"
echo ""
echo "Storage Account:    $TB_STORAGE_ACCOUNT"
echo "Container:          $TB_CONTAINER"
echo ""
echo "Container Registry: $TB_ACR_NAME"
echo "Login Server:       $TB_ACR_SERVER"
echo ""
echo "Key Vault:          ${TB_KEYVAULT_NAME:-'Not configured'}"
echo ""

# Verify all resources exist
echo "=== Verifying Resources ==="
az resource list \
  --resource-group $TB_RESOURCE_GROUP \
  --output table
```

### Save Configuration

Create a configuration file for future use:

```bash
cat > ~/.trustbridge-provider-config <<EOF
# TrustBridge Provider Configuration
# Generated: $(date)

export TB_LOCATION="$TB_LOCATION"
export TB_RESOURCE_GROUP="$TB_RESOURCE_GROUP"
export TB_STORAGE_ACCOUNT="$TB_STORAGE_ACCOUNT"
export TB_CONTAINER="$TB_CONTAINER"
export TB_ACR_NAME="$TB_ACR_NAME"
export TB_ACR_SERVER="$TB_ACR_SERVER"
export TB_KEYVAULT_NAME="${TB_KEYVAULT_NAME:-}"
EOF

chmod 600 ~/.trustbridge-provider-config
echo "Configuration saved to ~/.trustbridge-provider-config"
echo "Source it with: source ~/.trustbridge-provider-config"
```

---

## Cost Estimation

Estimated monthly costs for testing (minimal usage):

| Resource | SKU | Estimated Cost |
|----------|-----|----------------|
| Storage Account | Standard LRS | ~$0.02/GB/month |
| Container Registry | Basic | ~$5/month |
| Key Vault | Standard | ~$0.03/10K operations |
| **Total (minimal)** | | **~$10-15/month** |

**Production considerations:**
- Storage: Premium or GRS for redundancy (+$0.05-0.10/GB)
- ACR: Standard or Premium for geo-replication (+$20-50/month)
- Bandwidth: Egress charges apply for downloads (~$0.087/GB)

---

## Cleanup

To remove all resources when done testing:

```bash
# Delete entire resource group (removes all resources)
az group delete \
  --name $TB_RESOURCE_GROUP \
  --yes \
  --no-wait

echo "Resource group deletion initiated. This may take a few minutes."
```

**Warning:** This permanently deletes all resources including:
- Storage account and all blobs
- Container registry and all images
- Key Vault and all secrets

---

## Next Steps

After completing Azure setup:

1. **Install TrustBridge CLI**
   ```bash
   cd src/cli
   pip install -r requirements.txt
   ```

2. **Encrypt your model**
   ```bash
   trustbridge encrypt \
     --asset-id "my-model-v1" \
     --in /path/to/model.safetensors \
     --out model.tbenc \
     --manifest model.manifest.json
   ```

3. **Upload to Azure**
   ```bash
   trustbridge upload \
     --storage-account "$TB_STORAGE_ACCOUNT" \
     --container "$TB_CONTAINER" \
     --asset-id "my-model-v1" \
     --encrypted-file model.tbenc \
     --manifest model.manifest.json
   ```

4. See [workflow.md](workflow.md) for the complete provider workflow.

---

## Troubleshooting

### "Storage account name already exists"

Storage account names must be globally unique. Try a different name:
```bash
export TB_STORAGE_ACCOUNT="tbprovider$(date +%s)"
```

### "Insufficient permissions"

Ensure your account has the Contributor role:
```bash
az role assignment list --assignee $(az ad signed-in-user show --query id -o tsv) --output table
```

### "ACR login failed"

Ensure admin access is enabled:
```bash
az acr update --name $TB_ACR_NAME --admin-enabled true
```

### "Quota exceeded"

Check and request quota increases:
```bash
az vm list-usage --location $TB_LOCATION --output table
```

---

*Last updated: January 2026*
