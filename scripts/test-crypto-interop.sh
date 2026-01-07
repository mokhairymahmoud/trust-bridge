#!/bin/bash
# Test crypto interoperability between Python encryption and Go decryption

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"
TEST_DIR="$ROOT_DIR/.interop-test"

echo "=== TrustBridge Crypto Interop Test ==="
echo ""

# Clean up any previous test
rm -rf "$TEST_DIR"
mkdir -p "$TEST_DIR"

# Create test plaintext
echo "1. Generating test plaintext..."
PLAINTEXT="TrustBridge Crypto Interop Test - $(date +%s)"
PLAINTEXT_FILE="$TEST_DIR/plain.txt"
echo -n "$PLAINTEXT" > "$PLAINTEXT_FILE"

# Compute expected hash
EXPECTED_HASH=$(shasum -a 256 "$PLAINTEXT_FILE" | awk '{print $1}')
echo "   Plaintext: $PLAINTEXT"
echo "   Expected SHA256: $EXPECTED_HASH"
echo ""

# Generate encryption key (known key for reproducibility)
KEY_HEX="0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
echo "2. Using test key: $KEY_HEX"
echo ""

# Encrypt with Python
echo "3. Encrypting with Python..."
cd "$ROOT_DIR/src/cli"
source venv/bin/activate

python3 << EOF
from pathlib import Path
from trustbridge_cli.crypto_tbenc import encrypt_file

key = bytes.fromhex("$KEY_HEX")
input_path = Path("$PLAINTEXT_FILE")
output_path = Path("$TEST_DIR/encrypted.tbenc")

ciphertext_sha256, plaintext_size = encrypt_file(
    input_path,
    output_path,
    key,
    chunk_bytes=1024
)

print(f"   Encrypted {plaintext_size} bytes")
print(f"   Ciphertext SHA256: {ciphertext_sha256}")
print(f"   Output: {output_path}")
EOF

echo ""

# Decrypt with Go
echo "4. Decrypting with Go..."
cd "$ROOT_DIR/src/sentinel"
GO_OUTPUT=$(go run ./cmd/decrypt-test/main.go "$TEST_DIR/encrypted.tbenc" "$KEY_HEX")
echo "$GO_OUTPUT" | sed 's/^/   /'
echo ""

# Extract actual hash from Go output
ACTUAL_HASH=$(echo "$GO_OUTPUT" | grep "Plaintext SHA256:" | awk '{print $3}')

# Verify hash matches
echo "5. Verifying interoperability..."
if [ "$EXPECTED_HASH" == "$ACTUAL_HASH" ]; then
    echo "   ✓ SUCCESS: Hashes match!"
    echo "   ✓ Python encryption → Go decryption: WORKING"
else
    echo "   ✗ FAILURE: Hash mismatch!"
    echo "   Expected: $EXPECTED_HASH"
    echo "   Actual:   $ACTUAL_HASH"
    exit 1
fi

echo ""
echo "=== Crypto Interop Test PASSED ==="
echo ""

# Clean up
rm -rf "$TEST_DIR"
