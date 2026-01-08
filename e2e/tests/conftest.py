"""
Pytest fixtures for E2E testing.

This module provides fixtures for:
- Docker Compose management (startup/shutdown)
- Health check waiting utilities
- Service URL configuration
- Test data management
"""

import os
import subprocess
import time
from pathlib import Path
from typing import Generator

import pytest
import requests


# Register pytest markers
def pytest_configure(config):
    config.addinivalue_line(
        "markers", "isolated: marks tests that require exclusive port access (these run after main tests)"
    )


def pytest_collection_modifyitems(session, config, items):
    """
    Reorder tests so that isolated tests run last.
    
    This ensures:
    1. Main tests run first with shared Docker containers
    2. Isolated tests run after main containers are torn down
    """
    # Separate isolated and non-isolated tests
    isolated_tests = []
    main_tests = []
    
    for item in items:
        if item.get_closest_marker('isolated'):
            isolated_tests.append(item)
        else:
            main_tests.append(item)
    
    # Reorder: main tests first, then isolated tests
    items[:] = main_tests + isolated_tests


# Directory containing docker-compose.yml
E2E_DIR = Path(__file__).parent.parent
COMPOSE_FILE = E2E_DIR / "docker-compose.yml"
ENV_FILE = E2E_DIR / ".env"
ARTIFACTS_DIR = E2E_DIR / "artifacts"
DATA_DIR = E2E_DIR / "data"

# Service URLs (from host perspective)
SENTINEL_URL = "http://localhost:8000"
SENTINEL_HEALTH_URL = "http://localhost:8001"
BLOB_SERVER_URL = "http://localhost:9000"
CONTROLPLANE_URL = "http://localhost:8080"


def wait_for_health(url: str, timeout: int = 120, interval: float = 2.0) -> bool:
    """
    Wait for a health endpoint to return 200.

    Args:
        url: Health endpoint URL
        timeout: Maximum time to wait in seconds
        interval: Time between checks in seconds

    Returns:
        True if healthy, False if timeout
    """
    start_time = time.time()
    while time.time() - start_time < timeout:
        try:
            response = requests.get(url, timeout=5)
            if response.status_code == 200:
                return True
        except requests.exceptions.RequestException:
            pass
        time.sleep(interval)
    return False


def docker_compose_up(env: dict | None = None, project_name: str | None = None) -> bool:
    """
    Start docker compose services.

    Args:
        env: Additional environment variables
        project_name: Optional project name for isolation (default: "e2e")

    Returns:
        True if successful
    """
    cmd_env = os.environ.copy()
    if env:
        cmd_env.update(env)

    project = project_name or "e2e"
    result = subprocess.run(
        ["docker", "compose", "-f", str(COMPOSE_FILE), "-p", project, "up", "--build", "-d"],
        cwd=str(E2E_DIR),
        env=cmd_env,
        capture_output=True,
        text=True,
    )

    if result.returncode != 0:
        print(f"docker compose up failed:\n{result.stderr}")
        return False
    return True


def docker_compose_down(project_name: str | None = None) -> bool:
    """
    Stop and remove docker compose services.

    Args:
        project_name: Optional project name for isolation (default: "e2e")

    Returns:
        True if successful
    """
    project = project_name or "e2e"
    result = subprocess.run(
        ["docker", "compose", "-f", str(COMPOSE_FILE), "-p", project, "down", "-v"],
        cwd=str(E2E_DIR),
        capture_output=True,
        text=True,
    )
    return result.returncode == 0


def get_container_logs(service_name: str, project_name: str | None = None) -> str:
    """Get logs from a specific service container."""
    project = project_name or "e2e"
    result = subprocess.run(
        ["docker", "compose", "-f", str(COMPOSE_FILE), "-p", project, "logs", service_name],
        cwd=str(E2E_DIR),
        capture_output=True,
        text=True,
    )
    return result.stdout


def exec_in_container(service_name: str, command: list[str], project_name: str | None = None) -> tuple[int, str, str]:
    """
    Execute a command in a running container.

    Returns:
        Tuple of (return_code, stdout, stderr)
    """
    project = project_name or "e2e"
    result = subprocess.run(
        ["docker", "compose", "-f", str(COMPOSE_FILE), "-p", project, "exec", "-T", service_name] + command,
        cwd=str(E2E_DIR),
        capture_output=True,
        text=True,
    )
    return result.returncode, result.stdout, result.stderr


@pytest.fixture(scope="session")
def e2e_env() -> dict:
    """
    Load E2E environment variables from .env file.

    Returns:
        Dictionary of environment variables
    """
    env = {}
    if ENV_FILE.exists():
        with open(ENV_FILE) as f:
            for line in f:
                line = line.strip()
                if line and not line.startswith("#") and "=" in line:
                    key, value = line.split("=", 1)
                    env[key.strip()] = value.strip()
    return env


@pytest.fixture(scope="session")
def plaintext_sha256() -> str | None:
    """
    Get the SHA256 hash of the original plaintext file.

    This is computed during test data generation and stored
    for verification after decryption.
    """
    # Check if we have a cached hash from the generation step
    hash_file = DATA_DIR / "plain.weights.sha256"
    if hash_file.exists():
        return hash_file.read_text().strip()

    # Compute hash if file exists
    plaintext_file = DATA_DIR / "plain.weights"
    if plaintext_file.exists():
        import hashlib
        with open(plaintext_file, "rb") as f:
            return hashlib.sha256(f.read()).hexdigest()

    return None


# Track if main services are running (for isolated tests to know)
_main_services_running = False


@pytest.fixture(scope="session")
def e2e_services(e2e_env: dict) -> Generator[dict, None, None]:
    """
    Start E2E services and wait for them to be healthy.

    This fixture:
    1. Starts docker compose
    2. Waits for all health endpoints
    3. Yields service URLs
    4. Tears down on completion

    Yields:
        Dictionary with service URLs
    """
    global _main_services_running
    # Start services
    assert docker_compose_up(e2e_env), "Failed to start docker compose"
    _main_services_running = True

    try:
        # Wait for infrastructure services first
        print("Waiting for blob-server...")
        assert wait_for_health(f"{BLOB_SERVER_URL}/health", timeout=30), \
            "blob-server health check failed"

        print("Waiting for controlplane...")
        assert wait_for_health(f"{CONTROLPLANE_URL}/health", timeout=30), \
            "controlplane health check failed"

        # Wait for sentinel (may take longer due to hydration)
        print("Waiting for sentinel...")
        sentinel_healthy = wait_for_health(f"{SENTINEL_HEALTH_URL}/health", timeout=180)

        yield {
            "sentinel": SENTINEL_URL,
            "sentinel_health": SENTINEL_HEALTH_URL,
            "blob_server": BLOB_SERVER_URL,
            "controlplane": CONTROLPLANE_URL,
            "sentinel_healthy": sentinel_healthy,
        }

    finally:
        # Always tear down
        print("Stopping E2E services...")
        docker_compose_down()
        _main_services_running = False


def stop_main_services_if_running():
    """
    Stop main E2E services if they are running.
    
    This is called by isolated tests before they start their own Docker containers.
    """
    global _main_services_running
    if _main_services_running:
        print("Stopping main E2E services for isolated test...")
        docker_compose_down()
        _main_services_running = False


@pytest.fixture
def sentinel_logs() -> Generator[callable, None, None]:
    """Fixture to get sentinel logs on demand."""
    def get_logs():
        return get_container_logs("sentinel")
    yield get_logs
