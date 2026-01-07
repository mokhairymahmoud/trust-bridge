"""
TrustBridge tbenc/v1 encryption format implementation.

This module implements the chunked AES-256-GCM encryption format for securing
model weights during transit and storage.

Format specification:
- Magic: 8 bytes ASCII "TBENC001"
- Version: uint16 = 1
- Algorithm: uint8 = 1 (AES-256-GCM-CHUNKED)
- Chunk size: uint32 (recommended 4-16MB)
- Nonce prefix: 4 random bytes
- Reserved: 13 zero bytes
Total header: 32 bytes

Each record:
- pt_len: uint32 (plaintext length for this chunk)
- ct_and_tag: encrypted data + 16-byte GCM tag

AAD (Associated Authenticated Data):
  magic||version||algo||chunk_bytes||nonce_prefix||chunk_index||pt_len
"""

import hashlib
import json
import os
import struct
from pathlib import Path
from typing import BinaryIO, Dict, Tuple

from cryptography.hazmat.primitives.ciphers.aead import AESGCM


# Constants
MAGIC = b"TBENC001"
VERSION = 1
ALGO_AES_GCM_CHUNKED = 1
HEADER_SIZE = 32
NONCE_PREFIX_SIZE = 4
NONCE_SIZE = 12  # GCM standard nonce size
TAG_SIZE = 16  # GCM tag size
DEFAULT_CHUNK_SIZE = 4 * 1024 * 1024  # 4MB


def generate_key() -> bytes:
    """Generate a random 32-byte AES-256 key."""
    return os.urandom(32)


def _build_header(chunk_bytes: int, nonce_prefix: bytes) -> bytes:
    """Build the 32-byte tbenc/v1 header."""
    if len(nonce_prefix) != NONCE_PREFIX_SIZE:
        raise ValueError(f"nonce_prefix must be {NONCE_PREFIX_SIZE} bytes")

    header = bytearray(HEADER_SIZE)
    offset = 0

    # Magic (8 bytes)
    header[offset:offset+8] = MAGIC
    offset += 8

    # Version (uint16, big-endian)
    struct.pack_into(">H", header, offset, VERSION)
    offset += 2

    # Algorithm (uint8)
    struct.pack_into(">B", header, offset, ALGO_AES_GCM_CHUNKED)
    offset += 1

    # Chunk bytes (uint32, big-endian)
    struct.pack_into(">I", header, offset, chunk_bytes)
    offset += 4

    # Nonce prefix (4 bytes)
    header[offset:offset+NONCE_PREFIX_SIZE] = nonce_prefix
    offset += NONCE_PREFIX_SIZE

    # Reserved (13 bytes, zeros already set by bytearray initialization)

    return bytes(header)


def _derive_nonce(nonce_prefix: bytes, chunk_index: int) -> bytes:
    """
    Derive a 12-byte nonce from prefix and chunk index.

    Format: nonce_prefix (4 bytes) || counter (8 bytes, big-endian)
    """
    counter_bytes = struct.pack(">Q", chunk_index)
    return nonce_prefix + counter_bytes


def _build_aad(
    magic: bytes,
    version: int,
    algo: int,
    chunk_bytes: int,
    nonce_prefix: bytes,
    chunk_index: int,
    pt_len: int
) -> bytes:
    """
    Build Associated Authenticated Data for GCM.

    AAD = magic||version||algo||chunk_bytes||nonce_prefix||chunk_index||pt_len
    """
    aad = bytearray()
    aad.extend(magic)
    aad.extend(struct.pack(">H", version))
    aad.extend(struct.pack(">B", algo))
    aad.extend(struct.pack(">I", chunk_bytes))
    aad.extend(nonce_prefix)
    aad.extend(struct.pack(">Q", chunk_index))
    aad.extend(struct.pack(">I", pt_len))
    return bytes(aad)


def encrypt_file(
    input_path: Path,
    output_path: Path,
    key: bytes,
    chunk_bytes: int = DEFAULT_CHUNK_SIZE
) -> Tuple[str, int]:
    """
    Encrypt a file using tbenc/v1 format.

    Args:
        input_path: Path to plaintext input file
        output_path: Path to write encrypted output
        key: 32-byte AES-256 key
        chunk_bytes: Size of chunks for encryption (default 4MB)

    Returns:
        Tuple of (ciphertext_sha256_hex, plaintext_size_bytes)
    """
    if len(key) != 32:
        raise ValueError("Key must be 32 bytes for AES-256")

    if chunk_bytes < 1024 or chunk_bytes > 64 * 1024 * 1024:
        raise ValueError("chunk_bytes must be between 1KB and 64MB")

    aesgcm = AESGCM(key)
    nonce_prefix = os.urandom(NONCE_PREFIX_SIZE)

    plaintext_size = 0
    ciphertext_hasher = hashlib.sha256()

    with open(input_path, "rb") as fin, open(output_path, "wb") as fout:
        # Write header
        header = _build_header(chunk_bytes, nonce_prefix)
        fout.write(header)
        ciphertext_hasher.update(header)

        chunk_index = 0
        while True:
            # Read chunk
            plaintext_chunk = fin.read(chunk_bytes)
            if not plaintext_chunk:
                break

            pt_len = len(plaintext_chunk)
            plaintext_size += pt_len

            # Derive nonce
            nonce = _derive_nonce(nonce_prefix, chunk_index)

            # Build AAD
            aad = _build_aad(
                MAGIC,
                VERSION,
                ALGO_AES_GCM_CHUNKED,
                chunk_bytes,
                nonce_prefix,
                chunk_index,
                pt_len
            )

            # Encrypt (returns ciphertext with appended tag)
            ciphertext_with_tag = aesgcm.encrypt(nonce, plaintext_chunk, aad)

            # Write record: pt_len (uint32) + ciphertext_with_tag
            record_header = struct.pack(">I", pt_len)
            fout.write(record_header)
            fout.write(ciphertext_with_tag)

            ciphertext_hasher.update(record_header)
            ciphertext_hasher.update(ciphertext_with_tag)

            chunk_index += 1

    ciphertext_sha256 = ciphertext_hasher.hexdigest()
    return ciphertext_sha256, plaintext_size


def write_manifest(
    manifest_path: Path,
    asset_id: str,
    weights_filename: str,
    chunk_bytes: int,
    plaintext_bytes: int,
    sha256_ciphertext: str
) -> None:
    """
    Write a tbenc/v1 manifest JSON file.

    Args:
        manifest_path: Path to write manifest
        asset_id: Asset identifier
        weights_filename: Name of the encrypted weights file
        chunk_bytes: Chunk size used for encryption
        plaintext_bytes: Original plaintext size
        sha256_ciphertext: SHA256 hash of the ciphertext file
    """
    manifest = {
        "format": "tbenc/v1",
        "algo": "aes-256-gcm-chunked",
        "chunk_bytes": chunk_bytes,
        "plaintext_bytes": plaintext_bytes,
        "sha256_ciphertext": sha256_ciphertext,
        "asset_id": asset_id,
        "weights_filename": weights_filename
    }

    with open(manifest_path, "w") as f:
        json.dump(manifest, f, indent=2)


def encrypt_and_generate_manifest(
    input_path: Path,
    output_dir: Path,
    asset_id: str,
    chunk_bytes: int = DEFAULT_CHUNK_SIZE,
    output_filename: str = "model.tbenc"
) -> Tuple[bytes, Path, Path]:
    """
    Convenience function to encrypt and generate manifest in one call.

    Args:
        input_path: Path to plaintext weights
        output_dir: Directory to write encrypted file and manifest
        asset_id: Asset identifier
        chunk_bytes: Chunk size for encryption
        output_filename: Name for encrypted output file

    Returns:
        Tuple of (encryption_key, encrypted_file_path, manifest_path)
    """
    output_dir.mkdir(parents=True, exist_ok=True)

    key = generate_key()
    encrypted_path = output_dir / output_filename
    manifest_path = output_dir / f"{output_filename.rsplit('.', 1)[0]}.manifest.json"

    ciphertext_sha256, plaintext_size = encrypt_file(
        input_path,
        encrypted_path,
        key,
        chunk_bytes
    )

    write_manifest(
        manifest_path,
        asset_id,
        output_filename,
        chunk_bytes,
        plaintext_size,
        ciphertext_sha256
    )

    return key, encrypted_path, manifest_path
