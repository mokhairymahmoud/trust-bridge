#!/usr/bin/env python3
"""
TrustBridge Runtime Mock for E2E Testing

This mock server simulates the runtime behavior for E2E testing:
1. Waits for the sentinel ready signal
2. Reads decrypted weights from FIFO
3. Computes SHA256 hash of the plaintext
4. Exposes HTTP API for testing verification

Endpoints:
- GET /health         - Health check (always 200)
- GET /v1/demo        - Demo endpoint with plaintext hash
- POST /v1/demo       - Demo endpoint with plaintext hash
- GET /plaintext-hash - Returns SHA256 of data read from FIFO
- GET /status         - Full status information
"""

import hashlib
import os
import threading
import time
from typing import Optional

from flask import Flask, jsonify, request

app = Flask(__name__)

# Global state for tracking FIFO reading
class State:
    def __init__(self):
        self.plaintext_hash: Optional[str] = None
        self.bytes_read: int = 0
        self.read_complete: bool = False
        self.read_error: Optional[str] = None
        self.started_at: float = time.time()
        self.lock = threading.Lock()

    def update(self, **kwargs):
        with self.lock:
            for key, value in kwargs.items():
                setattr(self, key, value)

    def get_all(self) -> dict:
        with self.lock:
            return {
                "plaintext_hash": self.plaintext_hash,
                "bytes_read": self.bytes_read,
                "read_complete": self.read_complete,
                "read_error": self.read_error,
                "uptime_seconds": time.time() - self.started_at,
            }


state = State()


def read_fifo_thread(pipe_path: str, ready_signal: str):
    """
    Background thread that:
    1. Waits for the ready signal file
    2. Opens and reads from the FIFO
    3. Computes SHA256 hash of all data read
    """
    global state

    # Wait for ready signal
    print(f"[runtime-mock] Waiting for ready signal: {ready_signal}")
    timeout = 300  # 5 minutes timeout
    elapsed = 0
    while not os.path.exists(ready_signal):
        time.sleep(0.5)
        elapsed += 0.5
        if elapsed >= timeout:
            state.update(read_error=f"Timeout waiting for ready signal after {timeout}s")
            print(f"[runtime-mock] ERROR: {state.read_error}")
            return

    print(f"[runtime-mock] Ready signal received!")

    # Read from FIFO
    print(f"[runtime-mock] Opening FIFO: {pipe_path}")
    try:
        hasher = hashlib.sha256()
        bytes_read = 0
        chunk_size = 1024 * 1024  # 1MB chunks

        with open(pipe_path, "rb") as f:
            while True:
                chunk = f.read(chunk_size)
                if not chunk:
                    break

                hasher.update(chunk)
                bytes_read += len(chunk)
                state.update(bytes_read=bytes_read)

                # Log progress every 10MB
                if bytes_read % (10 * 1024 * 1024) == 0:
                    print(f"[runtime-mock] Read {bytes_read / (1024*1024):.1f} MB")

        final_hash = hasher.hexdigest()
        print(f"[runtime-mock] Read complete: {bytes_read} bytes")
        print(f"[runtime-mock] SHA256: {final_hash}")

        state.update(
            plaintext_hash=final_hash,
            read_complete=True,
        )

    except Exception as e:
        error_msg = f"Error reading FIFO: {e}"
        print(f"[runtime-mock] {error_msg}")
        state.update(read_error=error_msg)


@app.route("/health", methods=["GET"])
def health():
    """
    Health check endpoint.
    Always returns 200 OK once server is running.
    """
    return jsonify({
        "status": "ok",
        "service": "runtime-mock",
    })


@app.route("/v1/demo", methods=["GET", "POST"])
def demo():
    """
    Demo endpoint for testing proxy functionality.
    Echoes request info and includes plaintext hash if available.
    """
    current_state = state.get_all()

    response = {
        "message": "Hello from runtime-mock",
        "plaintext_hash": current_state["plaintext_hash"],
        "bytes_read": current_state["bytes_read"],
        "read_complete": current_state["read_complete"],
        "request": {
            "method": request.method,
            "path": request.path,
            "content_type": request.content_type,
        },
    }

    # Include request body hash for POST requests
    if request.method == "POST" and request.data:
        req_hash = hashlib.sha256(request.data).hexdigest()
        response["request"]["body_sha256"] = req_hash
        response["request"]["body_size"] = len(request.data)

    return jsonify(response)


@app.route("/plaintext-hash", methods=["GET"])
def plaintext_hash():
    """
    Returns SHA256 hash of data read from FIFO.

    Returns:
    - 200 with hash if read complete
    - 202 if still reading
    - 500 if error occurred
    """
    current_state = state.get_all()

    if current_state["read_error"]:
        return jsonify({
            "error": current_state["read_error"],
        }), 500

    if not current_state["read_complete"]:
        return jsonify({
            "status": "reading",
            "bytes_read": current_state["bytes_read"],
        }), 202

    return jsonify({
        "sha256": current_state["plaintext_hash"],
        "bytes_read": current_state["bytes_read"],
    })


@app.route("/status", methods=["GET"])
def status():
    """
    Full status endpoint with all state information.
    """
    return jsonify(state.get_all())


def main():
    """Main entry point for the runtime mock server."""
    # Configuration from environment variables
    pipe_path = os.environ.get("TB_PIPE_PATH", "/dev/shm/model-pipe")
    ready_signal = os.environ.get("TB_READY_SIGNAL", "/dev/shm/weights/ready.signal")
    host = os.environ.get("TB_RUNTIME_HOST", "127.0.0.1")
    port = int(os.environ.get("TB_RUNTIME_PORT", "8081"))

    print("[runtime-mock] ========================================")
    print("[runtime-mock] TrustBridge Runtime Mock Server")
    print("[runtime-mock] ========================================")
    print(f"[runtime-mock] FIFO path: {pipe_path}")
    print(f"[runtime-mock] Ready signal: {ready_signal}")
    print(f"[runtime-mock] Listening on: {host}:{port}")
    print("[runtime-mock] ========================================")

    # Start FIFO reader in background thread
    reader_thread = threading.Thread(
        target=read_fifo_thread,
        args=(pipe_path, ready_signal),
        daemon=True,
        name="fifo-reader",
    )
    reader_thread.start()

    # Start Flask server
    # Note: Using threaded=True for concurrent request handling
    app.run(host=host, port=port, threaded=True)


if __name__ == "__main__":
    main()
