# KServe and KV Cache Integration Analysis for TrustBridge

This document analyzes how **KServe** (Kubernetes model serving) and **KV Cache serving technologies** can be integrated with TrustBridge to enhance scalability, performance, and enterprise deployment capabilities.

---

## Table of Contents

1. [Executive Summary](#executive-summary)
2. [Technology Overview](#technology-overview)
3. [KServe Integration](#kserve-integration)
4. [KV Cache Serving Integration](#kv-cache-serving-integration)
5. [Architecture Recommendations](#architecture-recommendations)
6. [Implementation Roadmap](#implementation-roadmap)
7. [Security Considerations](#security-considerations)
8. [Conclusion](#conclusion)

---

## Executive Summary

### What This Analysis Covers

| Technology | Description | Relevance to TrustBridge |
|------------|-------------|--------------------------|
| **KServe** | Kubernetes-native AI inference platform | Enterprise deployment, autoscaling, multi-model serving |
| **LMCache** | Distributed KV cache layer for LLM inference | 3-15x throughput improvement, reduced GPU costs |
| **llm-d** | KV cache-aware routing and scheduling | Intelligent request routing, prefix cache optimization |

### Key Findings

1. **KServe** can replace/complement TrustBridge's current VM-based deployment with Kubernetes-native orchestration
2. **KV Cache technologies** (LMCache, llm-d) can significantly improve inference performance for multi-turn conversations and RAG workloads
3. Integration requires careful security design to maintain TrustBridge's "no plaintext on disk" guarantee
4. Estimated **3-15x throughput improvement** with KV cache optimization for suitable workloads

---

## Technology Overview

### KServe

[KServe](https://kserve.github.io/website/) is a CNCF incubating project that provides a Kubernetes-native platform for AI model serving. As of v0.15 (June 2025), it offers:

- **InferenceService CRD**: Standardized Kubernetes resource for model deployment
- **vLLM Backend**: Native support for vLLM with OpenAI-compatible API
- **Autoscaling**: KEDA integration for LLM-specific metrics (queue depth, KV cache utilization)
- **KV Cache Offloading**: Integration with LMCache for distributed caching
- **Canary Deployments**: Gradual rollout and A/B testing
- **Multi-Model Serving**: Efficient resource sharing across models

**Reference**: [KServe v0.15 Release](https://www.cncf.io/blog/2025/06/18/announcing-kserve-v0-15-advancing-generative-ai-model-serving/)

### LMCache

[LMCache](https://github.com/LMCache/LMCache) is the de facto standard for KV cache management in enterprise LLM inference:

- **Multi-tier Storage**: GPU → CPU → Disk → Remote (Redis, S3, etc.)
- **Cross-engine Sharing**: Share KV caches between vLLM instances
- **Prefix Caching**: Automatic reuse of common prompt prefixes
- **PD Disaggregation**: Separate prefill and decode phases across GPUs
- **Performance**: Up to 15x throughput improvement for suitable workloads

**Reference**: [LMCache Technical Report](https://arxiv.org/abs/2510.09665)

### llm-d

[llm-d](https://llm-d.ai/) provides distributed KV cache-aware routing:

- **Global KV Index**: Real-time view of cache block locality across pods
- **Intelligent Routing**: Route requests to pods with relevant cached prefixes
- **Load Balancing**: Cache-aware load distribution
- **Kubernetes Native**: Designed for KServe/vLLM deployments

**Reference**: [KV-Cache Wins with llm-d](https://llm-d.ai/blog/kvcache-wins-you-can-see)

---

## KServe Integration

### Current TrustBridge Architecture

```
┌─────────────────────────────────────────────────────────────┐
│                    Current: VM-Based                        │
│                                                             │
│   ┌─────────────────┐      ┌─────────────────────────┐     │
│   │    Sentinel     │      │       vLLM Runtime      │     │
│   │   (Go binary)   │─────►│    (localhost:8081)     │     │
│   │   Port 8000     │ FIFO │                         │     │
│   └─────────────────┘      └─────────────────────────┘     │
│                                                             │
│   Single GPU VM with Docker Compose                         │
└─────────────────────────────────────────────────────────────┘
```

### Proposed KServe Architecture

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                        KServe on Kubernetes                                  │
│                                                                             │
│   ┌─────────────────────────────────────────────────────────────────────┐   │
│   │                    InferenceService CRD                              │   │
│   │   metadata:                                                          │   │
│   │     name: trustbridge-model                                          │   │
│   │   spec:                                                              │   │
│   │     predictor:                                                       │   │
│   │       containers:                                                    │   │
│   │         - name: sentinel (sidecar)                                   │   │
│   │         - name: vllm (main container)                                │   │
│   └─────────────────────────────────────────────────────────────────────┘   │
│                                                                             │
│   ┌───────────────┐     ┌───────────────┐     ┌───────────────┐            │
│   │   Pod 1       │     │   Pod 2       │     │   Pod N       │            │
│   │ ┌───────────┐ │     │ ┌───────────┐ │     │ ┌───────────┐ │            │
│   │ │ Sentinel  │ │     │ │ Sentinel  │ │     │ │ Sentinel  │ │            │
│   │ └─────┬─────┘ │     │ └─────┬─────┘ │     │ └─────┬─────┘ │            │
│   │       │       │     │       │       │     │       │       │            │
│   │ ┌─────▼─────┐ │     │ ┌─────▼─────┐ │     │ ┌─────▼─────┐ │            │
│   │ │   vLLM    │ │     │ │   vLLM    │ │     │ │   vLLM    │ │            │
│   │ │  + GPU    │ │     │ │  + GPU    │ │     │ │  + GPU    │ │            │
│   │ └───────────┘ │     │ └───────────┘ │     │ └───────────┘ │            │
│   └───────────────┘     └───────────────┘     └───────────────┘            │
│           │                     │                     │                     │
│           └─────────────────────┼─────────────────────┘                     │
│                                 │                                           │
│                    ┌────────────▼────────────┐                              │
│                    │    Istio / Envoy        │                              │
│                    │  (Ingress + Routing)    │                              │
│                    └─────────────────────────┘                              │
│                                                                             │
│   KEDA Autoscaler monitors: queue_depth, kv_cache_util, gpu_memory          │
└─────────────────────────────────────────────────────────────────────────────┘
```

### Integration Points

#### 1. Sentinel as Sidecar Container

Modify Sentinel to run as a Kubernetes sidecar:

```yaml
# kserve-inferenceservice.yaml
apiVersion: serving.kserve.io/v1beta1
kind: InferenceService
metadata:
  name: trustbridge-secure-model
spec:
  predictor:
    timeout: 600
    containers:
      # Sentinel sidecar - handles auth, decrypt, audit
      - name: sentinel
        image: trustbridge/sentinel:v1.0.0
        ports:
          - containerPort: 8000
            protocol: TCP
        env:
          - name: TB_CONTRACT_ID
            valueFrom:
              secretKeyRef:
                name: trustbridge-secrets
                key: contract-id
          - name: TB_ASSET_ID
            value: "model-asset-001"
          - name: TB_EDC_ENDPOINT
            value: "https://controlplane.example.com"
          - name: TB_RUNTIME_URL
            value: "http://localhost:8081"
        volumeMounts:
          - name: model-pipe
            mountPath: /dev/shm
          - name: encrypted-cache
            mountPath: /mnt/cache
        resources:
          limits:
            memory: "2Gi"
            cpu: "2"

      # vLLM inference container
      - name: vllm
        image: vllm/vllm-openai:v0.8.5
        args:
          - "--model=/dev/shm/model-pipe"
          - "--host=127.0.0.1"
          - "--port=8081"
          - "--enable-lora"
        ports:
          - containerPort: 8081
            protocol: TCP
        resources:
          limits:
            nvidia.com/gpu: "1"
            memory: "80Gi"
        volumeMounts:
          - name: model-pipe
            mountPath: /dev/shm

    volumes:
      - name: model-pipe
        emptyDir:
          medium: Memory
          sizeLimit: 512Mi
      - name: encrypted-cache
        emptyDir:
          sizeLimit: 100Gi
```

#### 2. KServe Service Routing

Configure KServe to route through Sentinel:

```yaml
# Route all traffic through sentinel sidecar
spec:
  predictor:
    serviceAccountName: trustbridge-sa
    # Sentinel handles external traffic on port 8000
    # vLLM only accessible via localhost:8081
    containerConcurrency: 1  # Per-pod request limit
```

#### 3. KEDA Autoscaling

```yaml
# keda-scaledobject.yaml
apiVersion: keda.sh/v1alpha1
kind: ScaledObject
metadata:
  name: trustbridge-autoscaler
spec:
  scaleTargetRef:
    apiVersion: serving.kserve.io/v1beta1
    kind: InferenceService
    name: trustbridge-secure-model
  minReplicaCount: 1
  maxReplicaCount: 10
  triggers:
    - type: prometheus
      metadata:
        serverAddress: http://prometheus:9090
        metricName: sentinel_request_queue_depth
        threshold: "10"
        query: |
          sum(sentinel_pending_requests{service="trustbridge"})
    - type: prometheus
      metadata:
        metricName: vllm_gpu_cache_usage
        threshold: "0.8"
        query: |
          vllm_gpu_cache_usage_perc{service="trustbridge"}
```

### Benefits of KServe Integration

| Benefit | Description | Impact |
|---------|-------------|--------|
| **Horizontal Scaling** | Auto-scale pods based on demand | Handle traffic spikes |
| **Rolling Updates** | Zero-downtime model updates | Continuous deployment |
| **Canary Deployments** | Gradual rollout of new models | Risk mitigation |
| **Multi-Model Serving** | Share GPU resources across models | Cost optimization |
| **Standardized API** | OpenAI-compatible endpoints | Easy integration |
| **Observability** | Prometheus metrics, tracing | Better monitoring |

---

## KV Cache Serving Integration

### Why KV Cache Matters for TrustBridge

KV (Key-Value) cache stores the intermediate computations during LLM inference. For TrustBridge use cases:

| Workload | KV Cache Benefit | Expected Improvement |
|----------|------------------|---------------------|
| **Multi-turn Chat** | Reuse conversation context | 3-5x latency reduction |
| **RAG Applications** | Cache document embeddings | 5-10x throughput |
| **Batch Processing** | Share common prompts | 10-15x efficiency |
| **System Prompts** | Cache instruction prefixes | 2-3x for all requests |

### LMCache Integration Architecture

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                    TrustBridge + LMCache Architecture                        │
│                                                                             │
│   ┌─────────────────────────────────────────────────────────────────────┐   │
│   │                         Request Flow                                 │   │
│   │                                                                      │   │
│   │   Client ──► Sentinel ──► vLLM ──► LMCache ──► Storage Tiers        │   │
│   │                │                       │                             │   │
│   │                │ Auth/Audit            │ KV Cache                    │   │
│   │                ▼                       ▼                             │   │
│   │           Control Plane         ┌─────────────────┐                  │   │
│   │                                 │   GPU Memory    │ ◄─ Fastest       │   │
│   │                                 │   (Hot Cache)   │                  │   │
│   │                                 ├─────────────────┤                  │   │
│   │                                 │   CPU Memory    │ ◄─ Fast          │   │
│   │                                 │  (Warm Cache)   │                  │   │
│   │                                 ├─────────────────┤                  │   │
│   │                                 │   Local NVMe    │ ◄─ Medium        │   │
│   │                                 │  (Cold Cache)   │                  │   │
│   │                                 ├─────────────────┤                  │   │
│   │                                 │  Redis/Remote   │ ◄─ Shared        │   │
│   │                                 │ (Cross-engine)  │                  │   │
│   │                                 └─────────────────┘                  │   │
│   └─────────────────────────────────────────────────────────────────────┘   │
└─────────────────────────────────────────────────────────────────────────────┘
```

### Security Considerations for KV Cache

**Critical**: KV cache contains intermediate model computations that could leak information about inputs. TrustBridge must ensure:

1. **Encryption at Rest**: KV cache data must be encrypted when persisted
2. **Memory Isolation**: GPU/CPU cache isolated per tenant
3. **No Cross-Tenant Sharing**: Cache must be contract-scoped
4. **Secure Eviction**: Overwrite cache on eviction

```python
# Proposed LMCache configuration for TrustBridge
lmcache_config = {
    "chunk_size": 256,
    "local_device": "cuda",

    # Security settings
    "encryption_enabled": True,
    "encryption_key_from": "sentinel",  # Derive from contract key

    # Storage tiers (all encrypted)
    "storage_tiers": [
        {"type": "gpu", "capacity_gb": 10},
        {"type": "cpu", "capacity_gb": 50},
        {"type": "local_disk", "path": "/mnt/kvcache", "capacity_gb": 200},
    ],

    # Isolation
    "tenant_isolation": True,
    "cache_scope": "contract_id",  # Scope cache to contract

    # Security
    "secure_eviction": True,  # Overwrite on eviction
    "audit_cache_access": True,  # Log cache hits/misses
}
```

### llm-d Integration for Multi-Pod Routing

When running multiple TrustBridge pods, llm-d provides intelligent routing:

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                      llm-d KV-Aware Routing                                  │
│                                                                             │
│   ┌─────────────────────────────────────────────────────────────────────┐   │
│   │                                                                      │   │
│   │   Request: "Continue our conversation about TrustBridge..."         │   │
│   │                           │                                          │   │
│   │                           ▼                                          │   │
│   │                  ┌─────────────────┐                                 │   │
│   │                  │  llm-d Router   │                                 │   │
│   │                  │                 │                                 │   │
│   │                  │  1. Hash prefix │                                 │   │
│   │                  │  2. Check index │                                 │   │
│   │                  │  3. Route to    │                                 │   │
│   │                  │     cached pod  │                                 │   │
│   │                  └────────┬────────┘                                 │   │
│   │                           │                                          │   │
│   │         ┌─────────────────┼─────────────────┐                        │   │
│   │         │                 │                 │                        │   │
│   │         ▼                 ▼                 ▼                        │   │
│   │   ┌───────────┐    ┌───────────┐    ┌───────────┐                   │   │
│   │   │   Pod 1   │    │   Pod 2   │    │   Pod 3   │                   │   │
│   │   │           │    │           │    │           │                   │   │
│   │   │ KV Cache: │    │ KV Cache: │    │ KV Cache: │                   │   │
│   │   │ - User A  │    │ - User B  │    │ - User C  │                   │   │
│   │   │ - Doc 1   │    │ - Doc 2   │    │ - Doc 1   │ ◄── Cache hit!   │   │
│   │   │           │    │           │    │           │                   │   │
│   │   └───────────┘    └───────────┘    └───────────┘                   │   │
│   │                                                                      │   │
│   └─────────────────────────────────────────────────────────────────────┘   │
│                                                                             │
│   Result: Request routed to Pod 3 which has relevant conversation cached    │
└─────────────────────────────────────────────────────────────────────────────┘
```

### Sentinel Modifications for KV Cache

Add KV cache awareness to Sentinel:

```go
// src/sentinel/internal/kvcache/manager.go

package kvcache

import (
    "crypto/aes"
    "crypto/cipher"
)

// CacheManager handles encrypted KV cache operations
type CacheManager struct {
    contractID    string
    encryptionKey []byte  // Derived from contract decryption key
    lmcacheClient *LMCacheClient
}

// NewCacheManager creates a contract-scoped cache manager
func NewCacheManager(contractID string, baseKey []byte) (*CacheManager, error) {
    // Derive cache encryption key from base key
    cacheKey := deriveKey(baseKey, "kvcache")

    return &CacheManager{
        contractID:    contractID,
        encryptionKey: cacheKey,
        lmcacheClient: NewLMCacheClient(getLMCacheEndpoint()),
    }, nil
}

// GetCachedKV retrieves and decrypts cached KV data
func (m *CacheManager) GetCachedKV(prefixHash string) ([]byte, bool) {
    scopedKey := m.contractID + ":" + prefixHash

    encrypted, found := m.lmcacheClient.Get(scopedKey)
    if !found {
        return nil, false
    }

    // Decrypt cache data
    decrypted, err := m.decrypt(encrypted)
    if err != nil {
        return nil, false
    }

    return decrypted, true
}

// StoreCachedKV encrypts and stores KV data
func (m *CacheManager) StoreCachedKV(prefixHash string, kvData []byte) error {
    scopedKey := m.contractID + ":" + prefixHash

    // Encrypt before storage
    encrypted, err := m.encrypt(kvData)
    if err != nil {
        return err
    }

    return m.lmcacheClient.Put(scopedKey, encrypted)
}

// EvictContractCache removes all cache for a contract (on suspension)
func (m *CacheManager) EvictContractCache() error {
    pattern := m.contractID + ":*"
    return m.lmcacheClient.DeletePattern(pattern)
}
```

---

## Architecture Recommendations

### Option 1: VM-Based with LMCache (Incremental)

**Best for**: Existing deployments, simple scaling needs

```
┌─────────────────────────────────────────────────────────────┐
│   Existing VM-Based + LMCache                               │
│                                                             │
│   ┌─────────────┐     ┌─────────────┐     ┌─────────────┐  │
│   │  Sentinel   │────►│    vLLM     │────►│  LMCache    │  │
│   │             │     │  + LMCache  │     │   (Local)   │  │
│   └─────────────┘     │   Plugin    │     └─────────────┘  │
│                       └─────────────┘                       │
│                                                             │
│   Changes Required:                                         │
│   - Add LMCache to vLLM startup                            │
│   - Configure local CPU/disk cache tiers                   │
│   - Add cache metrics to Sentinel                          │
└─────────────────────────────────────────────────────────────┘
```

**Effort**: Low (2-3 weeks)
**Benefit**: 3-5x improvement for repeat queries

### Option 2: KServe Deployment (Kubernetes Native)

**Best for**: Enterprise deployments, multi-model, autoscaling

```
┌─────────────────────────────────────────────────────────────┐
│   KServe + Sentinel Sidecar                                 │
│                                                             │
│   ┌─────────────────────────────────────────────────────┐  │
│   │              Kubernetes Cluster                      │  │
│   │                                                      │  │
│   │   ┌───────────────────────────────────────────────┐ │  │
│   │   │            KServe InferenceService            │ │  │
│   │   │                                               │ │  │
│   │   │   Pod: [Sentinel] ──► [vLLM + LMCache]       │ │  │
│   │   │   Pod: [Sentinel] ──► [vLLM + LMCache]       │ │  │
│   │   │   Pod: [Sentinel] ──► [vLLM + LMCache]       │ │  │
│   │   │                                               │ │  │
│   │   └───────────────────────────────────────────────┘ │  │
│   │                         │                            │  │
│   │                    KEDA Autoscaler                   │  │
│   │                                                      │  │
│   └─────────────────────────────────────────────────────┘  │
│                                                             │
│   Changes Required:                                         │
│   - Containerize Sentinel for K8s                          │
│   - Create InferenceService CRD                            │
│   - Configure KEDA triggers                                │
│   - Set up Istio/Envoy routing                            │
└─────────────────────────────────────────────────────────────┘
```

**Effort**: Medium (4-6 weeks)
**Benefit**: Enterprise-grade scaling, 5-10x improvement

### Option 3: Full Stack with llm-d (Maximum Performance)

**Best for**: High-traffic production, multi-region

```
┌─────────────────────────────────────────────────────────────┐
│   KServe + llm-d + Distributed LMCache                      │
│                                                             │
│   ┌─────────────────────────────────────────────────────┐  │
│   │                    llm-d Router                      │  │
│   │          (KV-cache aware request routing)            │  │
│   └───────────────────────┬─────────────────────────────┘  │
│                           │                                 │
│   ┌───────────────────────┼─────────────────────────────┐  │
│   │                       ▼                              │  │
│   │   ┌─────────┐   ┌─────────┐   ┌─────────┐          │  │
│   │   │  Pod 1  │   │  Pod 2  │   │  Pod 3  │          │  │
│   │   │ A100    │   │ A100    │   │ A100    │          │  │
│   │   └────┬────┘   └────┬────┘   └────┬────┘          │  │
│   │        │             │             │                │  │
│   │        └─────────────┼─────────────┘                │  │
│   │                      │                              │  │
│   │            ┌─────────▼─────────┐                    │  │
│   │            │   Redis Cluster   │                    │  │
│   │            │ (Shared KV Cache) │                    │  │
│   │            └───────────────────┘                    │  │
│   │                                                      │  │
│   └─────────────────────────────────────────────────────┘  │
│                                                             │
│   Changes Required:                                         │
│   - All Option 2 changes                                   │
│   - Deploy llm-d router                                    │
│   - Set up Redis cluster for shared cache                  │
│   - Configure cross-pod cache sharing                      │
└─────────────────────────────────────────────────────────────┘
```

**Effort**: High (8-12 weeks)
**Benefit**: 10-15x improvement, optimal resource utilization

### Recommendation Matrix

| Criteria | Option 1 (VM+LMCache) | Option 2 (KServe) | Option 3 (Full Stack) |
|----------|----------------------|-------------------|----------------------|
| **Implementation Effort** | Low | Medium | High |
| **Performance Gain** | 3-5x | 5-10x | 10-15x |
| **Scaling Capability** | Manual | Auto (KEDA) | Auto + Cache-aware |
| **Operational Complexity** | Low | Medium | High |
| **Cost Efficiency** | Good | Better | Best |
| **Multi-tenant Support** | Limited | Good | Excellent |
| **Recommended For** | POC, Small deployments | Production | Enterprise |

---

## Implementation Roadmap

### Phase A: LMCache Integration (Weeks 1-3)

**Goal**: Add local KV caching to existing VM deployment

#### A.1 vLLM Configuration Update

```bash
# Update runtime entrypoint.sh
vllm serve \
  --model "$TB_PIPE_PATH" \
  --host 127.0.0.1 \
  --port 8081 \
  --enable-lora \
  --enable-prefix-caching \
  --kv-cache-dtype fp8 \
  --max-num-seqs 256
```

#### A.2 LMCache Backend Configuration

```python
# lmcache_config.yaml
chunk_size: 256
local_device: "cuda"

storage:
  - type: "local_cpu"
    capacity_gb: 50

  - type: "local_disk"
    path: "/mnt/kvcache"
    capacity_gb: 200
    encryption: true

pipelining:
  enabled: true
  batch_size: 32
```

#### A.3 Sentinel Metrics Addition

```go
// Add to internal/health/metrics.go
var (
    kvCacheHitRate = prometheus.NewGauge(prometheus.GaugeOpts{
        Name: "sentinel_kv_cache_hit_rate",
        Help: "KV cache hit rate",
    })

    kvCacheSizeGB = prometheus.NewGauge(prometheus.GaugeOpts{
        Name: "sentinel_kv_cache_size_gb",
        Help: "Current KV cache size in GB",
    })
)
```

### Phase B: KServe Migration (Weeks 4-7)

**Goal**: Deploy TrustBridge on Kubernetes with KServe

#### B.1 Sentinel Containerization

```dockerfile
# Dockerfile.sentinel-k8s
FROM golang:1.21-alpine AS builder
WORKDIR /app
COPY src/sentinel/ .
RUN go build -o sentinel ./cmd/sentinel

FROM alpine:3.19
RUN apk add --no-cache ca-certificates
COPY --from=builder /app/sentinel /usr/local/bin/
EXPOSE 8000 8001
ENTRYPOINT ["sentinel"]
```

#### B.2 KServe CRD Deployment

```yaml
# Deploy InferenceService
kubectl apply -f trustbridge-inferenceservice.yaml

# Verify deployment
kubectl get inferenceservices trustbridge-secure-model
kubectl get pods -l serving.kserve.io/inferenceservice=trustbridge-secure-model
```

#### B.3 KEDA Autoscaler Setup

```bash
# Install KEDA
helm install keda kedacore/keda -n keda --create-namespace

# Deploy ScaledObject
kubectl apply -f trustbridge-scaledobject.yaml
```

### Phase C: llm-d Integration (Weeks 8-12)

**Goal**: Enable KV cache-aware routing across pods

#### C.1 llm-d Deployment

```yaml
# llm-d-deployment.yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: llm-d-router
spec:
  replicas: 2
  template:
    spec:
      containers:
        - name: llm-d
          image: llm-d/router:latest
          env:
            - name: KSERVE_SERVICE
              value: "trustbridge-secure-model"
            - name: KV_INDEX_BACKEND
              value: "redis://redis-cluster:6379"
```

#### C.2 Redis Cluster for Shared Cache

```yaml
# redis-cluster.yaml
apiVersion: redis.redis.opstreelabs.in/v1beta2
kind: RedisCluster
metadata:
  name: trustbridge-kvcache
spec:
  clusterSize: 6
  persistence:
    enabled: true
    storageClassName: premium-ssd
  resources:
    limits:
      memory: 64Gi
```

#### C.3 Cross-Pod Cache Configuration

```python
# lmcache distributed config
storage:
  - type: "local_gpu"
    capacity_gb: 10

  - type: "local_cpu"
    capacity_gb: 50

  - type: "redis_cluster"
    endpoint: "redis://trustbridge-kvcache:6379"
    capacity_gb: 500
    encryption: true
    key_prefix: "tb:kv:"
```

---

## Security Considerations

### KV Cache Security Requirements

| Requirement | Implementation | Status |
|-------------|----------------|--------|
| **Encryption at Rest** | AES-256-GCM for persisted cache | Required |
| **Tenant Isolation** | Contract-scoped cache keys | Required |
| **Secure Eviction** | Overwrite before deallocation | Required |
| **Audit Logging** | Log cache access patterns | Recommended |
| **Memory Protection** | Encrypted GPU memory (if available) | Optional |

### Security Architecture

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                    Secure KV Cache Architecture                              │
│                                                                             │
│   ┌─────────────────────────────────────────────────────────────────────┐   │
│   │                          Sentinel                                    │   │
│   │                                                                      │   │
│   │   ┌─────────────────┐    ┌─────────────────┐    ┌────────────────┐  │   │
│   │   │ Contract Auth   │───►│  Key Derivation │───►│ Cache Manager  │  │   │
│   │   │                 │    │                 │    │                │  │   │
│   │   │ contract_id     │    │ base_key        │    │ encrypt/decrypt│  │   │
│   │   │ decryption_key  │    │ cache_key =     │    │ scope = contract│  │   │
│   │   │                 │    │ KDF(base, "kv") │    │                │  │   │
│   │   └─────────────────┘    └─────────────────┘    └───────┬────────┘  │   │
│   │                                                          │           │   │
│   └──────────────────────────────────────────────────────────┼───────────┘   │
│                                                              │               │
│                                                              ▼               │
│   ┌─────────────────────────────────────────────────────────────────────┐   │
│   │                       LMCache Storage                                │   │
│   │                                                                      │   │
│   │   Key Format: {contract_id}:{prefix_hash}:{chunk_id}                │   │
│   │   Value: AES-256-GCM(kv_data, cache_key)                            │   │
│   │                                                                      │   │
│   │   Example:                                                           │   │
│   │   contract-123:abc123:0 → [encrypted KV tensor data]                │   │
│   │   contract-123:abc123:1 → [encrypted KV tensor data]                │   │
│   │                                                                      │   │
│   └─────────────────────────────────────────────────────────────────────┘   │
│                                                                             │
│   Security Invariants:                                                      │
│   - Cache key derived from contract decryption key (not stored)            │
│   - Contract isolation enforced at key level                               │
│   - Eviction triggers secure overwrite                                     │
│   - No cross-contract cache access possible                                │
└─────────────────────────────────────────────────────────────────────────────┘
```

### Threat Mitigations

| Threat | Mitigation |
|--------|------------|
| Cache poisoning | Signed cache entries + integrity checks |
| Cross-tenant leakage | Contract-scoped keys, tenant isolation |
| Side-channel attacks | Constant-time operations, noise injection |
| Cache timing attacks | Fixed cache lookup latency |
| Persistence exposure | Encryption + secure deletion |

---

## Conclusion

### Summary

Integrating **KServe** and **KV Cache technologies** into TrustBridge offers significant benefits:

1. **KServe** enables enterprise-grade Kubernetes deployment with autoscaling
2. **LMCache** provides 3-15x throughput improvement through intelligent caching
3. **llm-d** optimizes multi-pod deployments with cache-aware routing
4. Security can be maintained through encrypted, contract-scoped caching

### Recommended Approach

For TrustBridge, we recommend a **phased approach**:

1. **Short-term (Phase A)**: Add LMCache to existing VM deployment for immediate performance gains
2. **Medium-term (Phase B)**: Migrate to KServe for Kubernetes-native scaling
3. **Long-term (Phase C)**: Implement llm-d for maximum performance in high-traffic scenarios

### Next Steps

1. **Evaluate** LMCache performance with test workloads
2. **Prototype** Sentinel as KServe sidecar
3. **Design** encrypted KV cache schema
4. **Test** security invariants with cache enabled
5. **Benchmark** performance improvements

---

## References

- [KServe Documentation](https://kserve.github.io/website/)
- [KServe v0.15 Release Notes](https://www.cncf.io/blog/2025/06/18/announcing-kserve-v0-15-advancing-generative-ai-model-serving/)
- [LMCache GitHub](https://github.com/LMCache/LMCache)
- [LMCache Technical Report](https://arxiv.org/abs/2510.09665)
- [LMCache Architecture](https://docs.lmcache.ai/developer_guide/architecture.html)
- [llm-d KV Cache Routing](https://llm-d.ai/blog/kvcache-wins-you-can-see)
- [vLLM KServe Integration](https://docs.vllm.ai/en/latest/deployment/integrations/kserve/)
- [Ceph KV Caching with vLLM](https://ceph.io/en/news/blog/2025/vllm-kv-caching/)

---

*Last updated: January 2026*
