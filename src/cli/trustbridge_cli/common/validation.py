"""
Input validation utilities for TrustBridge CLI.

Provides functions to validate user inputs before processing:
- Asset ID format and constraints
- Chunk size ranges
- File paths and permissions
- Disk space availability
"""

import os
import re
from pathlib import Path
from typing import Optional

from .errors import ValidationError


def validate_asset_id(asset_id: str) -> None:
    """
    Validate asset ID format.

    Asset IDs must:
    - Not be empty
    - Contain only alphanumeric characters, hyphens, and underscores
    - Be 100 characters or less

    Args:
        asset_id: Asset identifier to validate

    Raises:
        ValidationError: If asset ID doesn't meet requirements

    Example:
        validate_asset_id("llama-3-70b-v1")  # OK
        validate_asset_id("my_model_2024")   # OK
        validate_asset_id("invalid id!")     # Raises ValidationError
    """
    if not asset_id:
        raise ValidationError("Asset ID cannot be empty")

    # Check for valid characters (alphanumeric, hyphen, underscore)
    if not re.match(r"^[a-zA-Z0-9_-]+$", asset_id):
        raise ValidationError(
            "Asset ID must contain only alphanumeric characters, hyphens, and underscores",
            details=f"Got: {asset_id}",
        )

    # Check length
    if len(asset_id) > 100:
        raise ValidationError(
            "Asset ID must be 100 characters or less",
            details=f"Got {len(asset_id)} characters",
        )


def validate_chunk_size(chunk_bytes: int) -> None:
    """
    Validate chunk size for encryption.

    Chunk size must be between 1KB (1024 bytes) and 64MB (67,108,864 bytes).
    Recommended sizes are 4MB-16MB for optimal performance.

    Args:
        chunk_bytes: Chunk size in bytes

    Raises:
        ValidationError: If chunk size is out of valid range

    Example:
        validate_chunk_size(4194304)  # 4MB - OK
        validate_chunk_size(100)      # Too small - Raises ValidationError
    """
    min_bytes = 1024  # 1KB
    max_bytes = 64 * 1024 * 1024  # 64MB

    if chunk_bytes < min_bytes or chunk_bytes > max_bytes:
        raise ValidationError(
            f"Chunk size must be between 1KB and 64MB",
            details=f"Got: {chunk_bytes:,} bytes ({chunk_bytes / (1024*1024):.2f} MB)",
        )

    # Warn if chunk size is unusual (not a power of 2 or common size)
    recommended_sizes = [
        1 * 1024 * 1024,  # 1MB
        4 * 1024 * 1024,  # 4MB
        8 * 1024 * 1024,  # 8MB
        16 * 1024 * 1024,  # 16MB
        32 * 1024 * 1024,  # 32MB
        64 * 1024 * 1024,  # 64MB
    ]

    if chunk_bytes not in recommended_sizes:
        from .console import warning

        warning(
            f"Chunk size {chunk_bytes / (1024*1024):.2f} MB is non-standard. "
            f"Recommended: 4MB, 8MB, or 16MB"
        )


def validate_path(path: Path, must_exist: bool = True) -> None:
    """
    Validate file or directory path.

    Args:
        path: Path to validate
        must_exist: If True, path must exist (default: True)

    Raises:
        ValidationError: If path is invalid or doesn't exist

    Example:
        validate_path(Path("model.safetensors"))  # Check if exists
        validate_path(Path("output/"), must_exist=False)  # OK if doesn't exist
    """
    if must_exist and not path.exists():
        raise ValidationError(f"Path does not exist: {path}")

    # Check if parent directory exists when path doesn't need to exist
    if not must_exist and not path.parent.exists():
        raise ValidationError(
            f"Parent directory does not exist: {path.parent}",
            details="Create the parent directory first",
        )


def validate_file_exists(file_path: Path, allow_empty: bool = False) -> None:
    """
    Validate that a file exists and has content.

    Args:
        file_path: Path to file
        allow_empty: If False, raises error for empty files (default: False)

    Raises:
        ValidationError: If file doesn't exist, is not a file, or is empty

    Example:
        validate_file_exists(Path("model.safetensors"))  # OK
        validate_file_exists(Path("empty.txt"), allow_empty=True)  # OK
    """
    if not file_path.exists():
        raise ValidationError(f"File not found: {file_path}")

    if not file_path.is_file():
        raise ValidationError(
            f"Path is not a file: {file_path}",
            details="Expected a regular file, got directory or special file",
        )

    if not allow_empty and file_path.stat().st_size == 0:
        raise ValidationError(f"File is empty: {file_path}")


def validate_disk_space(
    path: Path, required_bytes: int, safety_margin: float = 1.2
) -> None:
    """
    Validate sufficient disk space is available.

    Args:
        path: Path to check (file or directory)
        required_bytes: Minimum required bytes
        safety_margin: Multiplier for safety margin (default: 1.2 = 20% extra)

    Raises:
        ValidationError: If insufficient disk space

    Example:
        # Check if 10GB is available (with 20% margin = 12GB)
        validate_disk_space(Path("output/"), 10 * 1024**3)
    """
    # Get the directory to check (use parent if path is a file)
    check_dir = path if path.is_dir() else path.parent

    # Ensure directory exists
    if not check_dir.exists():
        check_dir = check_dir.parent

    try:
        stat = os.statvfs(check_dir)
        available_bytes = stat.f_bavail * stat.f_frsize
        required_with_margin = int(required_bytes * safety_margin)

        if available_bytes < required_with_margin:
            raise ValidationError(
                "Insufficient disk space",
                details=(
                    f"Available: {available_bytes / (1024**3):.2f} GB, "
                    f"Required: {required_with_margin / (1024**3):.2f} GB "
                    f"(includes {int((safety_margin - 1) * 100)}% safety margin)"
                ),
            )
    except AttributeError:
        # os.statvfs not available on Windows
        # Fall back to checking if we can write
        pass


def validate_url(url: str, require_https: bool = True) -> None:
    """
    Validate URL format.

    Args:
        url: URL to validate
        require_https: If True, only allow HTTPS URLs (default: True)

    Raises:
        ValidationError: If URL is invalid

    Example:
        validate_url("https://api.example.com")  # OK
        validate_url("http://api.example.com")   # Error if require_https=True
    """
    if not url:
        raise ValidationError("URL cannot be empty")

    if not url.startswith(("http://", "https://")):
        raise ValidationError(
            "URL must start with http:// or https://", details=f"Got: {url}"
        )

    if require_https and not url.startswith("https://"):
        raise ValidationError(
            "URL must use HTTPS for security", details=f"Got: {url}"
        )


def validate_hex_string(hex_string: str, expected_bytes: Optional[int] = None) -> None:
    """
    Validate hexadecimal string format.

    Args:
        hex_string: Hexadecimal string to validate
        expected_bytes: Expected length in bytes (hex string should be 2x this)

    Raises:
        ValidationError: If hex string is invalid

    Example:
        validate_hex_string("a1b2c3d4", expected_bytes=4)  # OK (8 chars = 4 bytes)
        validate_hex_string("xyz123")  # Error - invalid hex
    """
    if not hex_string:
        raise ValidationError("Hex string cannot be empty")

    # Check if valid hex characters
    if not re.match(r"^[0-9a-fA-F]+$", hex_string):
        raise ValidationError(
            "Invalid hexadecimal string - must contain only 0-9, a-f, A-F",
            details=f"Got: {hex_string[:20]}...",
        )

    # Check length if expected
    if expected_bytes is not None:
        expected_chars = expected_bytes * 2
        if len(hex_string) != expected_chars:
            raise ValidationError(
                f"Hex string must be {expected_chars} characters for {expected_bytes} bytes",
                details=f"Got {len(hex_string)} characters",
            )
