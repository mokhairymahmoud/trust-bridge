#!/usr/bin/env python3
"""
E2E Control Plane Mock - License authorization API for testing.

This server mocks the TrustBridge Control Plane/EDC for E2E testing.
It provides the authorization endpoint that the Sentinel calls during startup.

Usage:
    python server.py [--port PORT]

Environment Variables:
    CP_PORT: Server port (default: 8080)
    E2E_DECRYPTION_KEY: 64-character hex decryption key
    E2E_BLOB_SERVER: Blob server base URL (default: http://blob-server:9000)
    E2E_EXPIRY_SECONDS: SAS URL expiry time in seconds (default: 3600)
"""

import os
import json
import logging
from datetime import datetime, timezone, timedelta
from flask import Flask, request, Response

# Configure logging
logging.basicConfig(
    level=logging.INFO,
    format="%(asctime)s [%(levelname)s] %(message)s",
    datefmt="%Y-%m-%dT%H:%M:%S",
)
logger = logging.getLogger(__name__)

app = Flask(__name__)

# Configuration from environment
PORT = int(os.environ.get("CP_PORT", "8080"))
DECRYPTION_KEY = os.environ.get("E2E_DECRYPTION_KEY", "")
BLOB_SERVER = os.environ.get("E2E_BLOB_SERVER", "http://blob-server:9000")
EXPIRY_SECONDS = int(os.environ.get("E2E_EXPIRY_SECONDS", "3600"))

# Allowed contract ID for authorization
ALLOWED_CONTRACT = "contract-allow"


@app.route("/api/v1/license/authorize", methods=["POST"])
def authorize() -> Response:
    """
    License authorization endpoint.

    Request Body:
    {
        "contract_id": "contract-123",
        "asset_id": "tb-asset-123",
        "hw_id": "<hardware-fingerprint>",
        "attestation": "<optional>",
        "client_version": "sentinel/0.1.0"
    }

    Response (authorized):
    {
        "status": "authorized",
        "sas_url": "http://..../model.tbenc",
        "manifest_url": "http://..../model.manifest.json",
        "decryption_key_hex": "<64 hex chars>",
        "expires_at": "2026-01-08T12:00:00Z"
    }

    Response (denied):
    {
        "status": "denied",
        "reason": "contract_invalid"
    }
    """
    try:
        data = request.get_json()
        if not data:
            logger.warning("Empty request body")
            return Response(
                json.dumps({"status": "denied", "reason": "invalid_request"}),
                status=400,
                mimetype="application/json",
            )

        contract_id = data.get("contract_id", "")
        asset_id = data.get("asset_id", "")
        hw_id = data.get("hw_id", "")
        client_version = data.get("client_version", "unknown")

        logger.info(
            f"Authorization request: contract={contract_id}, asset={asset_id}, "
            f"hw_id={hw_id[:16]}..., client={client_version}"
        )

        # Check if contract is allowed
        if contract_id != ALLOWED_CONTRACT:
            logger.warning(f"Authorization denied: invalid contract_id={contract_id}")
            return Response(
                json.dumps({
                    "status": "denied",
                    "reason": "contract_invalid",
                }),
                status=200,  # Return 200 with denied status per API spec
                mimetype="application/json",
            )

        # Validate decryption key is configured
        if not DECRYPTION_KEY or len(DECRYPTION_KEY) != 64:
            logger.error("E2E_DECRYPTION_KEY not configured or invalid")
            return Response(
                json.dumps({
                    "status": "denied",
                    "reason": "server_configuration_error",
                }),
                status=500,
                mimetype="application/json",
            )

        # Calculate expiry time
        expires_at = datetime.now(timezone.utc) + timedelta(seconds=EXPIRY_SECONDS)

        # Build response
        response_data = {
            "status": "authorized",
            "sas_url": f"{BLOB_SERVER}/artifacts/model.tbenc",
            "manifest_url": f"{BLOB_SERVER}/artifacts/model.manifest.json",
            "decryption_key_hex": DECRYPTION_KEY,
            "expires_at": expires_at.strftime("%Y-%m-%dT%H:%M:%SZ"),
        }

        logger.info(
            f"Authorization granted: asset={asset_id}, expires={response_data['expires_at']}"
        )

        return Response(
            json.dumps(response_data),
            status=200,
            mimetype="application/json",
        )

    except Exception as e:
        logger.error(f"Authorization error: {e}")
        return Response(
            json.dumps({"status": "denied", "reason": "internal_error"}),
            status=500,
            mimetype="application/json",
        )


@app.route("/health", methods=["GET"])
def health() -> Response:
    """Health check endpoint."""
    return Response(
        json.dumps({"status": "healthy"}),
        mimetype="application/json",
    )


@app.route("/", methods=["GET"])
def index() -> Response:
    """Service info endpoint."""
    return Response(
        json.dumps({
            "service": "trustbridge-controlplane-mock",
            "version": "1.0.0",
            "endpoints": [
                "POST /api/v1/license/authorize",
                "GET /health",
            ],
            "config": {
                "allowed_contract": ALLOWED_CONTRACT,
                "blob_server": BLOB_SERVER,
                "expiry_seconds": EXPIRY_SECONDS,
                "key_configured": len(DECRYPTION_KEY) == 64,
            },
        }, indent=2),
        mimetype="application/json",
    )


if __name__ == "__main__":
    logger.info(f"Starting control plane mock on port {PORT}")
    logger.info(f"Blob server URL: {BLOB_SERVER}")
    logger.info(f"Allowed contract: {ALLOWED_CONTRACT}")
    logger.info(f"Decryption key configured: {len(DECRYPTION_KEY) == 64}")
    logger.info(f"SAS expiry: {EXPIRY_SECONDS} seconds")

    if not DECRYPTION_KEY:
        logger.warning("E2E_DECRYPTION_KEY not set - authorization will fail!")

    app.run(host="0.0.0.0", port=PORT, threaded=True)
