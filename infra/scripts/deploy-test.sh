#!/bin/bash
# TrustBridge Local Deployment Test
# This script deploys the infrastructure to Azure and runs validation tests.
#
# Prerequisites:
#   - Azure CLI installed and logged in
#   - SSH key pair available
#   - Container images pushed to ACR
#
# Usage:
#   ./deploy-test.sh [RESOURCE_GROUP] [LOCATION]
#
# Example:
#   ./deploy-test.sh trustbridge-test-rg eastus

set -euo pipefail

# Default values
RESOURCE_GROUP="${1:-trustbridge-test-rg}"
LOCATION="${2:-eastus}"
DEPLOYMENT_NAME="trustbridge-$(date +%Y%m%d-%H%M%S)"

# Script directory
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
INFRA_DIR="$(dirname "$SCRIPT_DIR")"

echo "========================================"
echo "TrustBridge Deployment Test"
echo "========================================"
echo ""
echo "Resource Group: $RESOURCE_GROUP"
echo "Location: $LOCATION"
echo "Deployment: $DEPLOYMENT_NAME"
echo ""

# Check Azure CLI login
echo "Checking Azure CLI login..."
if ! az account show > /dev/null 2>&1; then
    echo "Error: Not logged in to Azure CLI. Run 'az login' first."
    exit 1
fi

SUBSCRIPTION=$(az account show --query name -o tsv)
echo "Using subscription: $SUBSCRIPTION"
echo ""

# Create resource group if it doesn't exist
echo "Creating resource group..."
az group create --name "$RESOURCE_GROUP" --location "$LOCATION" --output none

# Generate SSH key if needed
SSH_KEY_PATH="$HOME/.ssh/trustbridge_test_key"
if [ ! -f "$SSH_KEY_PATH" ]; then
    echo "Generating SSH key for test..."
    ssh-keygen -t rsa -b 4096 -f "$SSH_KEY_PATH" -N "" -q
fi
SSH_PUBLIC_KEY=$(cat "${SSH_KEY_PATH}.pub")

# Prompt for required parameters
echo ""
echo "Enter deployment parameters (or press Enter for defaults):"
read -p "Contract ID [contract-test]: " CONTRACT_ID
CONTRACT_ID="${CONTRACT_ID:-contract-test}"

read -p "Asset ID [tb-asset-test-001]: " ASSET_ID
ASSET_ID="${ASSET_ID:-tb-asset-test-001}"

read -p "EDC Endpoint [https://controlplane.trustbridge.io]: " EDC_ENDPOINT
EDC_ENDPOINT="${EDC_ENDPOINT:-https://controlplane.trustbridge.io}"

read -p "ACR Name (leave empty for public images): " ACR_NAME

read -p "Sentinel Image [mcr.microsoft.com/hello-world]: " SENTINEL_IMAGE
SENTINEL_IMAGE="${SENTINEL_IMAGE:-mcr.microsoft.com/hello-world}"

read -p "Runtime Image [mcr.microsoft.com/hello-world]: " RUNTIME_IMAGE
RUNTIME_IMAGE="${RUNTIME_IMAGE:-mcr.microsoft.com/hello-world}"

read -p "VM Size [Standard_NC6s_v3]: " VM_SIZE
VM_SIZE="${VM_SIZE:-Standard_NC6s_v3}"

echo ""
echo "Deploying TrustBridge infrastructure..."
echo ""

# Deploy using Bicep
az deployment group create \
    --name "$DEPLOYMENT_NAME" \
    --resource-group "$RESOURCE_GROUP" \
    --template-file "$INFRA_DIR/main.bicep" \
    --parameters \
        namePrefix="tb-test" \
        contractId="$CONTRACT_ID" \
        assetId="$ASSET_ID" \
        edcEndpoint="$EDC_ENDPOINT" \
        sentinelImage="$SENTINEL_IMAGE" \
        runtimeImage="$RUNTIME_IMAGE" \
        acrName="$ACR_NAME" \
        vmSize="$VM_SIZE" \
        adminSshKey="$SSH_PUBLIC_KEY" \
        enablePublicIp=true \
        logLevel="debug" \
    --output none

echo "Deployment completed!"
echo ""

# Get outputs
VM_IP=$(az deployment group show \
    --name "$DEPLOYMENT_NAME" \
    --resource-group "$RESOURCE_GROUP" \
    --query "properties.outputs.vmPublicIp.value" -o tsv)

VM_FQDN=$(az deployment group show \
    --name "$DEPLOYMENT_NAME" \
    --resource-group "$RESOURCE_GROUP" \
    --query "properties.outputs.vmFqdn.value" -o tsv)

echo "========================================"
echo "Deployment Outputs"
echo "========================================"
echo "VM Public IP: $VM_IP"
echo "VM FQDN: $VM_FQDN"
echo "SSH: ssh -i $SSH_KEY_PATH azureuser@$VM_IP"
echo ""

# Wait for VM to be ready
echo "Waiting for VM to be ready (60 seconds)..."
sleep 60

# Run validation
echo ""
echo "Running validation..."
"$SCRIPT_DIR/validate-deployment.sh" "$VM_IP" "azureuser" "$SSH_KEY_PATH"

echo ""
echo "========================================"
echo "Cleanup"
echo "========================================"
echo ""
echo "To delete resources, run:"
echo "  az group delete --name $RESOURCE_GROUP --yes --no-wait"
echo ""
echo "To connect to the VM:"
echo "  ssh -i $SSH_KEY_PATH azureuser@$VM_IP"
echo ""
echo "To view logs:"
echo "  ssh -i $SSH_KEY_PATH azureuser@$VM_IP 'sudo cat /var/log/trustbridge-init.log'"
