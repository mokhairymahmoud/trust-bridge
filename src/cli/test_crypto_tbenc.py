"""
Unit tests for tbenc/v1 encryption.
"""

import hashlib
import struct
import tempfile
from pathlib import Path

import pytest

from trustbridge_cli.crypto_tbenc import (
    MAGIC,
    VERSION,
    ALGO_AES_GCM_CHUNKED,
    HEADER_SIZE,
    generate_key,
    encrypt_file,
    write_manifest,
    encrypt_and_generate_manifest,
)


def test_generate_key():
    """Test key generation produces 32 bytes."""
    key1 = generate_key()
    key2 = generate_key()

    assert len(key1) == 32
    assert len(key2) == 32
    assert key1 != key2  # Should be random


def test_encrypt_small_file():
    """Test encryption of a small file."""
    with tempfile.TemporaryDirectory() as tmpdir:
        tmpdir = Path(tmpdir)

        # Create test input
        plaintext = b"TRUSTBRIDGE_TEST_DATA" * 100
        input_path = tmpdir / "plain.txt"
        input_path.write_bytes(plaintext)

        # Encrypt
        key = generate_key()
        output_path = tmpdir / "encrypted.tbenc"
        ciphertext_sha256, plaintext_size = encrypt_file(
            input_path, output_path, key, chunk_bytes=1024
        )

        # Verify
        assert plaintext_size == len(plaintext)
        assert output_path.exists()

        # Verify header
        with open(output_path, "rb") as f:
            header = f.read(HEADER_SIZE)
            assert len(header) == HEADER_SIZE
            assert header[0:8] == MAGIC

            # Parse version
            version = struct.unpack(">H", header[8:10])[0]
            assert version == VERSION

            # Parse algo
            algo = struct.unpack(">B", header[10:11])[0]
            assert algo == ALGO_AES_GCM_CHUNKED

            # Parse chunk_bytes
            chunk_bytes = struct.unpack(">I", header[11:15])[0]
            assert chunk_bytes == 1024


def test_encrypt_empty_file():
    """Test encryption of an empty file."""
    with tempfile.TemporaryDirectory() as tmpdir:
        tmpdir = Path(tmpdir)

        # Create empty file
        input_path = tmpdir / "empty.txt"
        input_path.write_bytes(b"")

        # Encrypt
        key = generate_key()
        output_path = tmpdir / "encrypted.tbenc"
        ciphertext_sha256, plaintext_size = encrypt_file(
            input_path, output_path, key, chunk_bytes=1024
        )

        # Verify
        assert plaintext_size == 0
        assert output_path.exists()

        # Should still have header
        assert output_path.stat().st_size == HEADER_SIZE


def test_encrypt_multiple_chunks():
    """Test encryption with multiple chunks."""
    with tempfile.TemporaryDirectory() as tmpdir:
        tmpdir = Path(tmpdir)

        # Create input larger than one chunk
        chunk_size = 1024
        plaintext = b"X" * (chunk_size * 3 + 500)  # 3.5 chunks
        input_path = tmpdir / "large.txt"
        input_path.write_bytes(plaintext)

        # Encrypt
        key = generate_key()
        output_path = tmpdir / "encrypted.tbenc"
        ciphertext_sha256, plaintext_size = encrypt_file(
            input_path, output_path, key, chunk_bytes=chunk_size
        )

        # Verify
        assert plaintext_size == len(plaintext)

        # Ciphertext should be larger than plaintext (overhead from headers, tags, etc)
        assert output_path.stat().st_size > len(plaintext)


def test_write_manifest():
    """Test manifest generation."""
    with tempfile.TemporaryDirectory() as tmpdir:
        tmpdir = Path(tmpdir)
        manifest_path = tmpdir / "test.manifest.json"

        write_manifest(
            manifest_path,
            asset_id="test-asset-001",
            weights_filename="model.tbenc",
            chunk_bytes=4194304,
            plaintext_bytes=1000000,
            sha256_ciphertext="abc123"
        )

        # Read and verify
        import json
        with open(manifest_path) as f:
            manifest = json.load(f)

        assert manifest["format"] == "tbenc/v1"
        assert manifest["algo"] == "aes-256-gcm-chunked"
        assert manifest["asset_id"] == "test-asset-001"
        assert manifest["chunk_bytes"] == 4194304
        assert manifest["plaintext_bytes"] == 1000000
        assert manifest["sha256_ciphertext"] == "abc123"


def test_encrypt_and_generate_manifest():
    """Test convenience function."""
    with tempfile.TemporaryDirectory() as tmpdir:
        tmpdir = Path(tmpdir)

        # Create test input
        plaintext = b"TRUSTBRIDGE" * 1000
        input_path = tmpdir / "plain.txt"
        input_path.write_bytes(plaintext)

        output_dir = tmpdir / "output"

        # Encrypt and generate manifest
        key, encrypted_path, manifest_path = encrypt_and_generate_manifest(
            input_path,
            output_dir,
            asset_id="test-asset",
            chunk_bytes=2048
        )

        # Verify
        assert len(key) == 32
        assert encrypted_path.exists()
        assert manifest_path.exists()

        import json
        with open(manifest_path) as f:
            manifest = json.load(f)

        assert manifest["asset_id"] == "test-asset"
        assert manifest["plaintext_bytes"] == len(plaintext)


def test_deterministic_encryption():
    """Test that same key and nonce prefix produce same output (for debugging)."""
    with tempfile.TemporaryDirectory() as tmpdir:
        tmpdir = Path(tmpdir)

        plaintext = b"DETERMINISTIC_TEST"
        input_path = tmpdir / "plain.txt"
        input_path.write_bytes(plaintext)

        # Use same key for both encryptions
        key = generate_key()

        output1 = tmpdir / "enc1.tbenc"
        output2 = tmpdir / "enc2.tbenc"

        encrypt_file(input_path, output1, key, chunk_bytes=1024)
        encrypt_file(input_path, output2, key, chunk_bytes=1024)

        # Outputs should be different (different nonce_prefix)
        assert output1.read_bytes() != output2.read_bytes()


def test_known_test_vector():
    """Test with a known plaintext to generate test vectors for Go interop."""
    with tempfile.TemporaryDirectory() as tmpdir:
        tmpdir = Path(tmpdir)

        # Known plaintext
        plaintext = b"TrustBridge-Test-Vector-123"
        plaintext_hash = hashlib.sha256(plaintext).hexdigest()

        input_path = tmpdir / "test_vector.txt"
        input_path.write_bytes(plaintext)

        # Generate known key (for testing only!)
        key = bytes.fromhex("0123456789abcdef" * 4)  # 32 bytes

        output_path = tmpdir / "test_vector.tbenc"
        ciphertext_sha256, plaintext_size = encrypt_file(
            input_path, output_path, key, chunk_bytes=1024
        )

        print(f"\n--- Test Vector for Go Interop ---")
        print(f"Plaintext: {plaintext.decode()}")
        print(f"Plaintext SHA256: {plaintext_hash}")
        print(f"Key (hex): {key.hex()}")
        print(f"Ciphertext SHA256: {ciphertext_sha256}")
        print(f"Plaintext size: {plaintext_size}")
        print(f"Ciphertext size: {output_path.stat().st_size}")

        assert plaintext_size == len(plaintext)
