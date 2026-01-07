"""
TrustBridge CLI - Main entry point.

This module provides the main Typer application that orchestrates
all TrustBridge provider commands for secure model distribution.
"""

import sys
from pathlib import Path

import typer
from typing_extensions import Annotated

from . import __version__
from .common import console, error

# Create main Typer app
app = typer.Typer(
    name="trustbridge",
    help="TrustBridge Provider CLI - Secure model weight distribution for Azure Marketplace",
    add_completion=False,
    pretty_exceptions_show_locals=False,  # Security: don't leak variable values
    rich_markup_mode="rich",
)


def version_callback(value: bool):
    """Display version information."""
    if value:
        console.console.print(f"TrustBridge CLI v{__version__}")
        raise typer.Exit()


@app.callback()
def main(
    version: Annotated[
        bool,
        typer.Option(
            "--version",
            "-v",
            callback=version_callback,
            is_eager=True,
            help="Show version and exit",
        ),
    ] = False,
):
    """
    TrustBridge Provider CLI.

    Secure model weight distribution platform for Azure Marketplace.

    This CLI enables providers to:
    1. Encrypt model weights with tbenc/v1 format
    2. Upload encrypted assets to Azure Blob Storage
    3. Register assets with Control Plane / EDC
    4. Build Docker images with sentinel and runtime
    5. Package Azure Managed Applications
    6. Orchestrate the full publish workflow

    For detailed help on any command, run:
        trustbridge COMMAND --help

    Examples:
        # Encrypt model weights
        trustbridge encrypt model.safetensors --asset-id my-model-v1

        # Full publish workflow
        trustbridge publish model.safetensors --asset-id my-model-v1 \\
            --storage-account myaccount --registry myregistry.azurecr.io

    Documentation: https://github.com/trustbridge/trustbridge
    """
    pass


# Import and register commands
# Note: Commands are imported here to avoid circular imports
# and will be registered as they're implemented

def register_commands():
    """Register all CLI commands with the main app."""
    try:
        from .commands import encrypt
        app.command(name="encrypt")(encrypt.command)
    except ImportError:
        pass  # Command not yet implemented

    try:
        from .commands import blob
        app.command(name="upload")(blob.command)
    except ImportError:
        pass  # Command not yet implemented

    try:
        from .commands import edc
        app.command(name="register")(edc.command)
    except ImportError:
        pass  # Command not yet implemented

    try:
        from .commands import build
        app.command(name="build")(build.command)
    except ImportError:
        pass  # Command not yet implemented

    try:
        from .commands import package
        app.command(name="package")(package.command)
    except ImportError:
        pass  # Command not yet implemented

    try:
        from .commands import publish
        app.command(name="publish")(publish.command)
    except ImportError:
        pass  # Command not yet implemented


# Register commands when module is imported
register_commands()


def cli_main():
    """
    Main entry point for the CLI.

    This function is called when the CLI is invoked from the command line.
    It handles global error catching and provides user-friendly error messages.
    """
    try:
        app()
    except KeyboardInterrupt:
        console.warning("\nOperation cancelled by user")
        sys.exit(130)  # Standard exit code for SIGINT
    except Exception as e:
        # Catch any unhandled exceptions and display nicely
        from .common.errors import TrustBridgeError

        if isinstance(e, TrustBridgeError):
            error(e.message, details=e.details)
        else:
            error(f"Unexpected error: {type(e).__name__}", details=str(e))
        sys.exit(1)


if __name__ == "__main__":
    cli_main()
