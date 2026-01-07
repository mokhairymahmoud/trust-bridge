"""
Common utilities for TrustBridge CLI.

This package provides shared functionality used across all CLI commands:
- Custom exception hierarchy
- Rich console helpers for formatted output
- Retry logic with exponential backoff
- Input validation utilities
- Configuration management
"""

from .errors import (
    TrustBridgeError,
    ValidationError,
    CryptoError,
    AzureError,
    ControlPlaneError,
    DockerError,
    NetworkError,
)
from .console import console, success, error, warning, info, progress
from .retry import retry_with_backoff
from .validation import (
    validate_asset_id,
    validate_chunk_size,
    validate_path,
    validate_file_exists,
    validate_disk_space,
)

__all__ = [
    # Exceptions
    "TrustBridgeError",
    "ValidationError",
    "CryptoError",
    "AzureError",
    "ControlPlaneError",
    "DockerError",
    "NetworkError",
    # Console helpers
    "console",
    "success",
    "error",
    "warning",
    "info",
    "progress",
    # Retry
    "retry_with_backoff",
    # Validation
    "validate_asset_id",
    "validate_chunk_size",
    "validate_path",
    "validate_file_exists",
    "validate_disk_space",
]
