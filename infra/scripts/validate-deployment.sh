#!/bin/bash
# TrustBridge Deployment Validation Script
# This script validates that a TrustBridge deployment is correctly configured
# and all security invariants are maintained.
#
# Usage:
#   ./validate-deployment.sh <VM_IP_OR_FQDN> [SSH_USER] [SSH_KEY_PATH]
#
# Example:
#   ./validate-deployment.sh trustbridge-abc123.eastus.cloudapp.azure.com azureuser ~/.ssh/id_rsa

set -euo pipefail

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

# Arguments
VM_HOST="${1:-}"
SSH_USER="${2:-azureuser}"
SSH_KEY="${3:-~/.ssh/id_rsa}"

if [ -z "$VM_HOST" ]; then
    echo -e "${RED}Error: VM hostname or IP required${NC}"
    echo "Usage: $0 <VM_IP_OR_FQDN> [SSH_USER] [SSH_KEY_PATH]"
    exit 1
fi

PASS_COUNT=0
FAIL_COUNT=0
WARN_COUNT=0

# Helper functions
log_pass() {
    echo -e "${GREEN}✓ PASS:${NC} $1"
    PASS_COUNT=$((PASS_COUNT + 1))
}

log_fail() {
    echo -e "${RED}✗ FAIL:${NC} $1"
    FAIL_COUNT=$((FAIL_COUNT + 1))
}

log_warn() {
    echo -e "${YELLOW}⚠ WARN:${NC} $1"
    WARN_COUNT=$((WARN_COUNT + 1))
}

log_info() {
    echo -e "  INFO: $1"
}

run_ssh() {
    ssh -o StrictHostKeyChecking=no -o ConnectTimeout=10 -i "$SSH_KEY" "$SSH_USER@$VM_HOST" "$1" 2>/dev/null
}

echo "========================================"
echo "TrustBridge Deployment Validation"
echo "========================================"
echo ""
echo "Target: $SSH_USER@$VM_HOST"
echo "SSH Key: $SSH_KEY"
echo ""
echo "Starting validation..."
echo ""

# ==============================================================================
# Test 1: SSH Connectivity
# ==============================================================================
echo "--- Test 1: SSH Connectivity ---"
if ssh -o StrictHostKeyChecking=no -o ConnectTimeout=10 -i "$SSH_KEY" "$SSH_USER@$VM_HOST" "echo 'SSH OK'" > /dev/null 2>&1; then
    log_pass "SSH connection successful"
else
    log_fail "Cannot establish SSH connection"
    echo -e "${RED}Cannot continue without SSH access${NC}"
    exit 1
fi

# ==============================================================================
# Test 2: Docker Running
# ==============================================================================
echo ""
echo "--- Test 2: Docker Service ---"
if run_ssh "systemctl is-active docker" | grep -q "active"; then
    log_pass "Docker service is running"
else
    log_fail "Docker service is not running"
fi

# ==============================================================================
# Test 3: NVIDIA Container Toolkit
# ==============================================================================
echo ""
echo "--- Test 3: NVIDIA Container Toolkit ---"
if run_ssh "nvidia-smi" > /dev/null 2>&1; then
    GPU_INFO=$(run_ssh "nvidia-smi --query-gpu=gpu_name,memory.total --format=csv,noheader" 2>/dev/null | head -1)
    log_pass "NVIDIA GPU detected: $GPU_INFO"
else
    log_warn "NVIDIA GPU not detected (may be expected for non-GPU testing)"
fi

# ==============================================================================
# Test 4: TrustBridge Containers Running
# ==============================================================================
echo ""
echo "--- Test 4: Container Status ---"
CONTAINERS=$(run_ssh "docker ps --format '{{.Names}}: {{.Status}}'" 2>/dev/null || echo "")

if echo "$CONTAINERS" | grep -q "sentinel"; then
    SENTINEL_STATUS=$(echo "$CONTAINERS" | grep "sentinel" | head -1)
    log_pass "Sentinel container running: $SENTINEL_STATUS"
else
    log_fail "Sentinel container not found"
fi

if echo "$CONTAINERS" | grep -q "runtime"; then
    RUNTIME_STATUS=$(echo "$CONTAINERS" | grep "runtime" | head -1)
    log_pass "Runtime container running: $RUNTIME_STATUS"
else
    log_fail "Runtime container not found"
fi

# ==============================================================================
# Test 5: Health Endpoint (Internal)
# ==============================================================================
echo ""
echo "--- Test 5: Health Endpoint (Internal) ---"
HEALTH_RESPONSE=$(run_ssh "curl -sf http://localhost:8001/health" 2>/dev/null || echo "FAILED")
if [ "$HEALTH_RESPONSE" != "FAILED" ]; then
    log_pass "Health endpoint responding on localhost:8001"
    log_info "Response: $HEALTH_RESPONSE"
else
    log_fail "Health endpoint not responding"
fi

# ==============================================================================
# Test 6: Health Endpoint (External)
# ==============================================================================
echo ""
echo "--- Test 6: Health Endpoint (External) ---"
EXTERNAL_HEALTH=$(curl -sf --connect-timeout 10 "http://$VM_HOST:8001/health" 2>/dev/null || echo "FAILED")
if [ "$EXTERNAL_HEALTH" != "FAILED" ]; then
    log_pass "Health endpoint accessible externally on port 8001"
else
    log_warn "Health endpoint not accessible externally (may be blocked by NSG)"
fi

# ==============================================================================
# Test 7: Sentinel API (External)
# ==============================================================================
echo ""
echo "--- Test 7: Sentinel API (External) ---"
SENTINEL_RESPONSE=$(curl -sf --connect-timeout 10 "http://$VM_HOST:8000/health" 2>/dev/null || echo "FAILED")
if [ "$SENTINEL_RESPONSE" != "FAILED" ]; then
    log_pass "Sentinel API accessible externally on port 8000"
else
    log_warn "Sentinel API not accessible externally (may still be initializing)"
fi

# ==============================================================================
# Test 8: Runtime Port Blocked Externally (SECURITY CRITICAL)
# ==============================================================================
echo ""
echo "--- Test 8: Runtime Port Blocked (SECURITY CRITICAL) ---"
if timeout 5 bash -c "echo > /dev/tcp/$VM_HOST/8081" 2>/dev/null; then
    log_fail "SECURITY VIOLATION: Runtime port 8081 is accessible externally!"
else
    log_pass "Runtime port 8081 is blocked externally"
fi

# ==============================================================================
# Test 9: Runtime Bound to Localhost Only
# ==============================================================================
echo ""
echo "--- Test 9: Runtime Network Binding ---"
RUNTIME_LISTEN=$(run_ssh "docker exec \$(docker ps -q -f name=runtime) ss -tlnp 2>/dev/null | grep 8081 || echo ''" 2>/dev/null || echo "CANNOT_CHECK")
if [ "$RUNTIME_LISTEN" = "CANNOT_CHECK" ]; then
    log_warn "Cannot verify runtime network binding"
elif echo "$RUNTIME_LISTEN" | grep -q "127.0.0.1:8081"; then
    log_pass "Runtime bound to localhost only (127.0.0.1:8081)"
elif [ -z "$RUNTIME_LISTEN" ]; then
    log_info "Runtime not yet listening on 8081 (may be waiting for ready signal)"
else
    log_warn "Runtime binding: $RUNTIME_LISTEN"
fi

# ==============================================================================
# Test 10: No Plaintext on Disk
# ==============================================================================
echo ""
echo "--- Test 10: No Plaintext on Disk (SECURITY CRITICAL) ---"
TB_TARGET_DIR="/mnt/resource/trustbridge"
FILES_IN_TARGET=$(run_ssh "ls -la $TB_TARGET_DIR 2>/dev/null || echo 'DIR_NOT_FOUND'" 2>/dev/null)

if [ "$FILES_IN_TARGET" = "DIR_NOT_FOUND" ]; then
    log_info "Target directory not yet created (sentinel may still be initializing)"
else
    # Check for plaintext files (anything without .tbenc extension)
    PLAINTEXT_FILES=$(run_ssh "find $TB_TARGET_DIR -type f ! -name '*.tbenc' ! -name '*.manifest.json' ! -name '*.json' 2>/dev/null" || echo "")
    if [ -z "$PLAINTEXT_FILES" ]; then
        log_pass "No plaintext files found in $TB_TARGET_DIR"
    else
        log_fail "SECURITY VIOLATION: Potential plaintext files found:"
        echo "$PLAINTEXT_FILES"
    fi
fi

# ==============================================================================
# Test 11: FIFO Exists in /dev/shm
# ==============================================================================
echo ""
echo "--- Test 11: FIFO Configuration ---"
FIFO_CHECK=$(run_ssh "stat /dev/shm/trustbridge/model-pipe 2>/dev/null | head -3 || echo 'NOT_FOUND'" 2>/dev/null)
if echo "$FIFO_CHECK" | grep -q "NOT_FOUND"; then
    log_info "FIFO not yet created (sentinel may still be initializing)"
else
    if run_ssh "test -p /dev/shm/trustbridge/model-pipe" 2>/dev/null; then
        log_pass "FIFO exists at /dev/shm/trustbridge/model-pipe"
    else
        log_warn "FIFO path exists but is not a named pipe"
    fi
fi

# ==============================================================================
# Test 12: Systemd Service
# ==============================================================================
echo ""
echo "--- Test 12: Systemd Service ---"
SERVICE_STATUS=$(run_ssh "systemctl is-active trustbridge.service 2>/dev/null || echo 'not-found'" 2>/dev/null)
if [ "$SERVICE_STATUS" = "active" ]; then
    log_pass "TrustBridge systemd service is active"
elif [ "$SERVICE_STATUS" = "not-found" ]; then
    log_warn "TrustBridge systemd service not found"
else
    log_info "TrustBridge service status: $SERVICE_STATUS"
fi

# ==============================================================================
# Test 13: Docker Compose Configuration
# ==============================================================================
echo ""
echo "--- Test 13: Configuration Files ---"
if run_ssh "test -f /opt/trustbridge/docker-compose.yml" 2>/dev/null; then
    log_pass "Docker compose configuration exists"
else
    log_fail "Docker compose configuration not found"
fi

if run_ssh "test -f /opt/trustbridge/.env" 2>/dev/null; then
    log_pass "Environment configuration exists"
    # Verify key variables are set (without exposing values)
    REQUIRED_VARS=("TB_CONTRACT_ID" "TB_ASSET_ID" "TB_EDC_ENDPOINT" "SENTINEL_IMAGE" "RUNTIME_IMAGE")
    for var in "${REQUIRED_VARS[@]}"; do
        if run_ssh "grep -q '^$var=' /opt/trustbridge/.env" 2>/dev/null; then
            log_pass "  - $var is configured"
        else
            log_fail "  - $var is missing"
        fi
    done
else
    log_fail "Environment configuration not found"
fi

# ==============================================================================
# Test 14: Log Files
# ==============================================================================
echo ""
echo "--- Test 14: Logging ---"
if run_ssh "test -f /var/log/trustbridge-init.log" 2>/dev/null; then
    log_pass "Cloud-init log file exists"
    INIT_STATUS=$(run_ssh "tail -1 /var/log/trustbridge-init.log 2>/dev/null | grep -c 'complete' || echo '0'" 2>/dev/null)
    if [ "$INIT_STATUS" != "0" ]; then
        log_pass "Cloud-init completed successfully"
    else
        log_warn "Cloud-init may not have completed"
    fi
else
    log_warn "Cloud-init log file not found"
fi

# ==============================================================================
# Summary
# ==============================================================================
echo ""
echo "========================================"
echo "Validation Summary"
echo "========================================"
echo -e "${GREEN}Passed:${NC}  $PASS_COUNT"
echo -e "${RED}Failed:${NC}  $FAIL_COUNT"
echo -e "${YELLOW}Warnings:${NC} $WARN_COUNT"
echo ""

if [ $FAIL_COUNT -eq 0 ]; then
    echo -e "${GREEN}✓ Deployment validation PASSED${NC}"
    exit 0
else
    echo -e "${RED}✗ Deployment validation FAILED${NC}"
    echo "Please review the failed tests above."
    exit 1
fi
