"""
Rich console helpers for beautiful CLI output.

Provides consistent, user-friendly console output using the Rich library:
- Success/error/warning/info messages with appropriate styling
- Progress indicators for long-running operations
- Formatted tables and panels
"""

from contextlib import contextmanager
from typing import Optional

from rich.console import Console
from rich.progress import Progress, SpinnerColumn, TextColumn
from rich.panel import Panel
from rich.table import Table

# Global console instance
console = Console()


def success(message: str) -> None:
    """Display success message in green with checkmark.

    Args:
        message: Success message to display

    Example:
        success("File encrypted successfully")
    """
    console.print(f"✓ {message}", style="bold green")


def error(message: str, details: Optional[str] = None) -> None:
    """Display error message in red with X mark.

    Args:
        message: Primary error message
        details: Optional detailed error information

    Example:
        error("Upload failed", details="Connection timeout after 30s")
    """
    console.print(f"✗ {message}", style="bold red")
    if details:
        console.print(f"  Details: {details}", style="dim red")


def warning(message: str) -> None:
    """Display warning message in yellow with warning symbol.

    Args:
        message: Warning message to display

    Example:
        warning("Chunk size is very large, this may impact performance")
    """
    console.print(f"⚠ {message}", style="bold yellow")


def info(message: str) -> None:
    """Display informational message in blue with info symbol.

    Args:
        message: Information message to display

    Example:
        info("Using default chunk size: 4MB")
    """
    console.print(f"ℹ {message}", style="bold blue")


@contextmanager
def progress(description: str):
    """Context manager for displaying progress spinner.

    Args:
        description: Description of the ongoing operation

    Yields:
        Progress task that can be updated

    Example:
        with progress("Encrypting file..."):
            encrypt_file(...)
    """
    with Progress(
        SpinnerColumn(),
        TextColumn("[progress.description]{task.description}"),
        console=console,
        transient=True,  # Remove spinner after completion
    ) as prog:
        task = prog.add_task(description, total=None)
        yield task


def display_key_warning(key_hex: str) -> None:
    """Display decryption key in a warning panel.

    Args:
        key_hex: Hexadecimal decryption key

    Example:
        display_key_warning("a1b2c3d4...")
    """
    key_panel = Panel(
        f"[bold red]{key_hex}[/bold red]",
        title="⚠️  DECRYPTION KEY - SAVE THIS!",
        border_style="red",
    )
    console.print("\n", key_panel)
    console.print(
        "\n[yellow]This key is required for decryption. Store it securely![/yellow]"
    )


def display_table(title: str, rows: list[tuple[str, str]]) -> None:
    """Display a formatted table with two columns.

    Args:
        title: Table title
        rows: List of (key, value) tuples

    Example:
        display_table("Results", [
            ("File", "model.tbenc"),
            ("Size", "1.5 GB"),
        ])
    """
    table = Table(title=title, show_header=False, show_lines=False)
    table.add_column("Key", style="cyan", no_wrap=True)
    table.add_column("Value", style="green")

    for key, value in rows:
        table.add_row(key, value)

    console.print(table)


def redact_key(key_hex: str) -> str:
    """Redact key for safe logging, showing only first and last 8 chars.

    Args:
        key_hex: Full hexadecimal key

    Returns:
        Redacted key string

    Example:
        redact_key("a1b2c3d4e5f6...9876") -> "a1b2c3d4...76543210"
    """
    if len(key_hex) < 16:
        return "***"
    return f"{key_hex[:8]}...{key_hex[-8:]}"


def redact_url(url: str) -> str:
    """Redact SAS token from URL for safe logging.

    Args:
        url: URL potentially containing SAS token

    Returns:
        URL with sig parameter redacted

    Example:
        redact_url("https://storage.blob.core.windows.net/...?sig=abc123")
        -> "https://storage.blob.core.windows.net/...?sig=***"
    """
    from urllib.parse import urlparse, parse_qs

    parsed = urlparse(url)
    query_params = parse_qs(parsed.query)

    if "sig" in query_params or "se" in query_params:
        # URL contains SAS parameters
        return f"{parsed.scheme}://{parsed.netloc}{parsed.path}?<SAS_REDACTED>"

    return url
