#!/bin/bash
# Test FIFO decryption flow: Python encrypt -> Go decrypt to FIFO -> verify hash
#
# This script validates the complete FIFO decryption pipeline:
# 1. Generates deterministic test plaintext
# 2. Encrypts with Python tbenc/v1
# 3. Starts Go decrypt to FIFO in background
# 4. Reads from FIFO in parallel and computes SHA256
# 5. Verifies the hashes match
#
# Usage:
#   ./scripts/test-fifo.sh [--size <bytes>] [--chunk-size <bytes>]
#
# Options:
#   --size        Size of test plaintext in bytes (default: 1048576 = 1MB)
#   --chunk-size  Encryption chunk size in bytes (default: 65536 = 64KB)

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"
TEST_DIR="$ROOT_DIR/.fifo-test"

# Default parameters
PLAINTEXT_SIZE=${PLAINTEXT_SIZE:-1048576}  # 1MB
CHUNK_SIZE=${CHUNK_SIZE:-65536}            # 64KB
TIMEOUT_SECONDS=60

# Parse arguments
while [[ $# -gt 0 ]]; do
    case $1 in
        --size)
            PLAINTEXT_SIZE="$2"
            shift 2
            ;;
        --chunk-size)
            CHUNK_SIZE="$2"
            shift 2
            ;;
        *)
            echo "Unknown option: $1"
            exit 1
            ;;
    esac
done

# Cleanup function
cleanup() {
    echo ""
    echo "Cleaning up..."

    # Kill background processes
    if [[ -n "$DECRYPT_PID" ]] && kill -0 "$DECRYPT_PID" 2>/dev/null; then
        kill "$DECRYPT_PID" 2>/dev/null || true
    fi

    # Remove test directory
    rm -rf "$TEST_DIR"
}

# Set up trap for cleanup
trap cleanup EXIT

echo "=== TrustBridge FIFO Decryption Test ==="
echo ""
echo "Parameters:"
echo "  Plaintext size: $PLAINTEXT_SIZE bytes"
echo "  Chunk size: $CHUNK_SIZE bytes"
echo ""

# Clean up any previous test
rm -rf "$TEST_DIR"
mkdir -p "$TEST_DIR"

# File paths
PLAINTEXT_FILE="$TEST_DIR/plain.bin"
ENCRYPTED_FILE="$TEST_DIR/encrypted.tbenc"
FIFO_PATH="$TEST_DIR/model-pipe"
DECRYPTED_FILE="$TEST_DIR/decrypted.bin"

# Known key for testing
KEY_HEX="0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

# Step 1: Generate deterministic test plaintext
echo "1. Generating deterministic test plaintext ($PLAINTEXT_SIZE bytes)..."
python3 << EOF
import hashlib

size = $PLAINTEXT_SIZE
# Generate deterministic data using a repeating pattern
pattern = b'TrustBridge-FIFO-Test-Pattern-'
data = (pattern * (size // len(pattern) + 1))[:size]

with open('$PLAINTEXT_FILE', 'wb') as f:
    f.write(data)

hash_value = hashlib.sha256(data).hexdigest()
print(f"   Generated {len(data)} bytes")
print(f"   SHA256: {hash_value}")
EOF

# Compute expected hash
EXPECTED_HASH=$(shasum -a 256 "$PLAINTEXT_FILE" | awk '{print $1}')
echo "   Expected SHA256: $EXPECTED_HASH"
echo ""

# Step 2: Encrypt with Python
echo "2. Encrypting with Python..."
cd "$ROOT_DIR/src/cli"

# Check if virtual environment exists
if [[ ! -d "venv" ]]; then
    echo "   Error: Python virtual environment not found at $ROOT_DIR/src/cli/venv"
    echo "   Please run: cd $ROOT_DIR/src/cli && python3 -m venv venv && source venv/bin/activate && pip install -e ."
    exit 1
fi

source venv/bin/activate

python3 << EOF
from pathlib import Path
from trustbridge_cli.crypto_tbenc import encrypt_file

key = bytes.fromhex("$KEY_HEX")
input_path = Path("$PLAINTEXT_FILE")
output_path = Path("$ENCRYPTED_FILE")

ciphertext_sha256, plaintext_size = encrypt_file(
    input_path,
    output_path,
    key,
    chunk_bytes=$CHUNK_SIZE
)

print(f"   Encrypted {plaintext_size} bytes")
print(f"   Ciphertext SHA256: {ciphertext_sha256}")
print(f"   Output file size: {output_path.stat().st_size} bytes")
EOF

echo ""

# Step 3: Start FIFO decryption in background
echo "3. Starting FIFO decryption (Go)..."
cd "$ROOT_DIR/src/sentinel"

# Start decryption in background
go run ./cmd/fifo-test/main.go "$ENCRYPTED_FILE" "$KEY_HEX" "$FIFO_PATH" &
DECRYPT_PID=$!

# Wait for FIFO to be created
echo "   Waiting for FIFO to be ready..."
WAIT_COUNT=0
while [[ ! -p "$FIFO_PATH" ]] && [[ $WAIT_COUNT -lt 30 ]]; do
    sleep 0.1
    WAIT_COUNT=$((WAIT_COUNT + 1))
done

if [[ ! -p "$FIFO_PATH" ]]; then
    echo "   Error: FIFO was not created within timeout"
    exit 1
fi

echo "   FIFO created at: $FIFO_PATH"
echo ""

# Step 4: Read from FIFO and compute hash
echo "4. Reading from FIFO and computing hash..."
cat "$FIFO_PATH" > "$DECRYPTED_FILE" &
CAT_PID=$!

# Wait for decryption to complete with timeout
WAIT_COUNT=0
while kill -0 "$DECRYPT_PID" 2>/dev/null && [[ $WAIT_COUNT -lt $((TIMEOUT_SECONDS * 10)) ]]; do
    sleep 0.1
    WAIT_COUNT=$((WAIT_COUNT + 1))
done

# Wait for cat to finish
wait "$CAT_PID" 2>/dev/null || true

# Check if decrypt process is still running (timeout)
if kill -0 "$DECRYPT_PID" 2>/dev/null; then
    echo "   Error: Decryption timed out after $TIMEOUT_SECONDS seconds"
    kill "$DECRYPT_PID" 2>/dev/null || true
    exit 1
fi

# Wait for decrypt process to finish and get exit code
wait "$DECRYPT_PID" || {
    echo "   Error: Decryption process failed"
    exit 1
}

DECRYPTED_SIZE=$(stat -f%z "$DECRYPTED_FILE" 2>/dev/null || stat -c%s "$DECRYPTED_FILE")
echo "   Decrypted $DECRYPTED_SIZE bytes"
echo ""

# Step 5: Verify hash
echo "5. Verifying hash..."
ACTUAL_HASH=$(shasum -a 256 "$DECRYPTED_FILE" | awk '{print $1}')
echo "   Expected: $EXPECTED_HASH"
echo "   Actual:   $ACTUAL_HASH"
echo ""

if [[ "$EXPECTED_HASH" == "$ACTUAL_HASH" ]]; then
    echo "=== FIFO Decryption Test PASSED ==="
    echo ""
    echo "Summary:"
    echo "  - Plaintext size: $PLAINTEXT_SIZE bytes"
    echo "  - Chunk size: $CHUNK_SIZE bytes"
    echo "  - Hash verification: OK"
    echo "  - Python -> Go FIFO interop: WORKING"
    echo ""
    exit 0
else
    echo "=== FIFO Decryption Test FAILED ==="
    echo ""
    echo "Hash mismatch!"
    echo "  Expected: $EXPECTED_HASH"
    echo "  Actual:   $ACTUAL_HASH"
    echo ""
    exit 1
fi
