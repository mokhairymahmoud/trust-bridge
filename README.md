# TrustBridge - Secure AI Model Distribution Platform

TrustBridge is a secure platform for distributing large AI model weights with encryption, contract-based licensing, and runtime isolation.

## Project Status

**Current Phase:** Phase 1 Complete ✅

Implementation follows the detailed specification in [`implementation.md`](./implementation.md).

### Phase 1: Crypto Foundation ✅ COMPLETE

Cryptographic interoperability between provider (Python) and consumer (Go) is fully implemented and validated.

- ✅ Python `tbenc/v1` encryption
- ✅ Go `tbenc/v1` decryption
- ✅ Cross-language interoperability validation
- ✅ Multiple chunk sizes tested (1KB, 1MB, 4MB, 16MB)
- ✅ Edge cases coverage (empty files, single byte, non-aligned chunks)

See [`PHASE1_STATUS.md`](./PHASE1_STATUS.md) for detailed completion report.

## Repository Structure

```
.
├── implementation.md           # Complete implementation specification
├── PHASE1_STATUS.md           # Phase 1 completion report
├── README.md                  # This file
│
├── src/
│   ├── cli/                   # Provider CLI (Python)
│   │   ├── trustbridge_cli/
│   │   │   ├── crypto_tbenc.py      # tbenc/v1 encryption
│   │   │   └── __init__.py
│   │   ├── requirements.txt
│   │   └── test_crypto_tbenc.py     # Unit tests
│   │
│   ├── sentinel/              # Consumer sentinel (Go)
│   │   ├── go.mod
│   │   ├── cmd/
│   │   │   └── decrypt-test/        # CLI test utility
│   │   │       └── main.go
│   │   └── internal/
│   │       └── crypto/
│   │           ├── decrypt.go       # tbenc/v1 decryption
│   │           └── decrypt_test.go  # Unit tests
│   │
│   └── runtime/               # (To be implemented)
│
├── scripts/
│   ├── test-crypto-interop.sh       # Cross-language validation
│   └── test-chunk-sizes.sh          # Chunk size validation
│
├── e2e/                       # (To be implemented)
├── infra/                     # (To be implemented)
└── docs/                      # (To be implemented)
```

## Quick Start

### Testing Crypto Implementation

1. **Run Python encryption tests:**
```bash
cd src/cli
python3 -m venv venv
source venv/bin/activate
pip install -r requirements.txt pytest
python -m pytest test_crypto_tbenc.py -v
```

2. **Run Go decryption tests:**
```bash
cd src/sentinel
go test -v ./internal/crypto/
```

3. **Run cross-language interop test:**
```bash
bash scripts/test-crypto-interop.sh
```

4. **Run chunk size validation:**
```bash
bash scripts/test-chunk-sizes.sh
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

- [x] **Phase 1:** Crypto Foundation (Days 1-3) ✅
- [ ] **Phase 2:** Provider CLI - Core Commands (Days 4-7)
- [ ] **Phase 3:** Sentinel - Authorization & Fingerprinting (Days 8-10)
- [ ] **Phase 4:** Sentinel - Asset Hydration (Days 11-14)
- [ ] **Phase 5:** Sentinel - Decryption & Ready Signal (Days 15-17)
- [ ] **Phase 6:** Sentinel - State Machine & Health (Days 18-20)
- [ ] **Phase 7:** Sentinel - Proxy & Audit (Days 21-23)
- [ ] **Phase 8:** Sentinel - Billing & Suspend Logic (Days 24-25)
- [ ] **Phase 9:** Runtime Integration (Days 26-27)
- [ ] **Phase 10:** E2E Infrastructure (Days 28-32)
- [ ] **Phase 11:** Infrastructure Packaging (Days 33-36)
- [ ] **Phase 12:** Production Hardening (Days 37-40)
- [ ] **Phase 13:** Acceptance Testing (Days 41-42)
- [ ] **Phase 14:** Documentation & Handoff (Days 43-45)

## Testing

### Current Test Coverage

- **Python:** 8 test cases, 100% function coverage
- **Go:** 11 test cases + 1 benchmark, comprehensive coverage
- **Cross-language:** End-to-end interoperability validated
- **Chunk sizes:** 1KB, 1MB, 4MB, 16MB validated
- **Edge cases:** Empty files, single chunk, multiple chunks, wrong key

### Test Results

All tests passing:
- Python: 8/8 ✅
- Go: 11/11 ✅
- Interop: Hash match verified ✅
- Chunk sizes: 4/4 ✅

## License

[To be determined]

## Contributing

[To be determined]

## Contact

[To be determined]
