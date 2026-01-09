# TrustBridge - Secure AI Model Distribution Platform

TrustBridge is a secure platform for distributing large AI model weights with encryption, contract-based licensing, and runtime isolation.

## Project Status

**Current Phase:** Phase 11 Complete ✅ (Core Platform Functional)

Implementation follows the detailed specification in [`implementation.md`](./implementation.md).

### Completed Phases (1-11)

| Phase | Description | Status |
|-------|-------------|--------|
| **1** | Crypto Foundation | ✅ Complete |
| **2** | Provider CLI - Core Commands | ✅ Complete |
| **3** | Sentinel - Authorization & Fingerprinting | ✅ Complete |
| **4** | Sentinel - Asset Hydration | ✅ Complete |
| **5** | Sentinel - Decryption & Ready Signal | ✅ Complete |
| **6** | Sentinel - State Machine & Health | ✅ Complete |
| **7** | Sentinel - Proxy & Audit | ✅ Complete |
| **8** | Sentinel - Billing & Suspend Logic | ✅ Complete |
| **9** | Runtime Integration | ✅ Complete |
| **10** | E2E Infrastructure | ✅ Complete |
| **11** | Infrastructure Packaging | ✅ Complete |

### What's Working

- **Provider Side**: Complete workflow - encrypt → upload → register → build → package → publish
- **Consumer Side**: Full sentinel lifecycle - authorize → hydrate → decrypt → proxy → audit
- **E2E Testing**: Automated test suite with Docker Compose
- **Azure Deployment**: Managed App templates with GPU VM provisioning

### Remaining Phases (12-15)

- **Phase 12**: Production Hardening (SAS retry optimization, mTLS)
- **Phase 13**: Acceptance Testing
- **Phase 14**: Documentation & Handoff
- **Phase 15**: QLoRA Fine-Tuning (optional feature)

## Repository Structure

```
.
├── implementation.md              # Complete implementation specification
├── README.md                      # This file
│
├── src/
│   ├── cli/                       # Provider CLI (Python 3.10+)
│   │   ├── trustbridge_cli/
│   │   │   ├── main.py            # Typer CLI entry point
│   │   │   ├── crypto_tbenc.py    # tbenc/v1 encryption
│   │   │   ├── commands/          # CLI command implementations
│   │   │   │   ├── encrypt.py     # Encrypt model weights
│   │   │   │   ├── blob.py        # Upload to Azure Blob
│   │   │   │   ├── edc.py         # Register with Control Plane
│   │   │   │   ├── build.py       # Build Docker images
│   │   │   │   ├── package.py     # Generate Managed App package
│   │   │   │   └── publish.py     # Full workflow orchestration
│   │   │   └── common/            # Shared utilities
│   │   ├── requirements.txt
│   │   └── tests/                 # Unit tests
│   │
│   ├── sentinel/                  # Consumer sentinel (Go 1.21+)
│   │   ├── go.mod
│   │   ├── cmd/sentinel/          # Main sentinel binary
│   │   └── internal/
│   │       ├── config/            # Configuration management
│   │       ├── license/           # Authorization & fingerprinting
│   │       ├── asset/             # Download & integrity verification
│   │       ├── crypto/            # tbenc/v1 decryption, FIFO
│   │       ├── state/             # State machine orchestration
│   │       ├── health/            # Health check endpoints
│   │       ├── proxy/             # Reverse proxy & audit logging
│   │       └── billing/           # Usage metering & billing
│   │
│   └── runtime/                   # Runtime wrapper
│       └── entrypoint.sh          # FIFO-based model loading
│
├── e2e/                           # E2E testing infrastructure
│   ├── docker-compose.yml         # Full test environment
│   ├── Makefile                   # Test automation
│   ├── data/                      # Test data generation
│   ├── artifacts/                 # Encrypted test files
│   ├── blob-server/               # HTTP file server (Range support)
│   ├── controlplane-mock/         # Mock authorization API
│   ├── runtime-mock/              # Mock inference server
│   └── tests/                     # Automated pytest suite
│
├── infra/                         # Azure infrastructure
│   ├── main.bicep                 # Managed App deployment
│   ├── createUiDefinition.json    # Azure Portal UI
│   ├── cloud-init/                # VM bootstrap scripts
│   └── scripts/                   # Deployment utilities
│
├── scripts/                       # Development scripts
│   ├── test-crypto-interop.sh     # Cross-language validation
│   └── test-chunk-sizes.sh        # Chunk size validation
│
└── docs/                          # Documentation
    ├── workflow.md                # End-to-end workflow guide
    └── kserve-kvcache-analysis.md # KServe & KV Cache integration analysis
```

## Quick Start

### Run Full E2E Demo (Recommended)

The easiest way to verify the entire system works:

```bash
# Navigate to e2e directory
cd e2e

# Run the full E2E workflow
make e2e
```

This will:
1. Generate test plaintext weights
2. Encrypt weights using the provider CLI
3. Start all services (blob server, control plane mock, sentinel, runtime mock)
4. Run automated tests validating the complete workflow
5. Clean up when done

### Provider Workflow (Manual)

```bash
# 1. Install provider CLI
cd src/cli
pip install -r requirements.txt

# 2. Encrypt model weights
trustbridge encrypt \
  --asset-id my-model-001 \
  --in /path/to/model.safetensors \
  --out model.tbenc \
  --manifest model.manifest.json

# 3. Upload to Azure Blob Storage
trustbridge upload \
  --storage-account myaccount \
  --container models \
  --asset-id my-model-001 \
  --encrypted-file model.tbenc \
  --manifest model.manifest.json

# 4. Register with Control Plane
trustbridge register \
  --edc-endpoint https://controlplane.example.com \
  --asset-id my-model-001 \
  --manifest model.manifest.json \
  --key-hex <decryption-key>

# 5. Build and push Docker image
trustbridge build \
  --registry myacr.azurecr.io \
  --image-name trustbridge-runtime \
  --tag v1.0.0

# 6. Generate Managed App package
trustbridge package \
  --asset-id my-model-001 \
  --image myacr.azurecr.io/trustbridge-runtime:v1.0.0 \
  --output-zip managed-app.zip

# OR run all steps at once:
trustbridge publish --all-steps ...
```

### Development Testing

```bash
# Run Python unit tests
cd src/cli
pip install -r requirements.txt pytest
python -m pytest tests/ -v

# Run Go unit tests
cd src/sentinel
go test -v ./...

# Run cross-language interop test
bash scripts/test-crypto-interop.sh
```

## tbenc/v1 Format

TrustBridge uses a custom chunked encryption format optimized for streaming large files:

- **Algorithm:** AES-256-GCM with chunked streaming
- **Chunk sizes:** Configurable (recommended 4-16MB)
- **Authentication:** Per-chunk GCM tags with AAD
- **Nonce derivation:** Deterministic from random prefix + counter

### Header Format (32 bytes)
```
Magic:       TBENC001     (8 bytes)
Version:     1            (uint16, big-endian)
Algorithm:   1            (uint8, AES-256-GCM-CHUNKED)
ChunkBytes:  <size>       (uint32, big-endian)
NoncePrefix: <random>     (4 bytes)
Reserved:    <zeros>      (13 bytes)
```

### Record Format (per chunk)
```
PlaintextLen:    <len>            (uint32, big-endian)
CiphertextTag:   <encrypted+tag>  (PlaintextLen + 16 bytes)
```

## System Architecture

### Roles

- **Provider:** Encrypts model weights, uploads to Azure Blob Storage, registers with Control Plane
- **Consumer:** Deploys via Azure Managed App, sentinel hydrates and decrypts weights at runtime
- **Control Plane:** (External) Validates contracts, issues SAS URLs, manages decryption keys

### Runtime Pattern

Ambassador Sidecar architecture:
- **Sentinel (sidecar):** Security, licensing, decryption, proxying, audit, billing
- **Runtime (model server):** Vanilla inference (vLLM/Triton), binds to localhost only

## Security Principles

1. **No plaintext on disk:** Weights decrypted to FIFO/tmpfs only
2. **Runtime isolation:** Model server not externally accessible
3. **Contract gating:** Authorization required before hydration
4. **Streaming decryption:** Minimize memory exposure
5. **Secure buffer cleanup:** Best-effort memory overwrites

## Technology Stack

- **Provider CLI:** Python 3.10+, Typer, cryptography, Azure SDK
- **Consumer Sentinel:** Go 1.21+, standard library crypto
- **Infrastructure:** Azure (Managed Apps, GPU VMs, Blob Storage)
- **Encryption:** AES-256-GCM (Python: cryptography, Go: crypto/cipher)

## Development Roadmap

Based on `implementation.md` Section 10:

- [x] **Phase 1:** Crypto Foundation ✅
- [x] **Phase 2:** Provider CLI - Core Commands ✅
- [x] **Phase 3:** Sentinel - Authorization & Fingerprinting ✅
- [x] **Phase 4:** Sentinel - Asset Hydration ✅
- [x] **Phase 5:** Sentinel - Decryption & Ready Signal ✅
- [x] **Phase 6:** Sentinel - State Machine & Health ✅
- [x] **Phase 7:** Sentinel - Proxy & Audit ✅
- [x] **Phase 8:** Sentinel - Billing & Suspend Logic ✅
- [x] **Phase 9:** Runtime Integration ✅
- [x] **Phase 10:** E2E Infrastructure ✅
- [x] **Phase 11:** Infrastructure Packaging ✅
- [ ] **Phase 12:** Production Hardening
- [ ] **Phase 13:** Acceptance Testing
- [ ] **Phase 14:** Documentation & Handoff
- [ ] **Phase 15:** QLoRA Fine-Tuning (optional)

## Testing

### Test Suites

| Suite | Description | Status |
|-------|-------------|--------|
| **Python Unit Tests** | Encryption, key generation, manifest | ✅ Passing |
| **Go Unit Tests** | Decryption, download, state machine, proxy | ✅ Passing |
| **Cross-language Interop** | Python encrypt → Go decrypt | ✅ Passing |
| **E2E Integration** | Full workflow validation | ✅ Passing |

### E2E Test Coverage

The automated E2E test suite (`e2e/tests/test_e2e.py`) validates:

- **Authorization Gating**: Contract denial blocks access
- **Download Integrity**: SHA256 hash verification
- **Decrypt Interop**: Cross-language encryption/decryption
- **No Plaintext on Disk**: Security validation
- **Proxy Forwarding**: Request routing to runtime
- **Audit Logging**: Audit trail generation
- **Runtime Isolation**: Port accessibility restrictions

### Running Tests

```bash
# Full E2E test suite
cd e2e && make e2e

# Python unit tests only
cd src/cli && python -m pytest tests/ -v

# Go unit tests only
cd src/sentinel && go test -v ./...
```

## License

[To be determined]

## Contributing

[To be determined]

## Contact

[To be determined]
