#!/bin/bash
# Test tbenc/v1 with multiple chunk sizes (1MB, 4MB, 16MB) as required by Phase 1

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"
TEST_DIR="$ROOT_DIR/.chunk-test"

echo "=== TrustBridge Chunk Size Validation ==="
echo ""

# Clean up any previous test
rm -rf "$TEST_DIR"
mkdir -p "$TEST_DIR"

# Create test plaintext (17MB to test all chunk sizes)
echo "1. Generating 17MB test file..."
PLAINTEXT_FILE="$TEST_DIR/plain.bin"
dd if=/dev/urandom of="$PLAINTEXT_FILE" bs=1048576 count=17 2>/dev/null

EXPECTED_HASH=$(shasum -a 256 "$PLAINTEXT_FILE" | awk '{print $1}')
PLAINTEXT_SIZE=$(stat -f%z "$PLAINTEXT_FILE" 2>/dev/null || stat -c%s "$PLAINTEXT_FILE")
echo "   Generated $PLAINTEXT_SIZE bytes"
echo "   Expected SHA256: $EXPECTED_HASH"
echo ""

# Generate encryption key
KEY_HEX="fedcba9876543210fedcba9876543210fedcba9876543210fedcba9876543210"

# Test function
test_chunk_size() {
    local CHUNK_SIZE=$1
    local CHUNK_NAME=$2

    echo "Testing $CHUNK_NAME chunk size..."

    cd "$ROOT_DIR/src/cli"
    source venv/bin/activate

    # Encrypt with Python
    python3 << EOF
from pathlib import Path
from trustbridge_cli.crypto_tbenc import encrypt_file

key = bytes.fromhex("$KEY_HEX")
input_path = Path("$PLAINTEXT_FILE")
output_path = Path("$TEST_DIR/encrypted_${CHUNK_SIZE}.tbenc")

ciphertext_sha256, plaintext_size = encrypt_file(
    input_path,
    output_path,
    key,
    chunk_bytes=$CHUNK_SIZE
)

print(f"   Encrypted: {plaintext_size} bytes with $CHUNK_NAME chunks")
print(f"   Ciphertext SHA256: {ciphertext_sha256}")
EOF

    # Decrypt with Go
    cd "$ROOT_DIR/src/sentinel"
    GO_OUTPUT=$(go run ./cmd/decrypt-test/main.go "$TEST_DIR/encrypted_${CHUNK_SIZE}.tbenc" "$KEY_HEX")
    ACTUAL_HASH=$(echo "$GO_OUTPUT" | grep "Plaintext SHA256:" | awk '{print $3}')

    # Verify
    if [ "$EXPECTED_HASH" == "$ACTUAL_HASH" ]; then
        echo "   ✓ Decrypted successfully"
    else
        echo "   ✗ Hash mismatch!"
        echo "   Expected: $EXPECTED_HASH"
        echo "   Actual:   $ACTUAL_HASH"
        exit 1
    fi

    echo ""
}

# Test all required chunk sizes
test_chunk_size "1048576" "1MB"     # 1MB = 1048576 bytes
test_chunk_size "4194304" "4MB"     # 4MB = 4194304 bytes
test_chunk_size "16777216" "16MB"   # 16MB = 16777216 bytes

echo "=== All Chunk Sizes Validated Successfully ==="
echo ""
echo "Summary:"
echo "  ✓ 1MB chunks: PASS"
echo "  ✓ 4MB chunks: PASS"
echo "  ✓ 16MB chunks: PASS"
echo ""
echo "Phase 1 chunk size requirement: COMPLETE"
echo ""

# Clean up
rm -rf "$TEST_DIR"
