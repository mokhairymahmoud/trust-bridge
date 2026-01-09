# TrustBridge – Global Implementation Specification

---

## Implementation Status

| Phase | Description | Status |
|-------|-------------|--------|
| **Phase 1** | Crypto Foundation | ✅ Complete |
| **Phase 2** | Provider CLI - Core Commands | ✅ Complete |
| **Phase 3** | Sentinel - Authorization & Fingerprinting | ✅ Complete |
| **Phase 4** | Sentinel - Asset Hydration | ✅ Complete |
| **Phase 5** | Sentinel - Decryption & Ready Signal | ✅ Complete |
| **Phase 6** | Sentinel - State Machine & Health | ✅ Complete |
| **Phase 7** | Sentinel - Proxy & Audit | ✅ Complete |
| **Phase 8** | Sentinel - Billing & Suspend Logic | ✅ Complete |
| **Phase 9** | Runtime Integration | ✅ Complete |
| **Phase 10** | E2E Infrastructure | ✅ Complete |
| **Phase 11** | Infrastructure Packaging | ✅ Complete |
| **Phase 12** | Production Hardening | 🔲 Not Started |
| **Phase 13** | Acceptance Testing | 🔲 Not Started |
| **Phase 14** | Documentation & Handoff | 🔲 Not Started |
| **Phase 15** | QLoRA Fine-Tuning | 🔲 Not Started |

**Last Updated:** January 2026

---

This document is the canonical, end-to-end implementation guide for the TrustBridge project. It merges the current intent of:

- plan.md (overall milestones)
- provider.md (optimized provider “skeleton image + secure pull” design)
- consumer.md (runtime + sidecar/sentinel behavior)

The goal is to make the system implementable by a coding agent without guesswork.

---

## 0) Canonical decisions (resolve inconsistencies)

### 0.1 Asset delivery: **NO baked weights**

- Model weights are typically 50GB+.
- Docker image size limits make “fat images” impractical.
- Canonical approach: **Small runtime image** + **Encrypted weights in Azure Blob Storage**.

### 0.2 Deployment target for 50GB+ models

Consumer.md mentions ACI/AKS; provider.md notes ACI is not a good fit.

Canonical guidance:

- For production-scale 50GB+ hydration using ephemeral NVMe and high throughput: prefer **GPU VM (or VMSS)** running containers.
- AKS is also viable (with GPU nodes and ephemeral storage), but is a larger infra commitment.
- ACI may be used only for **small demo assets**, not the 50GB+ path.

### 0.3 Crypto: require a streaming/chunked format

Plan.md and consumer.md mention “AES-GCM decrypt chunk-by-chunk”; provider.md correctly flags that single-shot AES-GCM is not a streaming format.

Canonical: **Chunked AES-256-GCM file format (`tbenc/v1`)** with per-chunk authentication and deterministic parsing.

This avoids CGO dependencies and can be implemented using:

- Python `cryptography` for provider encryption
- Go standard library `crypto/aes` + `crypto/cipher` for consumer decryption

---

## 1) System overview

### 1.1 Roles

- **Provider**: encrypts weights, uploads encrypted asset to Blob, registers key/metadata to Control Plane/EDC, publishes deployable package.
- **Consumer**: deploys package into their tenant; sentinel hydrates encrypted asset via SAS; decrypts to RAM/pipe and starts inference.
- **Control Plane / EDC**: validates contracts and issues short-lived SAS URLs (and returns key material under policy).
  - **NOTE**: The production Control Plane is an **external dependency** (already implemented in a separate project). This project only implements a **mock Control Plane** for E2E testing.

### 1.2 Runtime pattern

**Ambassador Sidecar pattern**:

- **Sentinel (sidecar)**: security, licensing, asset hydration, decryption, proxying, audit, billing.
- **Runtime (model server)**: vanilla inference server (vLLM/Triton). Binds to localhost only.

---

## 2) Repository layout (to be created)

This is the expected folder structure for implementation.

- `src/sentinel/` (Go 1.21+)
  - `cmd/sentinel/main.go`
  - `internal/config/` (env + flags)
  - `internal/license/` (EDC handshake)
  - `internal/asset/` (download + verify + local paths)
  - `internal/crypto/` (tbenc/v1 decrypt to FIFO)
  - `internal/proxy/` (reverse proxy + audit)
  - `internal/billing/` (metering client)
  - `internal/health/` (readiness/liveness)
  - `internal/state/` (state machine orchestration)
  - `internal/finetune/` (QLoRA fine-tuning - Phase 15)
    - `types.go` (data structures)
    - `adapter.go` (adapter CRUD)
    - `storage.go` (filesystem storage)
    - `manager.go` (job management)
    - `api.go` (HTTP handlers)
    - `data.go` (training data management)
    - `runtime.go` (adapter activation)
- `src/runtime/`
  - `entrypoint.sh` (waits for weights-ready signal)
  - `Dockerfile.runtime` (optional wrapper if needed)
  - `finetune/` (QLoRA training worker - Phase 15)
    - `Dockerfile.finetune`
    - `train.py`
    - `requirements.txt`
- `src/cli/` (Python 3.10+, Typer)
  - `trustbridge_cli/__init__.py`
  - `trustbridge_cli/main.py` (Typer app)
  - `trustbridge_cli/crypto_tbenc.py` (tbenc/v1 encrypt)
  - `trustbridge_cli/blob.py` (upload)
  - `trustbridge_cli/edc.py` (register asset + key)
  - `trustbridge_cli/build.py` (skeleton image build)
  - `trustbridge_cli/package.py` (managed app package)
- `infra/` (ARM/Bicep + templates)
  - `mainTemplate.json` or `main.bicep`
  - `createUiDefinition.json`
  - `cloud-init/` or `scripts/` (VM bootstrap)
- `e2e/` (end-to-end testing infrastructure)
  - `docker-compose.yml`
  - `data/` (test plaintext weights generation)
  - `artifacts/` (encrypted test files)
  - `controlplane-mock/` (mock authorization API for testing)
  - `blob-server/` (local HTTP file server with Range support)
  - `runtime-mock/` (FIFO reader + simple HTTP API)
  - `tests/` (automated E2E test suite)
- `docs/`
  - (optional)

---

## 3) Environment variables (canonical)

### 3.1 Sentinel

- `TB_CONTRACT_ID` (string, required)
- `TB_ASSET_ID` (string, required)
- `TB_EDC_ENDPOINT` (URL, required) – Control Plane/EDC endpoint
- `TB_TARGET_DIR` (string, default `/mnt/resource/trustbridge`) – encrypted download landing zone
- `TB_PIPE_PATH` (string, default `/dev/shm/model-pipe`) – FIFO path for plaintext streaming
- `TB_READY_SIGNAL` (string, default `/dev/shm/weights/ready.signal`) – runtime waits on this
- `TB_RUNTIME_URL` (URL, default `http://127.0.0.1:8081`) – where runtime listens
- `TB_PUBLIC_ADDR` (string, default `0.0.0.0:8000`) – sentinel public listener
- `TB_DOWNLOAD_CONCURRENCY` (int, default `4`)
- `TB_DOWNLOAD_CHUNK_BYTES` (int, default `8388608` = 8MiB)
- `TB_LOG_LEVEL` (string, default `info`)

#### Fine-tuning variables (Phase 15)

- `TB_FINETUNE_ENABLED` (bool, default `false`) – enable fine-tuning feature
- `TB_ADAPTERS_DIR` (string, default `/mnt/adapters`) – adapter storage path
- `TB_TRAINING_DATA_DIR` (string, default `/mnt/training`) – training data upload path
- `TB_MAX_ADAPTERS` (int, default `10`) – max adapters per deployment
- `TB_MAX_TRAINING_DATA_GB` (int, default `50`) – max training data size
- `TB_MAX_CONCURRENT_JOBS` (int, default `1`) – max concurrent training jobs
- `TB_DEFAULT_LORA_R` (int, default `16`) – default LoRA rank
- `TB_DEFAULT_LORA_ALPHA` (int, default `32`) – default LoRA alpha
- `TB_DEFAULT_EPOCHS` (int, default `3`) – default training epochs
- `TB_DEFAULT_BATCH_SIZE` (int, default `4`) – default batch size
- `TB_MAX_LORAS` (int, default `4`) – max LoRA adapters loaded in runtime
- `TB_MAX_LORA_RANK` (int, default `64`) – max LoRA rank supported

### 3.2 Runtime container

- Must bind to `127.0.0.1:8081`
- Must read model from `TB_PIPE_PATH` (FIFO) or from a RAM path if the chosen runtime cannot read from FIFO.

---

## 4) Control Plane / EDC APIs (contract + SAS)

**NOTE**: The production Control Plane is an **external dependency** (already implemented in a separate project). This section documents the API contract that the external Control Plane provides and that the Sentinel consumes. This project implements only a **mock Control Plane** for E2E testing purposes.

This is the minimal API surface the sentinel needs.

### 4.1 License + hydration authorization

`POST /api/v1/license/authorize`

Request JSON:

```json
{
  "contract_id": "contract-123",
  "asset_id": "tb-asset-123",
  "hw_id": "<hardware-FINGERPRINT>",
  "attestation": "<optional_azure_attestation_or_imds_token>",
  "client_version": "sentinel/0.1.0"
}
```

Response JSON (success):

```json
{
  "status": "authorized",
  "sas_url": "https://.../model.tbenc?sv=...&sig=...",
  "manifest_url": "https://.../model.manifest.json?sv=...&sig=...",
  "decryption_key_hex": "<64 hex chars>",
  "expires_at": "2026-01-07T12:00:00Z"
}
```

Response JSON (denied):

```json
{
  "status": "denied",
  "reason": "subscription_inactive"
}
```

Transport requirements:

- HTTPS
- Prefer mTLS between sentinel and control plane

---

## 5) Asset format: tbenc/v1 (Chunked AES-256-GCM)

This is the critical interoperability contract between provider encryption and consumer decryption.

### 5.1 Key

- 32 bytes random (AES-256)
- Stored in EDC/Vault associated with `asset_id`

### 5.2 Nonce derivation

To avoid storing a nonce per chunk, derive it as:

- `nonce_prefix`: 4 random bytes stored in file header
- `counter`: uint64 chunk index, big-endian
- `nonce`: `nonce_prefix (4) || counter (8)` = 12 bytes

Nonce uniqueness is guaranteed if:

- prefix is random per file
- counter is never reused

### 5.3 Header (binary)

All multi-byte integers are big-endian.

- `magic` (8 bytes): ASCII `TBENC001`
- `version` (uint16): `1`
- `algo` (uint8): `1` (meaning `AES-256-GCM-CHUNKED`)
- `chunk_bytes` (uint32): recommended 4MiB–16MiB
- `nonce_prefix` (4 bytes)
- `reserved` (13 bytes): zero (pads header to 32 bytes)

Header total: 8 + 2 + 1 + 4 + 4 + 13 = 32 bytes.

### 5.4 Record format (repeated until EOF)

- `pt_len` (uint32)
- `ct_and_tag` (`pt_len + 16` bytes) where 16 is the GCM tag length

Associated data (AAD) for each chunk must be:

- `magic||version||algo||chunk_bytes||nonce_prefix||chunk_index||pt_len`

Where `chunk_index` is uint64 big-endian.

### 5.5 Manifest (JSON)

Store as a separate blob alongside the ciphertext.

Required fields:

```json
{
  "format": "tbenc/v1",
  "algo": "aes-256-gcm-chunked",
  "chunk_bytes": 4194304,
  "plaintext_bytes": 53821440000,
  "sha256_ciphertext": "...",
  "asset_id": "tb-asset-123",
  "weights_filename": "model.tbenc",
  "allow_finetune": true
}
```

Note: `allow_finetune` (Phase 15) controls whether consumers can fine-tune this model. Default is `true`.

Integrity checks required by sentinel:

- After download, compute sha256 over `model.tbenc` and match `sha256_ciphertext`.

---

## 6) Provider implementation (CLI)

### 6.1 CLI commands (minimal)

1) `trustbridge encrypt`

- Input: raw weights (`.safetensors`)
- Output:
  - `model.tbenc`
  - `model.manifest.json`
  - Prints `asset_id` (or accepts user-specified)

Behavior:

- Generate AES-256 key (32 bytes)
- Generate nonce_prefix (4 bytes)
- Stream-read input file in `chunk_bytes`
- For each chunk:
  - compute nonce from prefix + counter
  - AES-GCM Seal with AAD
  - write record
- Track ciphertext sha256 while writing
- Write manifest

2) `trustbridge upload`

- Upload `model.tbenc` and `model.manifest.json` to Azure Blob Storage
- Container must be private
- Blob paths:
  - `models/<asset_id>/model.tbenc`
  - `models/<asset_id>/model.manifest.json`

3) `trustbridge register`

- Register in EDC/Control Plane:
  - `asset_id`
  - blob URLs (without SAS)
  - `sha256_ciphertext`
  - size fields
  - store `decryption_key` in Vault

4) `trustbridge build`

- Build and push skeleton image:
  - Base runtime (vLLM)
  - Sentinel binary
  - Optional azcopy
- Must not include weights

5) `trustbridge package`

- Generate Azure Managed App package templates with parameters:
  - `TB_ASSET_ID`
  - `TB_EDC_ENDPOINT`
  - `TB_CONTRACT_ID` input at deploy time

6) `trustbridge publish`

- Orchestrates steps: encrypt → upload → register → build → package → publish

### 6.2 Python dependencies

- `cryptography` (AES-GCM)
- `typer` (CLI)
- `azure-identity` and `azure-storage-blob` (upload)
- `requests` (EDC/control plane API)

---

## 7) Consumer implementation (Sentinel)

Sentinel is the critical component. It must:

1) start in “lockdown” (do not forward traffic)
2) authorize with control plane
3) hydrate encrypted weights
4) decrypt to FIFO/volatile memory
5) signal runtime
6) proxy traffic, audit, bill

### 7.1 Sentinel startup state machine

States:

- `Boot`
- `Authorize`
- `Hydrate` (download + verify)
- `Decrypt` (start decrypt writer)
- `Ready` (open proxy)
- `Suspended` (billing/contract failure kill switch)

Transitions:

- Boot → Authorize: on start
- Authorize → Hydrate: if authorized
- Authorize → Suspended: if denied
- Hydrate → Decrypt: download success + hash verified
- Decrypt → Ready: FIFO created + runtime ready signal written

### 7.2 Hardware fingerprint

Minimum viable fingerprint:

- Read `/sys/class/dmi/id/product_uuid` when available
- Fallback: combine hostname + instance-id from Azure IMDS (if present)

### 7.3 License / authorize client

- Call `POST /api/v1/license/authorize`
- Handle:
  - retries with exponential backoff
  - 401/403 as terminal denial
  - expiry time; refresh before expiry if runtime continues

### 7.4 Downloader

Inputs:

- `sas_url` for model.tbenc
- `manifest_url` (or download manifest first)

Behavior:

- Ensure `TB_TARGET_DIR` exists
- Download manifest JSON
- Download ciphertext to `${TB_TARGET_DIR}/model.tbenc`
  - Use concurrent HTTP range requests
  - Each worker downloads a byte-range into the correct offset
  - Retry on transient failures
- Compute sha256 of downloaded file and compare to manifest

### 7.5 Decrypter: tbenc/v1 → FIFO

Behavior:

- Create FIFO at `TB_PIPE_PATH` with 0600
- Start a goroutine that:
  - opens FIFO for write (note: will block until reader opens)
  - opens `model.tbenc`
  - reads and validates header
  - loops over chunk records:
    - parse `pt_len` and read `ct` bytes
    - derive nonce
    - decrypt with AES-GCM using AAD
    - write plaintext bytes to FIFO
    - best-effort overwrite plaintext buffer after write

Ready signal:

- Once FIFO exists AND decrypt goroutine is running, create `TB_READY_SIGNAL` file.

### 7.6 Runtime wrapper

- Runtime startup script waits for `TB_READY_SIGNAL`
- Runtime must listen on `127.0.0.1:8081`
- Runtime must load model from FIFO path if supported.

If chosen runtime cannot read from FIFO:

- Sentinel decrypts into a RAM-backed file (e.g., `/dev/shm/weights/decrypted-model`) instead of FIFO.
- This still satisfies “no disk plaintext” as long as `/dev/shm` is tmpfs.

### 7.7 Reverse proxy + audit

- Sentinel listens on `0.0.0.0:8000`
- For each request:
  - optional auth check (`Authorization: Bearer`)
  - compute `sha256(request_body)`
  - forward to runtime at `127.0.0.1:8081`
  - compute `sha256(response_body)` (optional)
  - append audit log entry to an internal queue

Audit log schema (minimum):

```json
{
  "ts": "2026-01-07T10:00:00Z",
  "contract_id": "contract-123",
  "asset_id": "tb-asset-123",
  "method": "POST",
  "path": "/v1/chat/completions",
  "req_sha256": "...",
  "status": 200,
  "latency_ms": 123
}
```

### 7.8 Billing agent

- Runs every 60s
- Reads token usage counter
- Sends metering event to Azure Marketplace Metering API
- If metering fails with “disabled/quota exceeded” → transition to `Suspended`
  - proxy returns `403 Payment Required` (or `402` if you choose)

Note: exact token counting depends on the inference API. Minimum viable approach:

- Count bytes in request/response as a proxy metric, or
- Count requests, or
- Parse known response fields if using OpenAI-compatible vLLM.

---

## 8) Infrastructure (Azure Managed App)

### 8.1 Minimal production-like path (GPU VM)

Managed App deploys:

- 1 GPU VM size parameter (e.g., `Standard_NC24ads_A100_v4`)
- attaches system-managed identity (for metering / optional control plane auth)
- bootstraps Docker + runs a compose with:
  - `sentinel` container (port 8000 exposed)
  - `runtime` container (port 8081 bound localhost)

Key host paths:

- `/mnt/resource/trustbridge` must exist (ephemeral)
- `/dev/shm` available for RAM-backed artifacts

### 8.2 Container networking requirement

- Runtime must not be reachable publicly.
- Enforce runtime bind to `127.0.0.1`.
- Sentinel is the only externally exposed surface.

---

## 9) Acceptance checks (definition of “done”)

### 9.1 Crypto interop

- Provider encrypts a test file (e.g., 50MB)
- Sentinel decrypts to FIFO and the reconstructed plaintext matches original sha256

### 9.2 No plaintext on disk

- During runtime, verify no plaintext weights exist under `/mnt/resource`
- Plaintext exists only in FIFO/tmpfs or GPU VRAM

### 9.3 Contract gating

- If authorize endpoint returns denied → sentinel never opens port 8000

### 9.4 SAS expiry handling

- If download is mid-flight and SAS expires:
  - sentinel requests a new SAS and resumes/retries download

### 9.5 Runtime isolation

- Confirm runtime port is not reachable externally

---

## 10) Implementation order (recommended)

This section provides a detailed, step-by-step implementation plan that covers all components required to deliver a working TrustBridge system. Each phase builds on previous work and can be validated independently.

### Phase 1: Crypto Foundation (Days 1-3)

**Goal**: Establish cryptographic interoperability between provider and consumer.

1. **Implement `tbenc/v1` encryption (Python)**
   - File: `src/cli/trustbridge_cli/crypto_tbenc.py`
   - Functions: `generate_key()`, `encrypt_file()`, `write_manifest()`
   - Unit tests with known test vectors

2. **Implement `tbenc/v1` decryption (Go)**
   - File: `src/sentinel/internal/crypto/decrypt.go`
   - Functions: `ParseHeader()`, `DecryptChunk()`, `DecryptToWriter()`
   - Unit tests with same test vectors as Python

3. **Crypto interop validation**
   - Cross-language test: Python encrypts → Go decrypts → verify hash match
   - Test with multiple chunk sizes (1MB, 4MB, 16MB)
   - Test with edge cases (empty file, single byte, chunk-aligned, non-aligned)

**Acceptance**: Plaintext hash after decrypt matches original plaintext hash.

---

### Phase 2: Provider CLI - Core Commands (Days 4-7)

**Goal**: Enable providers to prepare and publish encrypted model packages.

4. **Implement `trustbridge encrypt` command**
   - File: `src/cli/trustbridge_cli/main.py`
   - Calls crypto_tbenc.encrypt_file()
   - CLI args: `--in`, `--out`, `--manifest`, `--asset-id`, `--chunk-bytes`
   - Outputs: `model.tbenc`, `model.manifest.json`, decryption key (hex)

5. **Implement `trustbridge upload` command**
   - File: `src/cli/trustbridge_cli/blob.py`
   - Uses Azure SDK: `azure-storage-blob`
   - Uploads to `models/<asset_id>/model.tbenc` and `model.manifest.json`
   - CLI args: `--storage-account`, `--container`, `--asset-id`, `--encrypted-file`, `--manifest`
   - Requires: Azure credentials (DefaultAzureCredential)

6. **Implement `trustbridge register` command**
   - File: `src/cli/trustbridge_cli/edc.py`
   - Calls external Control Plane API (production endpoint)
   - Registers: asset_id, blob URLs (no SAS), sha256, size, decryption key
   - CLI args: `--edc-endpoint`, `--asset-id`, `--manifest`, `--key-hex`
   - Note: Talks to **external production Control Plane**, not the mock

7. **Implement `trustbridge build` command**
   - File: `src/cli/trustbridge_cli/build.py`
   - Builds Docker image with:
     - Base: `nvcr.io/nvidia/vllm:latest` or similar
     - Adds: compiled sentinel binary
     - Adds: runtime entrypoint.sh
   - Tags and pushes to ACR
   - CLI args: `--registry`, `--image-name`, `--tag`, `--sentinel-binary`

8. **Implement `trustbridge package` command**
   - File: `src/cli/trustbridge_cli/package.py`
   - Generates Azure Managed App package structure
   - Outputs: ZIP with mainTemplate.json, createUiDefinition.json, metadata
   - Parameterizes: `TB_ASSET_ID`, `TB_EDC_ENDPOINT`, `TB_CONTRACT_ID`
   - CLI args: `--asset-id`, `--image`, `--output-zip`

9. **Implement `trustbridge publish` command (orchestration)**
   - Calls: encrypt → upload → register → build → package
   - Validates each step before proceeding
   - CLI args: aggregate of all above commands
   - Prints summary with blob URLs, image tag, package path

**Acceptance**: Run `trustbridge publish` end-to-end and verify all artifacts are created.

---

### Phase 3: Sentinel - Authorization & Fingerprinting (Days 8-10)

**Goal**: Sentinel can identify itself and request hydration authorization.

10. **Implement hardware fingerprinting**
    - File: `src/sentinel/internal/license/fingerprint.go`
    - Function: `GenerateHardwareID()` → string
    - Logic:
      - Try `/sys/class/dmi/id/product_uuid`
      - Fallback: hostname + Azure IMDS instance ID
    - Unit tests with mocked filesystem and IMDS

11. **Implement Control Plane authorize client**
    - File: `src/sentinel/internal/license/client.go`
    - Function: `Authorize(contractID, assetID string) (*AuthResponse, error)`
    - Calls: `POST /api/v1/license/authorize`
    - Features:
      - Exponential backoff retry (3 attempts)
      - Timeout: 30s per request
      - Parse JSON response (status, sas_url, manifest_url, decryption_key_hex, expires_at)
      - Handle 401/403 as terminal denial
    - Unit tests with mock HTTP server

12. **Implement config management**
    - File: `src/sentinel/internal/config/config.go`
    - Loads from environment variables (Section 3.1)
    - Validates required fields
    - Provides defaults

**Acceptance**: Sentinel can call mock authorize endpoint and parse response.

---

### Phase 4: Sentinel - Asset Hydration (Days 11-14)

**Goal**: Sentinel can download encrypted assets using SAS URLs.

13. **Implement manifest downloader**
    - File: `src/sentinel/internal/asset/manifest.go`
    - Function: `DownloadManifest(url string) (*Manifest, error)`
    - Parses JSON and validates required fields

14. **Implement single-threaded asset downloader**
    - File: `src/sentinel/internal/asset/download.go`
    - Function: `DownloadFile(url, outputPath string) error`
    - Simple HTTP GET with progress logging
    - Unit test with local HTTP server

15. **Add concurrent range request downloader**
    - Enhance `download.go` with `DownloadFileConcurrent()`
    - Uses HTTP Range headers: `bytes=start-end`
    - Worker pool (configurable concurrency, default 4)
    - Each worker writes to correct file offset
    - Retry transient failures per-range
    - Unit test with range-supporting HTTP server

16. **Implement integrity verification**
    - Function: `VerifyFileHash(filePath, expectedSHA256 string) error`
    - Computes SHA256 of downloaded ciphertext
    - Compares to manifest `sha256_ciphertext`
    - Fails hydration if mismatch

**Acceptance**: Download 100MB test file using 4 concurrent ranges; verify hash.

---

### Phase 5: Sentinel - Decryption & Ready Signal (Days 15-17)

**Goal**: Sentinel decrypts to FIFO/tmpfs and signals runtime.

17. **Implement FIFO creation**
    - File: `src/sentinel/internal/crypto/fifo.go`
    - Function: `CreateFIFO(path string) error`
    - Uses `syscall.Mkfifo()` with mode 0600
    - Handle "already exists" gracefully

18. **Implement streaming decrypter**
    - File: `src/sentinel/internal/crypto/decrypt_stream.go`
    - Function: `DecryptToFIFO(encryptedPath, fifoPath string, key []byte) error`
    - Runs in goroutine (non-blocking)
    - Opens FIFO for write (blocks until runtime reads)
    - Decrypts chunk-by-chunk to FIFO
    - Securely overwrites plaintext buffers after write
    - Logs progress every 10%

19. **Implement ready signal writer**
    - File: `src/sentinel/internal/crypto/signal.go`
    - Function: `WriteReadySignal(path string) error`
    - Creates empty file or writes timestamp JSON
    - Called after FIFO exists and decrypt goroutine is running

20. **Test FIFO end-to-end locally**
    - Script: `scripts/test-fifo.sh`
    - Encrypts test file, starts decrypter, reads from FIFO in parallel
    - Verifies plaintext hash

**Acceptance**: 1GB encrypted file decrypted to FIFO; reader consumes and verifies hash.

---

### Phase 6: Sentinel - State Machine & Health (Days 18-20)

**Goal**: Orchestrate startup phases and expose health endpoints.

21. **Implement state machine**
    - File: `src/sentinel/internal/state/machine.go`
    - States: Boot, Authorize, Hydrate, Decrypt, Ready, Suspended
    - Transitions: defined in Section 7.1
    - Methods: `Transition(newState)`, `CurrentState()`, thread-safe
    - Logging at each transition

22. **Implement orchestration logic**
    - File: `src/sentinel/cmd/sentinel/main.go`
    - Startup sequence:
      - Boot → load config
      - Authorize → call license API
      - Hydrate → download manifest + ciphertext + verify
      - Decrypt → create FIFO + start decrypter + write ready signal
      - Ready → start proxy
    - Handle failures → transition to Suspended and exit (or block)

23. **Implement health endpoints**
    - File: `src/sentinel/internal/health/http.go`
    - Endpoints:
      - `GET /health` → 200 if state == Ready, 503 otherwise
      - `GET /readiness` → 200 if state >= Decrypt
      - `GET /status` → JSON with current state, asset_id, uptime
    - Runs on separate port (e.g., 8001) or same port before Ready

**Acceptance**: Start sentinel; observe state transitions in logs; `/health` returns 503 until Ready.

---

### Phase 7: Sentinel - Proxy & Audit (Days 21-23)

**Goal**: Sentinel proxies traffic and logs audit trail.

24. **Implement reverse proxy**
    - File: `src/sentinel/internal/proxy/proxy.go`
    - Uses `httputil.ReverseProxy`
    - Forwards requests to `TB_RUNTIME_URL` (default `http://127.0.0.1:8081`)
    - Listens on `TB_PUBLIC_ADDR` (default `0.0.0.0:8000`)
    - Only starts when state == Ready

25. **Implement audit logging**
    - File: `src/sentinel/internal/proxy/audit.go`
    - Middleware that captures:
      - Timestamp, method, path, status, latency
      - Request SHA256 (body hash)
      - Optional: response SHA256
      - contract_id, asset_id
    - Writes to in-memory ring buffer or append-only log file
    - Format: JSON lines

26. **Optional: Add request authentication**
    - Middleware: check `Authorization: Bearer <token>`
    - Validates token against allow-list or calls external API
    - Returns 401 if missing/invalid

**Acceptance**: Send request through sentinel; verify it reaches runtime mock; check audit log.

---

### Phase 8: Sentinel - Billing & Suspend Logic (Days 24-25)

**Goal**: Implement metering and contract kill-switch.

27. **Implement billing counter**
    - File: `src/sentinel/internal/billing/counter.go`
    - Tracks: request count, total bytes in/out, or token count (if parseable)
    - Thread-safe increment

28. **Implement billing agent (stub)**
    - File: `src/sentinel/internal/billing/agent.go`
    - Runs every 60s
    - Reads counter, logs usage (no actual API call yet)
    - Resets counter after report

29. **Integrate Azure Marketplace Metering API**
    - Calls: `POST https://marketplaceapi.microsoft.com/api/usageEvent`
    - Sends: resourceId, quantity, dimension, effectiveStartTime
    - Handles errors:
      - If "quota exceeded" or "subscription inactive" → transition state to Suspended
    - Requires: Azure managed identity or credentials

30. **Implement Suspended enforcement**
    - When state == Suspended:
      - Proxy returns `403 Forbidden` or `402 Payment Required`
      - Health returns 503
      - Optionally: close listener and exit

**Acceptance**: Simulate billing failure; verify state transitions to Suspended and proxy blocks traffic.

---

### Phase 9: Runtime Integration (Days 26-27)

**Goal**: Runtime wrapper waits for weights and starts inference server.

31. **Implement runtime entrypoint.sh**
    - File: `src/runtime/entrypoint.sh`
    - Logic:
      ```bash
      while [ ! -f "$TB_READY_SIGNAL" ]; do sleep 1; done
      echo "Weights ready, starting runtime..."
      vllm serve --model "$TB_PIPE_PATH" --host 127.0.0.1 --port 8081
      ```
    - Handles: timeout after N seconds if signal never appears

32. **Build runtime Docker image**
    - File: `src/runtime/Dockerfile.runtime`
    - Base: vLLM or Triton
    - Copies entrypoint.sh
    - Sets CMD to run entrypoint.sh

33. **Create runtime-mock for E2E**
    - File: `e2e/runtime-mock/app.py` (Python Flask or Go HTTP server)
    - Reads from `$TB_PIPE_PATH` in background thread
    - Computes SHA256 of plaintext read from FIFO
    - Exposes endpoints:
      - `GET /health` → 200
      - `POST /v1/demo` → echo request + plaintext hash
      - `GET /plaintext-hash` → returns hash of data read from FIFO
    - Binds to `127.0.0.1:8081`

**Acceptance**: Start sentinel + runtime-mock locally; runtime-mock receives decrypted plaintext via FIFO.

---

### Phase 10: E2E Infrastructure (Days 28-32)

**Goal**: Implement all E2E demo components per Section 11.

34. **Create test data generation script**
    - File: `e2e/data/generate.py`
    - Generates deterministic plaintext (e.g., repeating pattern)
    - Size: configurable (default 16MB)
    - Outputs: `e2e/data/plain.weights` + prints SHA256

35. **Implement E2E blob server**
    - File: `e2e/blob-server/server.go` or `server.py`
    - Serves `e2e/artifacts/` directory
    - Supports HTTP Range requests
    - Listens on port 9000 (configurable)
    - Logs all requests

36. **Implement E2E control plane mock**
    - File: `e2e/controlplane-mock/server.go` or `server.py`
    - Implements: `POST /api/v1/license/authorize`
    - Logic:
      - If `contract_id == "contract-allow"` → return authorized response
      - Else → return denied response
    - Returns:
      - `sas_url`: `http://blob-server:9000/artifacts/model.tbenc`
      - `manifest_url`: `http://blob-server:9000/artifacts/model.manifest.json`
      - `decryption_key_hex`: reads from env or config file
      - `expires_at`: current_time + 1 hour
    - Listens on port 8080

37. **Create docker-compose.yml**
    - File: `e2e/docker-compose.yml`
    - Services:
      - `blob-server` (port 9000)
      - `controlplane` (port 8080)
      - `sentinel` (port 8000, depends on blob + controlplane)
      - `runtime-mock` (port 8081 localhost-only, network mode: service:sentinel or shared network)
    - Volumes:
      - `e2e/artifacts` → blob-server
      - `/dev/shm` → sentinel (for FIFO)
      - ephemeral tmpfs for `TB_TARGET_DIR`
    - Environment variables for sentinel (Section 3.1)

38. **Implement E2E test suite**
    - File: `e2e/tests/test_e2e.py` (pytest)
    - Tests:
      - **test_authorization_deny**: Start with `contract-deny`, verify sentinel doesn't reach Ready
      - **test_download_integrity**: Modify ciphertext, verify sentinel fails hydration
      - **test_decrypt_interop**: Verify plaintext hash from runtime-mock matches original
      - **test_no_plaintext_on_disk**: Check `TB_TARGET_DIR` contains only `.tbenc` files
      - **test_proxy_forwarding**: Send request to `:8000`, verify runtime-mock receives it
      - **test_audit_log**: Verify audit JSON is produced
      - **test_runtime_isolation**: Verify `:8081` is not reachable from host
    - Utilities: docker-compose up/down, wait for health endpoints, file inspection

39. **Create E2E Makefile targets**
    - File: `Makefile` or `e2e/Makefile`
    - Targets:
      - `make e2e-generate-plain`
      - `make e2e-encrypt` (calls trustbridge encrypt)
      - `make e2e-up` (docker compose up)
      - `make e2e-test` (run pytest suite)
      - `make e2e-down` (cleanup)
      - `make e2e` (all steps: generate → encrypt → up → test)

**Acceptance**: Run `make e2e` on developer machine; all tests pass.

---

### Phase 11: Infrastructure Packaging (Days 33-36)

**Goal**: Create Azure Managed App deployment templates.

40. **Create mainTemplate.json (ARM) or main.bicep**
    - File: `infra/mainTemplate.json`
    - Resources:
      - GPU VM (parameterized size, e.g., `Standard_NC24ads_A100_v4`)
      - System-assigned managed identity
      - NSG: allow 8000 inbound, block 8081
      - Public IP (optional, can use private endpoint)
      - OS disk + ephemeral disk config
    - Outputs: VM public IP, FQDN

41. **Create createUiDefinition.json**
    - File: `infra/createUiDefinition.json`
    - Parameters:
      - VM size (dropdown)
      - Contract ID (textbox)
      - Asset ID (hidden or readonly, pre-filled)
      - EDC endpoint (hidden or readonly, pre-filled)
      - Region (dropdown)
    - Validation: contract ID required

42. **Write cloud-init script**
    - File: `infra/cloud-init/init.sh` or embedded in ARM template
    - Steps:
      - Install Docker + nvidia-docker2
      - Create `/mnt/resource/trustbridge` (ephemeral storage)
      - Pull sentinel + runtime images from ACR
      - Write docker-compose.yml or run containers directly
      - Set environment variables from ARM template parameters
      - Start containers
    - Logging: send to Azure Monitor or serial console

43. **Configure networking**
    - NSG rules:
      - Allow TCP 8000 from Internet (or restrict to specific IPs)
      - Block TCP 8081 from Internet
      - Allow SSH (port 22) for debugging (optional, can disable in production)
    - Docker networking:
      - Runtime container: `network_mode: host` with bind to `127.0.0.1:8081`
      - OR: custom bridge network with no external exposure for runtime

44. **Test Managed App deployment**
    - Create Azure dev subscription test
    - Deploy Managed App package
    - Verify VM provisions, containers start, sentinel reaches Ready
    - Send test request to public IP:8000
    - SSH to VM and verify:
      - No plaintext in `/mnt/resource/trustbridge`
      - Port 8081 not reachable externally
      - Logs show successful hydration

**Acceptance**: Deploy Managed App in Azure; perform smoke test; verify all security invariants.

---

### Phase 12: Production Hardening (Days 37-40)

**Goal**: Add production-required features and security.

45. **Implement SAS expiry retry logic**
    - File: `src/sentinel/internal/asset/download.go`
    - Behavior:
      - If download returns 403 or 401 mid-flight → assume SAS expired
      - Call `Authorize()` again to get new SAS
      - Resume download from last successful byte offset
    - Backoff: exponential up to 5 retries
    - Test: mock server that expires SAS after 10 seconds

46. **Add mTLS support for Control Plane**
    - File: `src/sentinel/internal/license/client.go`
    - Load client certificate from env: `TB_CLIENT_CERT_PATH`, `TB_CLIENT_KEY_PATH`
    - Configure `http.Client` with `tls.Config`
    - Validate server certificate
    - Optional: mutual authentication

47. **Implement secure buffer overwrite**
    - File: `src/sentinel/internal/crypto/secure.go`
    - Function: `ZeroBytes(buf []byte)` → overwrites with random data or zeros
    - Call after writing plaintext chunks to minimize memory exposure
    - Note: Not foolproof (Go GC may copy), but reduces risk

48. **Add graceful shutdown**
    - File: `src/sentinel/cmd/sentinel/main.go`
    - Handle SIGTERM/SIGINT:
      - Stop accepting new requests
      - Finish in-flight requests (30s timeout)
      - Flush audit logs
      - Clean up FIFO
      - Exit cleanly

49. **Security audit checklist**
    - [ ] No plaintext on disk (only in tmpfs/FIFO)
    - [ ] Runtime port 8081 not externally reachable
    - [ ] SAS URLs not logged
    - [ ] Decryption key not logged
    - [ ] FIFO permissions 0600
    - [ ] Sensitive env vars redacted from logs
    - [ ] Error messages don't leak internal paths

**Acceptance**: Run security audit; all items pass; penetration test attempt to access runtime fails.

---

### Phase 13: Acceptance Testing (Days 41-42)

**Goal**: Validate all acceptance criteria from Section 9.

50. **Run acceptance check 9.1: Crypto interop**
    - Provider encrypts 50MB test file
    - Sentinel decrypts to FIFO
    - Verify SHA256 match

51. **Run acceptance check 9.2: No plaintext on disk**
    - During runtime, inspect filesystem
    - Confirm no plaintext under `/mnt/resource`
    - Confirm plaintext only in FIFO/tmpfs or GPU VRAM

52. **Run acceptance check 9.3: Contract gating**
    - Deploy with denied contract
    - Verify sentinel never opens port 8000 (or returns 403)

53. **Run acceptance check 9.4: SAS expiry handling**
    - Simulate SAS expiry mid-download
    - Verify sentinel re-authorizes and completes download

54. **Run acceptance check 9.5: Runtime isolation**
    - Attempt to connect to runtime port from external host
    - Verify connection refused or timeout

**Acceptance**: All 5 acceptance checks pass in Azure production-like environment.

---

### Phase 14: Documentation & Handoff (Days 43-45)

**Goal**: Document deployment and usage for providers and consumers.

55. **Write provider guide**
    - File: `docs/provider-guide.md`
    - Topics:
      - How to prepare model weights
      - Running `trustbridge publish`
      - Uploading to Azure Marketplace
      - Registering with Control Plane

56. **Write consumer deployment guide**
    - File: `docs/consumer-guide.md`
    - Topics:
      - Purchasing from Azure Marketplace
      - Deploying Managed App
      - Providing contract ID
      - Accessing inference endpoint
      - Monitoring and logs

57. **Write E2E demo README**
    - File: `e2e/README.md`
    - Quick start: `make e2e`
    - Architecture diagram
    - Troubleshooting common issues

58. **Create architecture diagram**
    - File: `docs/architecture.png` or `.svg`
    - Shows: Provider, Blob, Control Plane, Consumer (Sentinel + Runtime)
    - Data flows: encrypted weights, SAS URLs, inference requests

**Acceptance**: New team member can run E2E demo and deploy to Azure using only the documentation.

---

### Phase 15: QLoRA Fine-Tuning on Consumer Side (Days 46-60)

**Goal**: Enable consumers to fine-tune deployed models using QLoRA while maintaining security guarantees.

#### 15.1 Design Decisions

| Decision | Choice | Rationale |
|----------|--------|-----------|
| **Adapter Export** | No - in-system only | Adapters stay within TrustBridge deployment |
| **Compute Model** | Same GPU (time-sharing) | Simpler, lower cost - inference pauses during training |
| **Provider Control** | Yes - providers can disable | `allow_finetune: false` in asset metadata |
| **Quantization** | QLoRA 4-bit only | Maximum memory efficiency |
| **Training Data Format** | JSONL | Standard format for instruction tuning |

#### 15.2 New Repository Structure

```
src/
├── sentinel/
│   └── internal/
│       └── finetune/           # NEW: Fine-tuning orchestration
│           ├── types.go        # Data structures
│           ├── adapter.go      # Adapter CRUD operations
│           ├── storage.go      # Filesystem adapter storage
│           ├── manager.go      # Job queue management
│           ├── api.go          # HTTP handlers
│           └── errors.go       # Fine-tuning errors
│
├── runtime/
│   └── finetune/               # NEW: Fine-tuning worker
│       ├── Dockerfile.finetune # Training container
│       ├── train.py            # QLoRA training script
│       └── requirements.txt    # Python dependencies
```

#### 15.3 New Environment Variables

```bash
# Feature flag
TB_FINETUNE_ENABLED=true

# Storage paths
TB_ADAPTERS_DIR=/mnt/adapters
TB_TRAINING_DATA_DIR=/mnt/training

# Limits
TB_MAX_ADAPTERS=10
TB_MAX_TRAINING_DATA_GB=50
TB_MAX_CONCURRENT_JOBS=1

# Training defaults
TB_DEFAULT_LORA_R=16
TB_DEFAULT_LORA_ALPHA=32
TB_DEFAULT_EPOCHS=3
TB_DEFAULT_BATCH_SIZE=4

# Runtime LoRA config
TB_MAX_LORAS=4
TB_MAX_LORA_RANK=64
```

#### 15.4 API Endpoints

```
POST   /v1/finetune/jobs              # Create fine-tuning job
GET    /v1/finetune/jobs              # List jobs
GET    /v1/finetune/jobs/{id}         # Get job status
DELETE /v1/finetune/jobs/{id}         # Cancel job

POST   /v1/finetune/data              # Upload training data (JSONL)
GET    /v1/finetune/data              # List training datasets
DELETE /v1/finetune/data/{id}         # Delete training data

GET    /v1/adapters                   # List adapters
GET    /v1/adapters/{id}              # Get adapter details (metadata only)
DELETE /v1/adapters/{id}              # Delete adapter
POST   /v1/adapters/{id}/activate     # Load adapter into runtime
POST   /v1/adapters/{id}/deactivate   # Unload adapter
```

#### 15.5 Implementation Tasks

59. **Add `allow_finetune` to provider manifest**
    - File: `src/cli/trustbridge_cli/commands/encrypt.py`
    - Add `--allow-finetune` flag (default: true)
    - Include in manifest JSON: `"allow_finetune": true`
    - Update manifest parsing in sentinel

60. **Implement adapter data types**
    - File: `src/sentinel/internal/finetune/types.go`
    - Structures:
      - `AdapterManifest`: adapter_id, base_asset_id, lora_config, training_config, metrics, files, sha256
      - `LoRAConfig`: r, lora_alpha, target_modules, lora_dropout
      - `TrainingConfig`: epochs, learning_rate, batch_size, warmup_steps, max_seq_length
      - `Job`: id, status, training_data_id, config, created_at, completed_at, error
      - `TrainingDataset`: id, filename, size_bytes, samples, uploaded_at

61. **Implement adapter storage**
    - File: `src/sentinel/internal/finetune/storage.go`
    - Functions:
      - `NewAdapterStore(basePath string) *AdapterStore`
      - `Save(adapter *AdapterManifest) error`
      - `Load(adapterID string) (*AdapterManifest, error)`
      - `List() ([]*AdapterManifest, error)`
      - `Delete(adapterID string) error`
    - Storage format: `{TB_ADAPTERS_DIR}/{adapter_id}/adapter_manifest.json`

62. **Implement adapter CRUD operations**
    - File: `src/sentinel/internal/finetune/adapter.go`
    - Functions:
      - `CreateAdapter(manifest *AdapterManifest) error`
      - `GetAdapter(id string) (*AdapterManifest, error)`
      - `ListAdapters() ([]*AdapterManifest, error)`
      - `DeleteAdapter(id string) error`
      - `ValidateAdapter(path string) error`

63. **Implement fine-tuning job manager**
    - File: `src/sentinel/internal/finetune/manager.go`
    - Functions:
      - `NewManager(config *Config, manifest *asset.Manifest) *Manager`
      - `CreateJob(req *JobRequest) (*Job, error)` - validates provider allows fine-tuning
      - `GetJob(id string) (*Job, error)`
      - `ListJobs() ([]*Job, error)`
      - `CancelJob(id string) error`
      - `ExecuteJob(job *Job) error` - launches training worker
    - Job queue: in-memory with persistence to disk
    - Max concurrent jobs: 1 (same GPU as inference)

64. **Implement training data management**
    - File: `src/sentinel/internal/finetune/data.go`
    - Functions:
      - `UploadTrainingData(filename string, reader io.Reader) (*TrainingDataset, error)`
      - `ValidateJSONL(path string) (int, error)` - returns sample count
      - `GetTrainingData(id string) (*TrainingDataset, error)`
      - `ListTrainingData() ([]*TrainingDataset, error)`
      - `DeleteTrainingData(id string) error`
    - Storage: `{TB_TRAINING_DATA_DIR}/{data_id}/data.jsonl`
    - Size limit: `TB_MAX_TRAINING_DATA_GB`

65. **Implement fine-tuning HTTP API**
    - File: `src/sentinel/internal/finetune/api.go`
    - Handlers:
      - `HandleCreateJob(w, r)` - POST /v1/finetune/jobs
      - `HandleListJobs(w, r)` - GET /v1/finetune/jobs
      - `HandleGetJob(w, r)` - GET /v1/finetune/jobs/{id}
      - `HandleCancelJob(w, r)` - DELETE /v1/finetune/jobs/{id}
      - `HandleUploadData(w, r)` - POST /v1/finetune/data (multipart)
      - `HandleListData(w, r)` - GET /v1/finetune/data
      - `HandleDeleteData(w, r)` - DELETE /v1/finetune/data/{id}
      - `HandleListAdapters(w, r)` - GET /v1/adapters
      - `HandleGetAdapter(w, r)` - GET /v1/adapters/{id}
      - `HandleDeleteAdapter(w, r)` - DELETE /v1/adapters/{id}
      - `HandleActivateAdapter(w, r)` - POST /v1/adapters/{id}/activate
      - `HandleDeactivateAdapter(w, r)` - POST /v1/adapters/{id}/deactivate

66. **Add Training state to state machine**
    - File: `src/sentinel/internal/state/machine.go`
    - Add state: `StateTraining`
    - Transitions:
      - `Ready → Training`: on job start
      - `Training → Ready`: on job complete/cancel
    - Behavior in Training state:
      - Inference requests return 503 "Training in progress"
      - Health endpoint returns state info

67. **Integrate fine-tuning routes into proxy**
    - File: `src/sentinel/internal/proxy/proxy.go`
    - Route `/v1/finetune/*` to fine-tuning API handlers
    - Route `/v1/adapters/*` to adapter API handlers
    - Only route when `TB_FINETUNE_ENABLED=true`

68. **Create QLoRA training worker Dockerfile**
    - File: `src/runtime/finetune/Dockerfile.finetune`
    ```dockerfile
    FROM nvcr.io/nvidia/pytorch:24.01-py3
    RUN pip install peft transformers datasets accelerate bitsandbytes
    COPY train.py /app/train.py
    COPY requirements.txt /app/requirements.txt
    WORKDIR /app
    ENTRYPOINT ["python", "train.py"]
    ```

69. **Implement QLoRA training script**
    - File: `src/runtime/finetune/train.py`
    - Features:
      - Load base model from vLLM runtime API (not from disk)
      - Apply 4-bit quantization (BitsAndBytes NF4)
      - Configure LoRA: r, alpha, target_modules, dropout
      - Train on JSONL dataset (instruction/input/output format)
      - Save adapter to output directory
      - Report progress to sentinel via callback URL
    - QLoRA config (hardcoded):
      ```python
      bnb_config = BitsAndBytesConfig(
          load_in_4bit=True,
          bnb_4bit_quant_type="nf4",
          bnb_4bit_compute_dtype=torch.bfloat16,
          bnb_4bit_use_double_quant=True,
      )
      ```

70. **Update runtime entrypoint for LoRA support**
    - File: `src/runtime/entrypoint.sh`
    - Add vLLM LoRA flags:
      ```bash
      exec vllm serve \
        --model "$TB_PIPE_PATH" \
        --host 127.0.0.1 \
        --port 8081 \
        --enable-lora \
        --max-loras $TB_MAX_LORAS \
        --max-lora-rank $TB_MAX_LORA_RANK
      ```

71. **Implement adapter activation in runtime**
    - File: `src/sentinel/internal/finetune/runtime.go`
    - Functions:
      - `ActivateAdapter(adapterID string) error` - calls vLLM load_lora_adapter API
      - `DeactivateAdapter(adapterID string) error` - calls vLLM unload API
      - `GetActiveAdapters() ([]string, error)`
    - vLLM API: `POST http://127.0.0.1:8081/v1/load_lora_adapter`

72. **Add fine-tuning configuration to sentinel config**
    - File: `src/sentinel/internal/config/config.go`
    - Add fields:
      - `FineTuneEnabled bool`
      - `AdaptersDir string`
      - `TrainingDataDir string`
      - `MaxAdapters int`
      - `MaxTrainingDataGB int`
      - `MaxConcurrentJobs int`
      - `DefaultLoRAR int`
      - `DefaultLoRAAlpha int`
      - `DefaultEpochs int`
      - `DefaultBatchSize int`
      - `MaxLoRAs int`
      - `MaxLoRARank int`
    - Validation: directories must be writable

73. **Add fine-tuning metrics to billing**
    - File: `src/sentinel/internal/billing/counter.go`
    - Add fields:
      - `FineTuningJobs int64`
      - `FineTuningGPUMinutes int64`
      - `TrainingTokens int64`
    - Track job duration and report to metering API

74. **Implement fine-tuning E2E tests**
    - File: `e2e/tests/test_finetune.py`
    - Tests:
      - `test_finetune_creates_adapter`: Submit job, verify adapter created
      - `test_finetune_blocked_when_disabled`: Provider sets allow_finetune=false, verify 403
      - `test_inference_blocked_during_training`: Submit job, verify inference returns 503
      - `test_adapter_persists_across_restart`: Create adapter, restart, verify loadable
      - `test_adapter_activation`: Activate adapter, verify inference uses it
      - `test_invalid_training_data_rejected`: Upload malformed JSONL, verify error

75. **Update docker-compose for fine-tuning**
    - File: `e2e/docker-compose.yml`
    - Add volumes:
      - `./adapters:/mnt/adapters`
      - `./training-data:/mnt/training`
    - Add environment variables for fine-tuning
    - Add training worker service (on-demand)

76. **Security audit for fine-tuning**
    - Checklist:
      - [ ] Training worker cannot access `TB_PIPE_PATH` directly
      - [ ] Training worker accesses base model only through runtime API
      - [ ] Adapters cannot be downloaded/exported
      - [ ] Training data isolated from model artifacts
      - [ ] Job creation validates provider `allow_finetune` flag
      - [ ] No adapter data in audit logs

**Acceptance**:
- Consumer can upload training data and create fine-tuning job
- Training completes and adapter is saved
- Adapter can be activated and used for inference
- Provider can disable fine-tuning per-asset
- Inference blocked during training (503)
- All security invariants maintained

---

## Summary

This implementation plan covers **76 discrete, actionable tasks** organized into **15 phases** spanning approximately **8-12 weeks** of engineering effort (assuming 1-2 engineers).

### Current Progress

**Phases 1-11 are COMPLETE** ✅ - The core platform is fully functional:
- Provider can encrypt, upload, register, build, package, and publish model assets
- Consumer sentinel can authorize, hydrate, decrypt, and proxy inference requests
- E2E testing infrastructure validates the complete workflow
- Azure Managed App templates enable production deployment

**Remaining work** (Phases 12-15):
- Production hardening (SAS retry optimization, mTLS, security audit)
- Acceptance testing validation
- Documentation completion
- QLoRA fine-tuning support (optional feature)

**Key milestones**:
- **End of Phase 1**: Crypto interop proven ✅
- **End of Phase 2**: Provider can publish encrypted packages ✅
- **End of Phase 6**: Sentinel can hydrate and decrypt ✅
- **End of Phase 10**: E2E demo fully functional ✅
- **End of Phase 11**: Azure deployment working ✅
- **End of Phase 13**: Production-ready and validated
- **End of Phase 15**: QLoRA fine-tuning operational on consumer side

**Dependencies**:
- External Control Plane API must be available by Phase 11 (for production testing)
- Azure subscription with GPU quota for Phase 11+
- Azure Marketplace publisher account for Phase 14 (if publishing publicly)
- GPU with sufficient VRAM for QLoRA training (Phase 15) - minimum 24GB recommended

---

## Appendix A) Notes for coding agents

### General Principles
- Do not implement extra UX, dashboards, or additional services beyond what is specified here.
- Keep modules small and independently testable.
- Prefer deterministic, documented formats over "concept-only" crypto.
- Prefer the smallest deployable path: one VM running two containers.

### Implementation Guidance
- **Control Plane**: The production Control Plane is **external**. Only implement the mock version in `e2e/controlplane-mock/` for testing.
- **Follow Section 10**: The implementation order in Section 10 covers all 76 required tasks across 15 phases. Use it as a checklist.
- **Test as you go**: Each phase has an acceptance criterion. Do not proceed to the next phase until the current phase passes.
- **E2E first**: The E2E demo (Section 11, Phase 10) is the primary validation. Prioritize making it work before Azure deployment.
- **Fine-tuning (Phase 15)**: QLoRA fine-tuning is an optional feature that can be implemented after core functionality is stable. Ensure Phases 1-13 are complete before starting Phase 15.
- **Security invariants**: Never compromise on the core security requirements:
  - No plaintext weights on persistent disk
  - Runtime must never be externally accessible
  - Decryption keys must never be logged
  - SAS URLs must not be logged in production
  - Training worker must not have direct access to decrypted weights (Phase 15)

### File Organization
- Repository layout is defined in Section 2. Create directories as needed following this structure.
- Use the specified file paths in Section 10 (e.g., `src/sentinel/internal/crypto/decrypt.go`).
- Add unit tests alongside implementation files (e.g., `decrypt_test.go`).

### Dependencies
- External Control Plane API must be available by Phase 11 for production integration testing
- Azure subscription with GPU quota required for Phase 11+ (infrastructure deployment)
- Azure Marketplace publisher account required for Phase 14 if publishing publicly

---

## 11) E2E Demo (Provider → Consumer) – Full Workflow Test

This section defines a runnable end-to-end scenario that exercises the entire workflow:

1) Provider encrypts weights to `tbenc/v1`
2) Provider uploads encrypted artifacts (ciphertext + manifest)
3) Control Plane authorizes a consumer contract and returns download URLs + key
4) Consumer sentinel hydrates from URL, verifies hash, decrypts to FIFO/tmpfs
5) Runtime reads from FIFO (or RAM file fallback)
6) Client sends an inference-like request through sentinel; request is proxied and audited

The E2E demo is designed to be implementable on a developer machine using Docker Compose.

### 11.1 Why an E2E demo is required

Most defects in this project are *integration defects*:

- encryption format mismatch (provider vs consumer)
- downloader/range bugs
- FIFO blocking behavior
- readiness signaling
- control plane contract gating
- runtime isolation (localhost only)

This demo must be the default way to prove “the platform works” before deploying to Azure.

### 11.2 E2E components (local)

The demo uses 4 local services:

1) **Provider CLI (local process)**
  - Produces `model.tbenc` + `model.manifest.json`
2) **Blob server (local HTTP)**
  - Serves the encrypted artifacts over HTTP with Range support
  - In production this is Azure Blob + SAS; for E2E, HTTP is sufficient to validate download, integrity, and decryption
3) **Control Plane mock (local HTTP API)**
  - Implements `POST /api/v1/license/authorize`
  - Returns:
    - `manifest_url`
    - `sas_url` (for E2E it can be a plain URL)
    - `decryption_key_hex`
4) **Consumer stack (Docker Compose)**
  - `sentinel` container (port 8000 exposed)
  - `runtime-mock` container (localhost port 8081 within compose network)

Why `runtime-mock`?

- Real vLLM/Triton startup is heavy and requires GPUs.
- The E2E goal is proving the sentinel pipeline + proxying + “weights are readable from FIFO/tmpfs”.

Once this demo works, swap `runtime-mock` with real vLLM in an Azure environment.

### 11.3 Demo data

Use a small deterministic "weights" file so the demo is fast and produces stable hashes.

- File: `e2e/data/plain.weights`
- Size: 8–64 MiB
- Generation command example:

```bash
mkdir -p e2e/data
python - <<'PY'
import os, hashlib
path = 'e2e/data/plain.weights'
size = 16 * 1024 * 1024
data = b'TRUSTBRIDGE_E2E_' * (size // len(b'TRUSTBRIDGE_E2E_'))
data = data[:size]
open(path,'wb').write(data)
print('sha256=', hashlib.sha256(data).hexdigest())
PY
```

### 11.4 E2E wiring (URLs, keys, identities)

Canonical E2E parameters:

- `asset_id`: `tb-asset-e2e-001`
- `contract_id` (allowed): `contract-allow`
- `contract_id` (denied): `contract-deny`
- `decryption_key_hex`: fixed for the demo (32 bytes hex) so the control plane mock can be stateless

The control plane mock should:

- return `status=authorized` only for `contract-allow`
- return `status=denied` for anything else

### 11.5 E2E run steps (happy path)

These steps assume that `trustbridge` CLI and `sentinel` binaries exist.

1) Generate demo plaintext

```bash
make e2e-generate-plain
```

2) Provider encrypts to `tbenc/v1`

```bash
trustbridge encrypt \
  --asset-id tb-asset-e2e-001 \
  --in e2e/data/plain.weights \
  --out e2e/artifacts/model.tbenc \
  --manifest e2e/artifacts/model.manifest.json \
  --chunk-bytes 4194304
```

3) Start local services (blob + controlplane + consumer stack)

```bash
docker compose -f e2e/docker-compose.yml up --build
```

4) Call sentinel through its public interface

```bash
curl -sS http://localhost:8000/health
curl -sS http://localhost:8000/v1/demo
```

Expected:

- `/health` returns 200 only after sentinel is in `Ready`
- `/v1/demo` is proxied to runtime-mock

### 11.6 E2E assertions (what must be tested)

Implement an automated test runner (recommended: Python `pytest` or Go test) that asserts:

**A) Authorization gating**

- With `TB_CONTRACT_ID=contract-deny`, sentinel must not open port 8000 (or must return 403 for all routes).

**B) Download + integrity**

- Sentinel downloads `model.tbenc` and verifies `sha256_ciphertext` matches manifest.
- If a single byte of ciphertext is modified, sentinel must fail before reaching `Ready`.

**C) Decrypt interop**

- Runtime-mock reads the decrypted stream (FIFO or RAM file) and computes plaintext sha256.
- The plaintext sha256 must equal the sha256 printed when generating `plain.weights`.

**D) No plaintext on disk**

- Ensure `${TB_TARGET_DIR}` contains only encrypted artifacts (`model.tbenc`, optional manifest).
- Ensure no file with plaintext content is created under `${TB_TARGET_DIR}`.

**E) Proxy path correctness**

- A request to sentinel is forwarded to runtime URL `127.0.0.1:8081` (in-compose network).
- Runtime must not be reachable directly from outside the compose network.

**F) Audit log produced**

- Sentinel emits at least one audit record for one proxied request.

### 11.7 SAS expiry simulation (optional but recommended)

In production, SAS can expire mid-download; the sentinel must re-authorize and resume.

For the E2E demo, simulate this by:

- Control plane mock returns `expires_at` very soon (e.g., 5 seconds)
- Blob server enforces a token/expiry query parameter and starts returning 403 after expiry
- Sentinel must detect failures, re-call authorize, and retry/resume until complete

This is optional for the first E2E milestone, but required before Azure deployment.

### 11.8 Required E2E files (to implement)

Create these files to make the demo runnable:

- `e2e/docker-compose.yml`
- `e2e/controlplane-mock/` (implements `POST /api/v1/license/authorize`)
- `e2e/blob/` (serves `e2e/artifacts/` with Range support)
- `e2e/runtime-mock/` (reads FIFO/RAM plaintext and serves a tiny HTTP API on 8081)
- `e2e/tests/` (automated tests driving the workflow)

The E2E demo is considered complete when `docker compose up` + `e2e/tests` can run on a developer machine and validate all assertions above.
