# TrustBridge E2E Testing Makefile
#
# This Makefile provides targets for running the complete E2E test workflow:
# 1. Generate deterministic test data
# 2. Encrypt test data using the CLI
# 3. Start E2E services (docker compose)
# 4. Run automated tests
# 5. Clean up
#
# Usage:
#   make e2e           # Run full E2E workflow
#   make e2e-up        # Just start services
#   make e2e-test      # Just run tests
#   make e2e-down      # Just stop services

.PHONY: e2e e2e-generate-plain e2e-encrypt e2e-up e2e-down e2e-test e2e-clean \
        e2e-logs e2e-status e2e-build-sentinel help

# Configuration
PLAINTEXT_SIZE_MB ?= 16
ASSET_ID ?= tb-asset-e2e-001
CHUNK_BYTES ?= 4194304

# Paths
E2E_DIR := $(shell pwd)
PROJECT_ROOT := $(E2E_DIR)/..
CLI_DIR := $(PROJECT_ROOT)/src/cli
SENTINEL_DIR := $(PROJECT_ROOT)/src/sentinel
DATA_DIR := $(E2E_DIR)/data
ARTIFACTS_DIR := $(E2E_DIR)/artifacts

# Default target
.DEFAULT_GOAL := help

help: ## Show this help message
	@echo "TrustBridge E2E Testing"
	@echo ""
	@echo "Usage: make <target>"
	@echo ""
	@echo "Targets:"
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "  %-20s %s\n", $$1, $$2}'
	@echo ""
	@echo "Quick Start:"
	@echo "  make e2e           # Run complete E2E workflow"
	@echo ""
	@echo "Configuration (environment variables):"
	@echo "  PLAINTEXT_SIZE_MB  Size of test data in MB (default: 16)"
	@echo "  ASSET_ID           Asset ID for encryption (default: tb-asset-e2e-001)"
	@echo "  CHUNK_BYTES        Chunk size for encryption (default: 4194304)"

# ============================================================================
# Data Generation
# ============================================================================

e2e-generate-plain: ## Generate deterministic test plaintext data
	@echo "Generating $(PLAINTEXT_SIZE_MB) MiB test data..."
	@mkdir -p $(DATA_DIR)
	@python3 $(DATA_DIR)/generate.py \
		--size $(PLAINTEXT_SIZE_MB) \
		--output $(DATA_DIR)/plain.weights
	@# Save SHA256 for later verification
	@python3 -c "import hashlib; print(hashlib.sha256(open('$(DATA_DIR)/plain.weights','rb').read()).hexdigest())" > $(DATA_DIR)/plain.weights.sha256
	@echo "SHA256 saved to $(DATA_DIR)/plain.weights.sha256"

# ============================================================================
# Encryption
# ============================================================================

e2e-encrypt: ## Encrypt test data using CLI
	@echo "Encrypting test data..."
	@mkdir -p $(ARTIFACTS_DIR)
	@cd $(CLI_DIR) && python3 -m trustbridge_cli.main encrypt \
		$(DATA_DIR)/plain.weights \
		--asset-id $(ASSET_ID) \
		--out $(ARTIFACTS_DIR) \
		--chunk-bytes $(CHUNK_BYTES) \
		2>&1 | tee $(ARTIFACTS_DIR)/encrypt.log
	@# Extract decryption key from output and save to .env
	@echo "Extracting decryption key..."
	@grep -oE '[a-f0-9]{64}' $(ARTIFACTS_DIR)/encrypt.log | head -1 > $(ARTIFACTS_DIR)/decryption_key.txt || true
	@if [ -s $(ARTIFACTS_DIR)/decryption_key.txt ]; then \
		echo "E2E_DECRYPTION_KEY=$$(cat $(ARTIFACTS_DIR)/decryption_key.txt)" > $(E2E_DIR)/.env; \
		echo "TB_CONTRACT_ID=contract-allow" >> $(E2E_DIR)/.env; \
		echo "TB_ASSET_ID=$(ASSET_ID)" >> $(E2E_DIR)/.env; \
		echo "Decryption key saved to .env"; \
	else \
		echo "WARNING: Could not extract decryption key from encrypt output"; \
		echo "Please manually add E2E_DECRYPTION_KEY to .env"; \
	fi

# ============================================================================
# Docker Compose Management
# ============================================================================

e2e-build-sentinel: ## Build sentinel binary
	@echo "Building sentinel binary..."
	@cd $(SENTINEL_DIR) && go build -o sentinel ./cmd/sentinel

e2e-up: ## Start E2E services
	@echo "Starting E2E services..."
	@if [ ! -f $(E2E_DIR)/.env ]; then \
		echo "ERROR: .env file not found. Run 'make e2e-encrypt' first."; \
		exit 1; \
	fi
	@docker compose -f $(E2E_DIR)/docker-compose.yml up --build -d
	@echo "Services started. Waiting for health..."
	@sleep 5
	@make e2e-status

e2e-down: ## Stop E2E services
	@echo "Stopping E2E services..."
	@docker compose -f $(E2E_DIR)/docker-compose.yml down -v

e2e-restart: ## Restart E2E services
	@make e2e-down
	@make e2e-up

e2e-logs: ## Show logs from all services
	@docker compose -f $(E2E_DIR)/docker-compose.yml logs

e2e-logs-sentinel: ## Show logs from sentinel only
	@docker compose -f $(E2E_DIR)/docker-compose.yml logs sentinel

e2e-logs-follow: ## Follow logs from all services
	@docker compose -f $(E2E_DIR)/docker-compose.yml logs -f

e2e-status: ## Show status of E2E services
	@echo "Service Status:"
	@docker compose -f $(E2E_DIR)/docker-compose.yml ps
	@echo ""
	@echo "Health Checks:"
	@echo -n "  Blob Server:    "; curl -sf http://localhost:9000/health > /dev/null && echo "OK" || echo "FAIL"
	@echo -n "  Control Plane:  "; curl -sf http://localhost:8080/health > /dev/null && echo "OK" || echo "FAIL"
	@echo -n "  Sentinel Health:"; curl -sf http://localhost:8001/health > /dev/null && echo "OK" || echo "NOT READY"
	@echo -n "  Sentinel API:   "; curl -sf http://localhost:8000/health > /dev/null && echo "OK" || echo "NOT READY"

# ============================================================================
# Testing
# ============================================================================

e2e-test: ## Run E2E test suite
	@echo "Running E2E tests..."
	@cd $(E2E_DIR)/tests && \
		pip install -q -r requirements.txt && \
		python3 -m pytest test_e2e.py -v --tb=short

e2e-test-quick: ## Run quick smoke tests only
	@echo "Running quick E2E tests..."
	@cd $(E2E_DIR)/tests && \
		pip install -q -r requirements.txt && \
		python3 -m pytest test_e2e.py -v -k "health or proxy" --tb=short

e2e-test-verbose: ## Run E2E tests with full output
	@echo "Running E2E tests (verbose)..."
	@cd $(E2E_DIR)/tests && \
		pip install -q -r requirements.txt && \
		python3 -m pytest test_e2e.py -v -s --tb=long

# ============================================================================
# Verification Commands
# ============================================================================

e2e-verify-hash: ## Verify plaintext hash from runtime-mock
	@echo "Querying runtime-mock for plaintext hash..."
	@echo "Original SHA256: $$(cat $(DATA_DIR)/plain.weights.sha256 2>/dev/null || echo 'N/A')"
	@echo "Decrypted SHA256: $$(curl -sf http://localhost:8000/plaintext-hash | python3 -c 'import sys,json; print(json.load(sys.stdin).get("sha256","N/A"))' 2>/dev/null || echo 'N/A')"

e2e-curl-demo: ## Send demo request through sentinel
	@echo "Sending demo request..."
	@curl -s http://localhost:8000/v1/demo | python3 -m json.tool

e2e-curl-status: ## Get sentinel status
	@echo "Sentinel status:"
	@curl -s http://localhost:8001/status | python3 -m json.tool

# ============================================================================
# Full Workflow
# ============================================================================

e2e: e2e-generate-plain e2e-encrypt e2e-up ## Run complete E2E workflow (generate, encrypt, start, test)
	@echo ""
	@echo "Waiting for sentinel to become ready (this may take a minute)..."
	@for i in 1 2 3 4 5 6 7 8 9 10 11 12; do \
		if curl -sf http://localhost:8001/health > /dev/null 2>&1; then \
			echo "Sentinel is ready!"; \
			break; \
		fi; \
		echo "  Waiting... ($$i/12)"; \
		sleep 10; \
	done
	@echo ""
	@make e2e-status
	@echo ""
	@make e2e-test

e2e-interactive: e2e-generate-plain e2e-encrypt e2e-up ## Start E2E environment for interactive testing
	@echo ""
	@echo "E2E environment is ready for interactive testing."
	@echo ""
	@echo "Useful commands:"
	@echo "  curl http://localhost:8000/health      # Check sentinel health"
	@echo "  curl http://localhost:8000/v1/demo     # Test proxy"
	@echo "  curl http://localhost:8000/plaintext-hash  # Get decrypted hash"
	@echo "  make e2e-logs-follow                   # Follow all logs"
	@echo "  make e2e-down                          # Stop all services"

# ============================================================================
# Cleanup
# ============================================================================

e2e-clean: e2e-down ## Stop services and remove generated files
	@echo "Cleaning up E2E artifacts..."
	@rm -f $(DATA_DIR)/plain.weights
	@rm -f $(DATA_DIR)/plain.weights.sha256
	@rm -f $(ARTIFACTS_DIR)/model.tbenc
	@rm -f $(ARTIFACTS_DIR)/model.manifest.json
	@rm -f $(ARTIFACTS_DIR)/encrypt.log
	@rm -f $(ARTIFACTS_DIR)/decryption_key.txt
	@rm -f $(E2E_DIR)/.env
	@echo "Cleanup complete"

e2e-clean-all: e2e-clean ## Clean everything including Docker images
	@echo "Removing Docker images..."
	@docker compose -f $(E2E_DIR)/docker-compose.yml down --rmi local -v 2>/dev/null || true
	@echo "Full cleanup complete"
