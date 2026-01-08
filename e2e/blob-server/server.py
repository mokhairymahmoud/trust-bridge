#!/usr/bin/env python3
"""
E2E Blob Server - HTTP file server with Range request support.

This server mocks Azure Blob Storage for E2E testing by serving files
from the artifacts directory with full HTTP Range header support for
concurrent/partial downloads.

Usage:
    python server.py [--port PORT] [--data-dir DATA_DIR]

Environment Variables:
    BLOB_PORT: Server port (default: 9000)
    BLOB_DATA_DIR: Directory to serve files from (default: /data)
"""

import os
import logging
from flask import Flask, request, Response, send_file, abort

# Configure logging
logging.basicConfig(
    level=logging.INFO,
    format="%(asctime)s [%(levelname)s] %(message)s",
    datefmt="%Y-%m-%dT%H:%M:%S",
)
logger = logging.getLogger(__name__)

app = Flask(__name__)

# Configuration from environment
DATA_DIR = os.environ.get("BLOB_DATA_DIR", "/data")
PORT = int(os.environ.get("BLOB_PORT", "9000"))


def parse_range_header(range_header: str, file_size: int) -> tuple[int, int] | None:
    """
    Parse HTTP Range header.

    Supports format: bytes=start-end
    Returns (start, end) tuple or None if invalid.
    """
    if not range_header or not range_header.startswith("bytes="):
        return None

    try:
        range_spec = range_header[6:]  # Remove "bytes="
        if "-" not in range_spec:
            return None

        start_str, end_str = range_spec.split("-", 1)

        # Handle different range formats
        if start_str == "":
            # Suffix range: -500 means last 500 bytes
            suffix_length = int(end_str)
            start = max(0, file_size - suffix_length)
            end = file_size - 1
        elif end_str == "":
            # Open-ended range: 500- means from byte 500 to end
            start = int(start_str)
            end = file_size - 1
        else:
            # Full range: 0-499
            start = int(start_str)
            end = int(end_str)

        # Validate range
        if start < 0 or start >= file_size:
            return None
        if end >= file_size:
            end = file_size - 1
        if start > end:
            return None

        return (start, end)
    except (ValueError, IndexError):
        return None


def serve_file_with_range(file_path: str, filename: str) -> Response:
    """Serve a file with HTTP Range support."""
    file_size = os.path.getsize(file_path)
    range_header = request.headers.get("Range")

    if range_header:
        byte_range = parse_range_header(range_header, file_size)
        if byte_range is None:
            # Invalid range - return 416 Range Not Satisfiable
            response = Response(status=416)
            response.headers["Content-Range"] = f"bytes */{file_size}"
            return response

        start, end = byte_range
        length = end - start + 1

        logger.info(f"Range request: {filename} bytes={start}-{end} ({length} bytes)")

        with open(file_path, "rb") as f:
            f.seek(start)
            data = f.read(length)

        response = Response(
            data,
            status=206,
            mimetype="application/octet-stream",
        )
        response.headers["Content-Range"] = f"bytes {start}-{end}/{file_size}"
        response.headers["Content-Length"] = str(length)
        response.headers["Accept-Ranges"] = "bytes"
        return response

    # Full file request
    logger.info(f"Full file request: {filename} ({file_size} bytes)")
    response = send_file(
        file_path,
        mimetype="application/octet-stream",
        as_attachment=True,
        download_name=filename,
    )
    response.headers["Accept-Ranges"] = "bytes"
    response.headers["Content-Length"] = str(file_size)
    return response


@app.route("/artifacts/<path:filename>", methods=["GET", "HEAD"])
def get_artifact(filename: str) -> Response:
    """
    Serve artifact files with Range request support.

    GET /artifacts/<filename> - Download file (supports Range header)
    HEAD /artifacts/<filename> - Get file metadata
    """
    # Security: prevent path traversal
    if ".." in filename or filename.startswith("/"):
        logger.warning(f"Blocked path traversal attempt: {filename}")
        abort(400, "Invalid filename")

    file_path = os.path.join(DATA_DIR, filename)

    if not os.path.isfile(file_path):
        logger.warning(f"File not found: {filename}")
        abort(404, "File not found")

    if request.method == "HEAD":
        file_size = os.path.getsize(file_path)
        response = Response(status=200)
        response.headers["Content-Length"] = str(file_size)
        response.headers["Accept-Ranges"] = "bytes"
        response.headers["Content-Type"] = "application/octet-stream"
        return response

    return serve_file_with_range(file_path, filename)


@app.route("/health", methods=["GET"])
def health() -> Response:
    """Health check endpoint."""
    return Response('{"status": "healthy"}', mimetype="application/json")


@app.route("/", methods=["GET"])
def index() -> Response:
    """List available artifacts."""
    try:
        files = []
        if os.path.isdir(DATA_DIR):
            for f in os.listdir(DATA_DIR):
                file_path = os.path.join(DATA_DIR, f)
                if os.path.isfile(file_path):
                    files.append({
                        "name": f,
                        "size": os.path.getsize(file_path),
                        "url": f"/artifacts/{f}",
                    })

        import json
        return Response(
            json.dumps({"artifacts": files}, indent=2),
            mimetype="application/json",
        )
    except Exception as e:
        logger.error(f"Error listing artifacts: {e}")
        return Response(
            '{"error": "Internal server error"}',
            status=500,
            mimetype="application/json",
        )


if __name__ == "__main__":
    logger.info(f"Starting blob server on port {PORT}")
    logger.info(f"Serving files from: {DATA_DIR}")

    if not os.path.isdir(DATA_DIR):
        logger.warning(f"Data directory does not exist: {DATA_DIR}")
        os.makedirs(DATA_DIR, exist_ok=True)
        logger.info(f"Created data directory: {DATA_DIR}")

    app.run(host="0.0.0.0", port=PORT, threaded=True)
