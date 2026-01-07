"""
Custom exception hierarchy for TrustBridge CLI.

All TrustBridge-specific errors inherit from TrustBridgeError,
allowing for structured error handling and user-friendly error messages.
"""

from typing import Optional


class TrustBridgeError(Exception):
    """Base exception for all TrustBridge errors.

    Args:
        message: Primary error message describing what went wrong
        details: Optional additional details to help debug the issue
    """

    def __init__(self, message: str, details: Optional[str] = None):
        self.message = message
        self.details = details
        super().__init__(message)

    def __str__(self) -> str:
        if self.details:
            return f"{self.message}\n  Details: {self.details}"
        return self.message


class ValidationError(TrustBridgeError):
    """Input validation failed.

    Raised when user-provided inputs don't meet requirements
    (e.g., invalid asset ID format, chunk size out of range).
    """
    pass


class CryptoError(TrustBridgeError):
    """Encryption or decryption operation failed.

    Raised when cryptographic operations encounter errors
    (e.g., key generation failure, encryption error).
    """
    pass


class AzureError(TrustBridgeError):
    """Azure API operation failed.

    Raised when interactions with Azure services fail
    (e.g., authentication error, blob upload failure, container not found).
    """
    pass


class ControlPlaneError(TrustBridgeError):
    """Control Plane API operation failed.

    Raised when interactions with the Control Plane / EDC fail
    (e.g., asset registration error, authorization failure).
    """
    pass


class DockerError(TrustBridgeError):
    """Docker operation failed.

    Raised when Docker build or push operations fail
    (e.g., daemon not running, build error, push authentication failure).
    """
    pass


class NetworkError(TrustBridgeError):
    """Network operation failed.

    Raised for transient network errors that may be retryable
    (e.g., connection timeout, temporary DNS failure).
    """
    pass
