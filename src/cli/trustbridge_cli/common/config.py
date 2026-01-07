"""
Configuration management for TrustBridge CLI.

Provides a unified configuration system with the following priority:
1. CLI arguments (highest priority)
2. Environment variables
3. Configuration file (~/.trustbridge/config.toml)
4. Defaults (lowest priority)

Configuration files use TOML format and never store credentials.
"""

import os
from pathlib import Path
from typing import Any, Optional

try:
    import tomli as toml  # Python < 3.11
except ImportError:
    try:
        import tomllib as toml  # Python >= 3.11
    except ImportError:
        toml = None  # type: ignore

from .errors import ValidationError


class Config:
    """Global configuration manager with multi-source loading."""

    def __init__(self, config_path: Optional[Path] = None):
        """
        Initialize configuration manager.

        Args:
            config_path: Optional custom config file path.
                        Defaults to ~/.trustbridge/config.toml
        """
        if config_path:
            self.config_file = config_path
        else:
            self.config_file = Path.home() / ".trustbridge" / "config.toml"

        self._config = self._load_config()

    def _load_config(self) -> dict:
        """
        Load configuration from file if it exists.

        Returns:
            Dictionary of configuration values, empty dict if file doesn't exist

        Raises:
            ValidationError: If config file exists but is invalid TOML
        """
        if not self.config_file.exists():
            return {}

        if toml is None:
            from .console import warning

            warning(
                "TOML library not available. Install 'tomli' to use config files. "
                "Falling back to environment variables and CLI args."
            )
            return {}

        try:
            with open(self.config_file, "rb") as f:
                return toml.load(f)
        except Exception as e:
            raise ValidationError(
                f"Failed to load config file: {self.config_file}",
                details=str(e),
            )

    def get(
        self,
        key: str,
        default: Any = None,
        env_var: Optional[str] = None,
        section: Optional[str] = None,
    ) -> Any:
        """
        Get configuration value with fallback chain.

        Priority: env var > config file > default

        Args:
            key: Configuration key name
            default: Default value if not found
            env_var: Environment variable name to check (optional)
            section: TOML section name (e.g., "azure" for [azure] section)

        Returns:
            Configuration value from highest priority source

        Example:
            # Check env var TB_STORAGE_ACCOUNT, then [azure].storage_account in config
            config.get("storage_account", default="myaccount",
                      env_var="TB_STORAGE_ACCOUNT", section="azure")
        """
        # Priority 1: Environment variable
        if env_var and env_var in os.environ:
            return os.environ[env_var]

        # Priority 2: Config file
        if section:
            # Look in specific section
            if section in self._config and key in self._config[section]:
                return self._config[section][key]
        else:
            # Look in root
            if key in self._config:
                return self._config[key]

        # Priority 3: Default
        return default

    def set(self, key: str, value: Any, section: Optional[str] = None) -> None:
        """
        Set configuration value and save to file.

        Args:
            key: Configuration key name
            value: Value to store
            section: TOML section name (optional)

        Raises:
            ValidationError: If unable to write config file

        Example:
            config.set("storage_account", "myaccount", section="azure")
        """
        if section:
            if section not in self._config:
                self._config[section] = {}
            self._config[section][key] = value
        else:
            self._config[key] = value

        self._save_config()

    def _save_config(self) -> None:
        """
        Save configuration to file.

        Creates config directory if it doesn't exist.

        Raises:
            ValidationError: If unable to write config file
        """
        try:
            # Create config directory if needed
            self.config_file.parent.mkdir(parents=True, exist_ok=True)

            # Write config (manual TOML generation to avoid extra dependency)
            with open(self.config_file, "w") as f:
                self._write_toml(f, self._config)

        except Exception as e:
            raise ValidationError(
                f"Failed to save config file: {self.config_file}", details=str(e)
            )

    def _write_toml(self, f, data: dict, section: str = "") -> None:
        """
        Manually write TOML format (simple implementation).

        Args:
            f: File handle
            data: Data dictionary to write
            section: Current section name
        """
        # Write simple key-value pairs first
        for key, value in data.items():
            if not isinstance(value, dict):
                if isinstance(value, str):
                    f.write(f'{key} = "{value}"\n')
                elif isinstance(value, bool):
                    f.write(f"{key} = {str(value).lower()}\n")
                elif isinstance(value, (int, float)):
                    f.write(f"{key} = {value}\n")

        # Write sections
        for key, value in data.items():
            if isinstance(value, dict):
                section_name = f"{section}.{key}" if section else key
                f.write(f"\n[{section_name}]\n")
                self._write_toml(f, value, section_name)

    def get_azure_config(self) -> dict:
        """
        Get all Azure-related configuration.

        Returns:
            Dictionary with Azure settings (storage_account, container, registry, etc.)
        """
        return {
            "storage_account": self.get(
                "storage_account", env_var="AZURE_STORAGE_ACCOUNT", section="azure"
            ),
            "container": self.get(
                "container", default="models", env_var="AZURE_STORAGE_CONTAINER", section="azure"
            ),
            "registry": self.get(
                "registry", env_var="AZURE_CONTAINER_REGISTRY", section="azure"
            ),
        }

    def get_controlplane_config(self) -> dict:
        """
        Get Control Plane configuration.

        Returns:
            Dictionary with Control Plane settings (endpoint, etc.)
        """
        return {
            "endpoint": self.get(
                "endpoint",
                default="https://controlplane.trustbridge.io",
                env_var="TB_EDC_ENDPOINT",
                section="controlplane",
            ),
        }

    def get_docker_config(self) -> dict:
        """
        Get Docker build configuration.

        Returns:
            Dictionary with Docker settings (image_name, base_image, etc.)
        """
        return {
            "image_name": self.get(
                "image_name",
                default="trustbridge-runtime",
                env_var="DOCKER_IMAGE_NAME",
                section="docker",
            ),
            "base_image": self.get(
                "base_image",
                default="nvcr.io/nvidia/vllm:latest",
                env_var="DOCKER_BASE_IMAGE",
                section="docker",
            ),
        }

    def get_encryption_config(self) -> dict:
        """
        Get encryption configuration.

        Returns:
            Dictionary with encryption settings (default_chunk_bytes, etc.)
        """
        return {
            "default_chunk_bytes": self.get(
                "default_chunk_bytes",
                default=4194304,  # 4MB
                env_var="TB_CHUNK_BYTES",
                section="encryption",
            ),
        }


# Global config instance
config = Config()
