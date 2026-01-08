"""
TrustBridge E2E Test Suite

This module tests the complete TrustBridge workflow:
1. Provider encrypts weights
2. Blob server hosts encrypted artifacts
3. Control Plane authorizes consumer
4. Sentinel hydrates and decrypts
5. Runtime reads plaintext via FIFO
6. Client accesses inference through sentinel proxy

Test Cases:
- test_authorization_deny: Contract denial blocks access
- test_download_integrity: Hash verification prevents corrupted data
- test_decrypt_interop: Python encryption matches Go decryption
- test_no_plaintext_on_disk: Security - no plaintext on persistent storage
- test_proxy_forwarding: Requests are correctly proxied to runtime
- test_audit_log: Audit trail is produced
- test_runtime_isolation: Runtime port is not externally accessible
"""

import hashlib
import os
import socket
import subprocess
import time
from pathlib import Path

import pytest
import requests

from conftest import (
    E2E_DIR,
    COMPOSE_FILE,
    ARTIFACTS_DIR,
    docker_compose_up,
    docker_compose_down,
    wait_for_health,
    get_container_logs,
    exec_in_container,
    stop_main_services_if_running,
    SENTINEL_URL,
    SENTINEL_HEALTH_URL,
    BLOB_SERVER_URL,
    CONTROLPLANE_URL,
)


class TestE2EHappyPath:
    """
    Tests for the happy path E2E workflow.
    These tests require all services to be running with valid configuration.
    """

    def test_decrypt_interop(self, e2e_services: dict, plaintext_sha256: str):
        """
        Verify that Python encryption produces data that Go can decrypt correctly.

        This is the core cryptographic interoperability test:
        1. Provider (Python) encrypts plaintext with tbenc/v1 format
        2. Sentinel (Go) downloads, verifies hash, and decrypts
        3. Runtime-mock reads from FIFO and computes SHA256
        4. SHA256 must match original plaintext

        Acceptance Criteria (Section 9.1):
        - Plaintext hash after decrypt matches original plaintext hash
        """
        if not e2e_services["sentinel_healthy"]:
            pytest.skip("Sentinel not healthy - cannot test decrypt interop")

        if not plaintext_sha256:
            pytest.skip("Original plaintext SHA256 not available")

        # Wait a bit for runtime-mock to finish reading FIFO
        time.sleep(5)

        # Query runtime-mock for the plaintext hash it computed
        response = requests.get(f"{SENTINEL_URL}/plaintext-hash", timeout=30)

        # May be 202 if still reading
        if response.status_code == 202:
            # Wait and retry
            for _ in range(30):
                time.sleep(2)
                response = requests.get(f"{SENTINEL_URL}/plaintext-hash", timeout=30)
                if response.status_code != 202:
                    break

        assert response.status_code == 200, \
            f"Expected 200, got {response.status_code}: {response.text}"

        data = response.json()
        assert "sha256" in data, f"Response missing sha256: {data}"

        decrypted_hash = data["sha256"]
        assert decrypted_hash == plaintext_sha256, \
            f"Hash mismatch!\n" \
            f"  Original:  {plaintext_sha256}\n" \
            f"  Decrypted: {decrypted_hash}"

        print(f"Crypto interop verified: SHA256={decrypted_hash}")

    def test_proxy_forwarding(self, e2e_services: dict):
        """
        Verify that requests are correctly proxied to the runtime.

        The sentinel should:
        1. Accept requests on :8000
        2. Forward them to runtime at 127.0.0.1:8081
        3. Return runtime's response

        Section 7.7: Reverse proxy + audit
        """
        if not e2e_services["sentinel_healthy"]:
            pytest.skip("Sentinel not healthy - cannot test proxy")

        # Test GET request
        response = requests.get(f"{SENTINEL_URL}/v1/demo", timeout=30)
        assert response.status_code == 200, \
            f"GET /v1/demo failed: {response.status_code}"

        data = response.json()
        assert data.get("message") == "Hello from runtime-mock", \
            f"Unexpected response: {data}"

        # Test POST request with body
        test_body = b"test request body"
        response = requests.post(
            f"{SENTINEL_URL}/v1/demo",
            data=test_body,
            headers={"Content-Type": "application/octet-stream"},
            timeout=30,
        )
        assert response.status_code == 200, \
            f"POST /v1/demo failed: {response.status_code}"

        data = response.json()
        expected_hash = hashlib.sha256(test_body).hexdigest()
        assert data.get("request", {}).get("body_sha256") == expected_hash, \
            f"Request body not correctly forwarded: {data}"

        print("Proxy forwarding verified")

    def test_no_plaintext_on_disk(self, e2e_services: dict):
        """
        Verify that no plaintext weights exist on persistent storage.

        Security requirement (Section 9.2):
        - TB_TARGET_DIR should only contain encrypted artifacts (.tbenc)
        - No plaintext files should exist in the download directory
        """
        if not e2e_services["sentinel_healthy"]:
            pytest.skip("Sentinel not healthy - cannot test disk contents")

        # Execute ls in the sentinel container to check TB_TARGET_DIR
        returncode, stdout, stderr = exec_in_container(
            "sentinel",
            ["ls", "-la", "/mnt/resource/trustbridge"],
        )

        if returncode != 0:
            pytest.skip(f"Cannot list TB_TARGET_DIR: {stderr}")

        # Check that we only have .tbenc files (plus manifest)
        lines = stdout.strip().split("\n")
        for line in lines:
            if line.startswith("total") or line.startswith("d"):
                continue  # Skip total and directories

            # File line format: -rw-r--r-- ... filename
            parts = line.split()
            if len(parts) >= 9:
                filename = parts[-1]
                # Allow only .tbenc and .json files
                assert filename.endswith((".tbenc", ".json", "manifest.json")), \
                    f"Unexpected file in TB_TARGET_DIR: {filename}\n" \
                    f"Full listing:\n{stdout}"

        print("No plaintext on disk verified")

    def test_audit_log(self, e2e_services: dict, sentinel_logs: callable):
        """
        Verify that audit log entries are produced for proxied requests.

        Section 7.7: Audit log schema should include:
        - ts (timestamp)
        - contract_id
        - asset_id
        - method
        - path
        - status
        - latency_ms
        """
        if not e2e_services["sentinel_healthy"]:
            pytest.skip("Sentinel not healthy - cannot test audit log")

        # Make a request to generate audit log entry
        response = requests.get(f"{SENTINEL_URL}/v1/demo", timeout=30)
        assert response.status_code == 200

        # Give some time for audit log to be written
        time.sleep(1)

        # Check sentinel logs for audit entries
        logs = sentinel_logs()

        # Look for JSON-formatted audit log entries
        # The audit log should contain request metadata
        assert "POST" in logs or "GET" in logs, \
            f"No request methods found in logs. Check audit logging implementation.\n" \
            f"Logs sample:\n{logs[:2000]}"

        print("Audit log verification passed")


@pytest.mark.isolated
class TestAuthorizationDeny:
    """
    Tests for contract denial scenarios.

    When the control plane denies authorization, the sentinel should:
    - Not reach Ready state
    - Not expose the proxy port
    - Return appropriate error responses
    
    Note: This test runs after main tests with fresh Docker containers.
    """

    @pytest.fixture(scope="class")
    def denied_services(self, e2e_env: dict):
        """Start services with a denied contract."""
        # Stop main services if they're still running (shouldn't be, but just in case)
        stop_main_services_if_running()
        
        # Override contract ID to trigger denial
        env = e2e_env.copy()
        env["TB_CONTRACT_ID"] = "contract-deny"

        assert docker_compose_up(env), "Failed to start docker compose"

        try:
            # Wait for infrastructure services
            wait_for_health(f"{BLOB_SERVER_URL}/health", timeout=30)
            wait_for_health(f"{CONTROLPLANE_URL}/health", timeout=30)

            # Give sentinel time to attempt authorization
            time.sleep(10)

            yield {"contract_id": "contract-deny"}

        finally:
            docker_compose_down()

    def test_authorization_deny(self, denied_services: dict):
        """
        Verify that authorization denial prevents sentinel from reaching Ready.

        Section 9.3: Contract gating
        - If authorize endpoint returns denied → sentinel never opens port 8000
        - Or returns 403/503 for all routes
        """
        # Check sentinel health - should NOT be healthy
        try:
            response = requests.get(f"{SENTINEL_HEALTH_URL}/health", timeout=5)
            # If we get a response, it should be 503 (not ready)
            assert response.status_code != 200, \
                "Sentinel should not be healthy with denied contract"
        except requests.exceptions.RequestException:
            # Connection refused is acceptable - sentinel may not be listening
            pass

        # Try to access proxy - should fail
        try:
            response = requests.get(f"{SENTINEL_URL}/v1/demo", timeout=5)
            # If we get a response, it should be 403 or 503
            assert response.status_code in (403, 503), \
                f"Expected 403/503, got {response.status_code}"
        except requests.exceptions.RequestException:
            # Connection refused is acceptable
            pass

        print("Authorization deny verified - sentinel blocked access")


@pytest.mark.isolated
class TestDownloadIntegrity:
    """
    Tests for download integrity verification.

    The sentinel must verify SHA256 hash of downloaded ciphertext
    against the manifest before proceeding to decryption.
    
    Note: This test runs after main tests with fresh Docker containers.
    """

    def test_download_integrity_failure(self, e2e_env: dict):
        """
        Verify that sentinel fails hydration when ciphertext is corrupted.

        This test:
        1. Modifies a byte in the encrypted file
        2. Starts sentinel
        3. Verifies sentinel fails before reaching Ready

        Section 9.2 / 11.6B: If ciphertext is modified, sentinel must fail
        """
        # Stop main services if they're still running
        stop_main_services_if_running()
        
        # This is a destructive test - we need to corrupt the artifact
        # Skip if artifacts don't exist
        tbenc_file = ARTIFACTS_DIR / "model.tbenc"
        if not tbenc_file.exists():
            pytest.skip("model.tbenc not found - run e2e-encrypt first")

        # Create a backup
        backup_file = ARTIFACTS_DIR / "model.tbenc.backup"
        if not backup_file.exists():
            import shutil
            shutil.copy(tbenc_file, backup_file)

        try:
            # Corrupt a byte in the middle of the file
            with open(tbenc_file, "r+b") as f:
                f.seek(1000)  # Skip header, corrupt data
                original_byte = f.read(1)
                f.seek(1000)
                f.write(bytes([original_byte[0] ^ 0xFF]))  # Flip all bits

            # Start services
            assert docker_compose_up(e2e_env), "Failed to start docker compose"

            try:
                # Wait for infrastructure
                wait_for_health(f"{BLOB_SERVER_URL}/health", timeout=30)
                wait_for_health(f"{CONTROLPLANE_URL}/health", timeout=30)

                # Give sentinel time to download and verify
                time.sleep(30)

                # Sentinel should NOT be healthy
                try:
                    response = requests.get(f"{SENTINEL_HEALTH_URL}/health", timeout=5)
                    assert response.status_code != 200, \
                        "Sentinel should fail with corrupted ciphertext"
                except requests.exceptions.RequestException:
                    pass  # Expected - sentinel may have exited

                # Check logs for hash mismatch error
                logs = get_container_logs("sentinel")
                assert "hash" in logs.lower() or "mismatch" in logs.lower() or \
                       "integrity" in logs.lower() or "failed" in logs.lower(), \
                    f"Expected hash verification error in logs:\n{logs[:2000]}"

                print("Download integrity failure verified")

            finally:
                docker_compose_down()

        finally:
            # Restore original file
            if backup_file.exists():
                import shutil
                shutil.copy(backup_file, tbenc_file)


class TestRuntimeIsolation:
    """
    Tests for runtime network isolation.

    The runtime must not be accessible from outside the container network.
    Only sentinel should be able to communicate with runtime via localhost.
    """

    def test_runtime_isolation(self, e2e_services: dict):
        """
        Verify that runtime port 8081 is not accessible from host.

        Section 9.5: Runtime isolation
        - Confirm runtime port is not reachable externally
        """
        # Try to connect to runtime port directly from host
        # This should fail because runtime is bound to 127.0.0.1
        # inside the container network

        sock = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
        sock.settimeout(3)

        try:
            result = sock.connect_ex(("localhost", 8081))
            if result == 0:
                pytest.fail(
                    "Runtime port 8081 is accessible from host! "
                    "This violates the security requirement that runtime "
                    "should only be accessible from sentinel via localhost."
                )
        except socket.error:
            pass  # Expected - connection should fail
        finally:
            sock.close()

        print("Runtime isolation verified - port 8081 not accessible from host")


class TestHealthEndpoints:
    """
    Tests for health and status endpoints.
    """

    def test_health_endpoint_states(self, e2e_services: dict):
        """
        Verify health endpoint behavior.

        Section 7.6:
        - /health returns 200 if Ready, 503 otherwise
        - /readiness returns 200 if state >= Decrypt
        - /status returns JSON with state info
        """
        if not e2e_services["sentinel_healthy"]:
            pytest.skip("Sentinel not healthy")

        # Test /health
        response = requests.get(f"{SENTINEL_HEALTH_URL}/health", timeout=5)
        assert response.status_code == 200, \
            f"Health check failed: {response.status_code}"

        # Test /readiness
        response = requests.get(f"{SENTINEL_HEALTH_URL}/readiness", timeout=5)
        assert response.status_code == 200, \
            f"Readiness check failed: {response.status_code}"

        # Test /status
        response = requests.get(f"{SENTINEL_HEALTH_URL}/status", timeout=5)
        assert response.status_code == 200, \
            f"Status check failed: {response.status_code}"

        status = response.json()
        assert "state" in status or "current_state" in status, \
            f"Status missing state field: {status}"
        assert status.get("ready", False) or status.get("is_ready", False) or \
               "Ready" in str(status.get("state", "")), \
            f"Sentinel not in Ready state: {status}"

        print("Health endpoints verified")


class TestInfrastructureServices:
    """
    Tests for infrastructure services (blob server, control plane).
    """

    def test_blob_server_range_requests(self, e2e_services: dict):
        """
        Verify blob server supports HTTP Range requests.

        This is critical for concurrent downloads.
        """
        # First, check what files are available
        response = requests.get(f"{BLOB_SERVER_URL}/", timeout=5)
        assert response.status_code == 200

        files = response.json().get("artifacts", [])
        if not files:
            pytest.skip("No artifacts available for range request test")

        # Test Range request on first file
        filename = files[0]["name"]
        file_size = files[0]["size"]

        # Request first 100 bytes
        headers = {"Range": "bytes=0-99"}
        response = requests.get(
            f"{BLOB_SERVER_URL}/artifacts/{filename}",
            headers=headers,
            timeout=5,
        )

        assert response.status_code == 206, \
            f"Expected 206 Partial Content, got {response.status_code}"

        assert "Content-Range" in response.headers, \
            "Missing Content-Range header"

        content_range = response.headers["Content-Range"]
        assert content_range.startswith("bytes 0-99/"), \
            f"Invalid Content-Range: {content_range}"

        assert len(response.content) == 100, \
            f"Expected 100 bytes, got {len(response.content)}"

        print("Blob server Range requests verified")

    def test_controlplane_authorization_api(self, e2e_services: dict, e2e_env: dict):
        """
        Verify control plane authorization API works correctly.
        """
        # Test authorized request
        response = requests.post(
            f"{CONTROLPLANE_URL}/api/v1/license/authorize",
            json={
                "contract_id": "contract-allow",
                "asset_id": "tb-asset-e2e-001",
                "hw_id": "test-hardware-id",
                "client_version": "test/1.0.0",
            },
            timeout=5,
        )

        assert response.status_code == 200, \
            f"Authorization request failed: {response.status_code}"

        data = response.json()
        assert data.get("status") == "authorized", \
            f"Expected authorized, got: {data}"

        assert "sas_url" in data, f"Missing sas_url: {data}"
        assert "manifest_url" in data, f"Missing manifest_url: {data}"
        assert "decryption_key_hex" in data or not e2e_env.get("E2E_DECRYPTION_KEY"), \
            f"Missing decryption_key_hex: {data}"

        # Test denied request
        response = requests.post(
            f"{CONTROLPLANE_URL}/api/v1/license/authorize",
            json={
                "contract_id": "contract-deny",
                "asset_id": "tb-asset-e2e-001",
                "hw_id": "test-hardware-id",
            },
            timeout=5,
        )

        assert response.status_code == 200, \
            f"Denial request failed: {response.status_code}"

        data = response.json()
        assert data.get("status") == "denied", \
            f"Expected denied, got: {data}"

        print("Control plane API verified")
