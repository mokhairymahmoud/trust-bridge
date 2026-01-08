#!/bin/bash
# TrustBridge Cloud-Init Script
# This script runs on first boot to set up the GPU VM with Docker and TrustBridge containers.
#
# Environment variables are injected by the Bicep template:
#   TB_CONTRACT_ID, TB_ASSET_ID, TB_EDC_ENDPOINT
#   SENTINEL_IMAGE, RUNTIME_IMAGE, ACR_NAME
#   TB_BILLING_ENABLED, TB_LOG_LEVEL

set -euo pipefail

# Logging
LOG_FILE="/var/log/trustbridge-init.log"
exec > >(tee -a "$LOG_FILE") 2>&1
echo "$(date -u '+%Y-%m-%dT%H:%M:%SZ') - TrustBridge cloud-init starting..."

# ==============================================================================
# Step 1: System Updates and Prerequisites
# ==============================================================================
echo "$(date -u '+%Y-%m-%dT%H:%M:%SZ') - Installing prerequisites..."

export DEBIAN_FRONTEND=noninteractive
apt-get update -y
apt-get install -y \
    apt-transport-https \
    ca-certificates \
    curl \
    gnupg \
    lsb-release \
    jq

# ==============================================================================
# Step 2: Install Docker
# ==============================================================================
echo "$(date -u '+%Y-%m-%dT%H:%M:%SZ') - Installing Docker..."

# Add Docker's official GPG key
install -m 0755 -d /etc/apt/keyrings
curl -fsSL https://download.docker.com/linux/ubuntu/gpg | gpg --dearmor -o /etc/apt/keyrings/docker.gpg
chmod a+r /etc/apt/keyrings/docker.gpg

# Add Docker repository
echo \
  "deb [arch=$(dpkg --print-architecture) signed-by=/etc/apt/keyrings/docker.gpg] https://download.docker.com/linux/ubuntu \
  $(. /etc/os-release && echo "$VERSION_CODENAME") stable" | \
  tee /etc/apt/sources.list.d/docker.list > /dev/null

apt-get update -y
apt-get install -y docker-ce docker-ce-cli containerd.io docker-buildx-plugin docker-compose-plugin

# Enable and start Docker
systemctl enable docker
systemctl start docker

echo "$(date -u '+%Y-%m-%dT%H:%M:%SZ') - Docker installed successfully"

# ==============================================================================
# Step 3: Install NVIDIA Container Toolkit
# ==============================================================================
echo "$(date -u '+%Y-%m-%dT%H:%M:%SZ') - Installing NVIDIA Container Toolkit..."

# Add NVIDIA container toolkit repository
distribution=$(. /etc/os-release;echo $ID$VERSION_ID)
curl -fsSL https://nvidia.github.io/libnvidia-container/gpgkey | \
    gpg --dearmor -o /usr/share/keyrings/nvidia-container-toolkit-keyring.gpg
curl -s -L https://nvidia.github.io/libnvidia-container/$distribution/libnvidia-container.list | \
    sed 's#deb https://#deb [signed-by=/usr/share/keyrings/nvidia-container-toolkit-keyring.gpg] https://#g' | \
    tee /etc/apt/sources.list.d/nvidia-container-toolkit.list

apt-get update -y
apt-get install -y nvidia-container-toolkit

# Configure Docker to use NVIDIA runtime
nvidia-ctk runtime configure --runtime=docker
systemctl restart docker

echo "$(date -u '+%Y-%m-%dT%H:%M:%SZ') - NVIDIA Container Toolkit installed successfully"

# ==============================================================================
# Step 4: Install Azure CLI for ACR Authentication
# ==============================================================================
echo "$(date -u '+%Y-%m-%dT%H:%M:%SZ') - Installing Azure CLI..."

curl -sL https://aka.ms/InstallAzureCLIDeb | bash

echo "$(date -u '+%Y-%m-%dT%H:%M:%SZ') - Azure CLI installed successfully"

# ==============================================================================
# Step 5: Create TrustBridge Directories
# ==============================================================================
echo "$(date -u '+%Y-%m-%dT%H:%M:%SZ') - Creating TrustBridge directories..."

# Ephemeral storage for encrypted downloads (NVMe on GPU VMs)
# /mnt/resource is ephemeral storage on Azure VMs
TB_DATA_DIR="/mnt/resource/trustbridge"
mkdir -p "$TB_DATA_DIR"
chmod 700 "$TB_DATA_DIR"

# Shared memory for FIFO and signals
SHARED_SHM_DIR="/dev/shm/trustbridge"
mkdir -p "$SHARED_SHM_DIR"
chmod 700 "$SHARED_SHM_DIR"

# Create weights subdirectory for ready signal
mkdir -p "$SHARED_SHM_DIR/weights"

echo "$(date -u '+%Y-%m-%dT%H:%M:%SZ') - Directories created at $TB_DATA_DIR and $SHARED_SHM_DIR"

# ==============================================================================
# Step 6: Authenticate to Azure Container Registry (if provided)
# ==============================================================================
if [ -n "${ACR_NAME:-}" ]; then
    echo "$(date -u '+%Y-%m-%dT%H:%M:%SZ') - Authenticating to ACR: $ACR_NAME..."
    
    # Login using VM's managed identity
    az login --identity --allow-no-subscriptions
    
    # Login to ACR
    az acr login --name "$ACR_NAME"
    
    echo "$(date -u '+%Y-%m-%dT%H:%M:%SZ') - ACR authentication successful"
else
    echo "$(date -u '+%Y-%m-%dT%H:%M:%SZ') - No ACR specified, skipping ACR login"
fi

# ==============================================================================
# Step 7: Pull Container Images
# ==============================================================================
echo "$(date -u '+%Y-%m-%dT%H:%M:%SZ') - Pulling container images..."

docker pull "$SENTINEL_IMAGE"
docker pull "$RUNTIME_IMAGE"

echo "$(date -u '+%Y-%m-%dT%H:%M:%SZ') - Container images pulled successfully"

# ==============================================================================
# Step 8: Create Docker Compose Configuration
# ==============================================================================
echo "$(date -u '+%Y-%m-%dT%H:%M:%SZ') - Creating docker-compose configuration..."

TB_COMPOSE_DIR="/opt/trustbridge"
mkdir -p "$TB_COMPOSE_DIR"

cat > "$TB_COMPOSE_DIR/docker-compose.yml" << 'COMPOSE_EOF'
version: "3.8"

# TrustBridge Production Configuration
# Sentinel (security sidecar) + Runtime (inference server)

services:
  # Sentinel - TrustBridge security sidecar
  sentinel:
    image: ${SENTINEL_IMAGE}
    restart: unless-stopped
    ports:
      - "8000:8000"   # Public API
      - "8001:8001"   # Health endpoint
    environment:
      - TB_CONTRACT_ID=${TB_CONTRACT_ID}
      - TB_ASSET_ID=${TB_ASSET_ID}
      - TB_EDC_ENDPOINT=${TB_EDC_ENDPOINT}
      - TB_TARGET_DIR=/mnt/resource/trustbridge
      - TB_PIPE_PATH=/shared-shm/model-pipe
      - TB_READY_SIGNAL=/shared-shm/weights/ready.signal
      - TB_RUNTIME_URL=http://127.0.0.1:8081
      - TB_PUBLIC_ADDR=0.0.0.0:8000
      - TB_HEALTH_ADDR=0.0.0.0:8001
      - TB_DOWNLOAD_CONCURRENCY=4
      - TB_LOG_LEVEL=${TB_LOG_LEVEL:-info}
      - TB_BILLING_ENABLED=${TB_BILLING_ENABLED:-true}
    volumes:
      # Ephemeral storage for encrypted downloads
      - /mnt/resource/trustbridge:/mnt/resource/trustbridge
      # Shared memory for FIFO and signals
      - /dev/shm/trustbridge:/shared-shm
    healthcheck:
      test: ["CMD", "curl", "-sf", "http://localhost:8001/health"]
      interval: 30s
      timeout: 10s
      retries: 5
      start_period: 120s  # Allow time for download + hydration

  # Runtime - Inference server (shares network with sentinel)
  runtime:
    image: ${RUNTIME_IMAGE}
    restart: unless-stopped
    # Share network namespace with sentinel - runtime accessible via 127.0.0.1:8081
    network_mode: "service:sentinel"
    # GPU access
    deploy:
      resources:
        reservations:
          devices:
            - driver: nvidia
              count: all
              capabilities: [gpu]
    environment:
      - TB_PIPE_PATH=/shared-shm/model-pipe
      - TB_READY_SIGNAL=/shared-shm/weights/ready.signal
    volumes:
      # Shared memory for FIFO and signals (same as sentinel)
      - /dev/shm/trustbridge:/shared-shm
    depends_on:
      sentinel:
        condition: service_started
COMPOSE_EOF

echo "$(date -u '+%Y-%m-%dT%H:%M:%SZ') - Docker compose configuration created at $TB_COMPOSE_DIR/docker-compose.yml"

# ==============================================================================
# Step 9: Create Environment File
# ==============================================================================
cat > "$TB_COMPOSE_DIR/.env" << ENV_EOF
# TrustBridge Environment Configuration
# Generated by cloud-init on $(date -u '+%Y-%m-%dT%H:%M:%SZ')

TB_CONTRACT_ID=${TB_CONTRACT_ID}
TB_ASSET_ID=${TB_ASSET_ID}
TB_EDC_ENDPOINT=${TB_EDC_ENDPOINT}
SENTINEL_IMAGE=${SENTINEL_IMAGE}
RUNTIME_IMAGE=${RUNTIME_IMAGE}
TB_LOG_LEVEL=${TB_LOG_LEVEL:-info}
TB_BILLING_ENABLED=${TB_BILLING_ENABLED:-true}
ENV_EOF

chmod 600 "$TB_COMPOSE_DIR/.env"

echo "$(date -u '+%Y-%m-%dT%H:%M:%SZ') - Environment file created at $TB_COMPOSE_DIR/.env"

# ==============================================================================
# Step 10: Create Systemd Service for TrustBridge
# ==============================================================================
echo "$(date -u '+%Y-%m-%dT%H:%M:%SZ') - Creating systemd service..."

cat > /etc/systemd/system/trustbridge.service << 'SERVICE_EOF'
[Unit]
Description=TrustBridge Secure Inference Platform
Documentation=https://github.com/trustbridge
After=docker.service
Requires=docker.service

[Service]
Type=oneshot
RemainAfterExit=yes
WorkingDirectory=/opt/trustbridge
ExecStart=/usr/bin/docker compose up -d
ExecStop=/usr/bin/docker compose down
ExecReload=/usr/bin/docker compose restart

# Environment file with TrustBridge configuration
EnvironmentFile=/opt/trustbridge/.env

# Restart policy
Restart=on-failure
RestartSec=10s

# Security hardening
NoNewPrivileges=false

[Install]
WantedBy=multi-user.target
SERVICE_EOF

systemctl daemon-reload
systemctl enable trustbridge.service

echo "$(date -u '+%Y-%m-%dT%H:%M:%SZ') - Systemd service created and enabled"

# ==============================================================================
# Step 11: Start TrustBridge Services
# ==============================================================================
echo "$(date -u '+%Y-%m-%dT%H:%M:%SZ') - Starting TrustBridge services..."

cd "$TB_COMPOSE_DIR"
docker compose up -d

echo "$(date -u '+%Y-%m-%dT%H:%M:%SZ') - TrustBridge services started"

# ==============================================================================
# Step 12: Wait for Health Check
# ==============================================================================
echo "$(date -u '+%Y-%m-%dT%H:%M:%SZ') - Waiting for sentinel to become healthy..."

MAX_WAIT=300  # 5 minutes
INTERVAL=10
ELAPSED=0

while [ $ELAPSED -lt $MAX_WAIT ]; do
    if curl -sf http://localhost:8001/health > /dev/null 2>&1; then
        echo "$(date -u '+%Y-%m-%dT%H:%M:%SZ') - Sentinel is healthy!"
        break
    fi
    echo "$(date -u '+%Y-%m-%dT%H:%M:%SZ') - Waiting for sentinel... ($ELAPSED/$MAX_WAIT seconds)"
    sleep $INTERVAL
    ELAPSED=$((ELAPSED + INTERVAL))
done

if [ $ELAPSED -ge $MAX_WAIT ]; then
    echo "$(date -u '+%Y-%m-%dT%H:%M:%SZ') - WARNING: Sentinel did not become healthy within $MAX_WAIT seconds"
    echo "$(date -u '+%Y-%m-%dT%H:%M:%SZ') - Check logs with: docker compose logs -f"
fi

# ==============================================================================
# Step 13: Final Status
# ==============================================================================
echo "$(date -u '+%Y-%m-%dT%H:%M:%SZ') - TrustBridge cloud-init complete!"
echo ""
echo "=== TrustBridge Status ==="
echo "Contract ID: ${TB_CONTRACT_ID}"
echo "Asset ID: ${TB_ASSET_ID}"
echo "EDC Endpoint: ${TB_EDC_ENDPOINT}"
echo ""
echo "=== Service Status ==="
docker compose ps
echo ""
echo "=== Useful Commands ==="
echo "View logs:        cd /opt/trustbridge && docker compose logs -f"
echo "Restart services: systemctl restart trustbridge"
echo "Stop services:    systemctl stop trustbridge"
echo "Check health:     curl http://localhost:8001/health"
echo ""
echo "$(date -u '+%Y-%m-%dT%H:%M:%SZ') - Cloud-init complete. Log file: $LOG_FILE"
