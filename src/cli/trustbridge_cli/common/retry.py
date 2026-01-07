"""
Retry logic with exponential backoff for resilient network operations.

Provides decorators for automatically retrying operations that may fail
due to transient errors (network issues, rate limiting, etc.).
"""

import time
from functools import wraps
from typing import Callable, Tuple, Type, TypeVar, cast

from .errors import NetworkError

# Type variable for generic function signatures
F = TypeVar("F", bound=Callable)


def retry_with_backoff(
    max_attempts: int = 3,
    initial_delay: float = 1.0,
    backoff_factor: float = 2.0,
    max_delay: float = 60.0,
    retryable_exceptions: Tuple[Type[Exception], ...] = (NetworkError,),
) -> Callable[[F], F]:
    """
    Decorator for retrying functions with exponential backoff.

    This decorator will retry a function if it raises any of the specified
    exceptions, with increasing delays between attempts.

    Args:
        max_attempts: Maximum number of retry attempts (default: 3)
        initial_delay: Initial delay in seconds before first retry (default: 1.0)
        backoff_factor: Exponential multiplier for delays (default: 2.0)
        max_delay: Maximum delay cap in seconds (default: 60.0)
        retryable_exceptions: Tuple of exception types that trigger retry

    Returns:
        Decorated function with retry logic

    Example:
        @retry_with_backoff(max_attempts=5, initial_delay=2.0)
        def upload_file(path):
            # This will retry up to 5 times on NetworkError
            response = requests.post(url, files={'file': open(path, 'rb')})
            if response.status_code >= 500:
                raise NetworkError("Server error")
            return response

    Raises:
        The last exception if all retry attempts are exhausted
    """

    def decorator(func: F) -> F:
        @wraps(func)
        def wrapper(*args, **kwargs):
            delay = initial_delay
            last_exception = None

            for attempt in range(1, max_attempts + 1):
                try:
                    return func(*args, **kwargs)
                except retryable_exceptions as e:
                    last_exception = e

                    # If this was the last attempt, re-raise the exception
                    if attempt == max_attempts:
                        raise

                    # Show retry message
                    from .console import warning

                    warning(
                        f"Attempt {attempt}/{max_attempts} failed: {e}. "
                        f"Retrying in {delay:.1f}s..."
                    )

                    # Sleep before next attempt
                    time.sleep(delay)

                    # Increase delay for next attempt, capped at max_delay
                    delay = min(delay * backoff_factor, max_delay)

            # This should never be reached, but just in case
            if last_exception:
                raise last_exception

        return cast(F, wrapper)

    return decorator


def retry_on_status_codes(
    status_codes: Tuple[int, ...] = (500, 502, 503, 504),
    max_attempts: int = 3,
    initial_delay: float = 1.0,
    backoff_factor: float = 2.0,
) -> Callable[[F], F]:
    """
    Decorator for retrying HTTP operations based on status codes.

    Useful for retrying requests that fail with specific HTTP status codes
    (typically server errors that may be transient).

    Args:
        status_codes: HTTP status codes that trigger retry (default: 5xx errors)
        max_attempts: Maximum number of retry attempts
        initial_delay: Initial delay in seconds
        backoff_factor: Exponential multiplier for delays

    Returns:
        Decorated function with retry logic

    Example:
        @retry_on_status_codes(status_codes=(429, 500, 502, 503))
        def call_api(endpoint):
            response = requests.get(endpoint)
            return response

    Note:
        This decorator expects the wrapped function to return an object
        with a 'status_code' attribute (like requests.Response).
    """

    def decorator(func: F) -> F:
        @wraps(func)
        def wrapper(*args, **kwargs):
            delay = initial_delay
            last_response = None

            for attempt in range(1, max_attempts + 1):
                response = func(*args, **kwargs)
                last_response = response

                # Check if status code is in the retryable list
                if hasattr(response, "status_code"):
                    if response.status_code not in status_codes:
                        # Success or non-retryable error
                        return response

                    # If this was the last attempt, return the response
                    if attempt == max_attempts:
                        return response

                    # Show retry message
                    from .console import warning

                    warning(
                        f"HTTP {response.status_code} on attempt {attempt}/{max_attempts}. "
                        f"Retrying in {delay:.1f}s..."
                    )

                    # Sleep before next attempt
                    time.sleep(delay)
                    delay = min(delay * backoff_factor, 60.0)
                else:
                    # Response doesn't have status_code, return as-is
                    return response

            return last_response

        return cast(F, wrapper)

    return decorator
