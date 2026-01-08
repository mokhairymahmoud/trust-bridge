#!/bin/bash
# TrustBridge Runtime Entrypoint
# Waits for sentinel ready signal, then starts vLLM inference server

set -e

# Configuration with defaults
TB_READY_SIGNAL="${TB_READY_SIGNAL:-/dev/shm/weights/ready.signal}"
TB_PIPE_PATH="${TB_PIPE_PATH:-/dev/shm/model-pipe}"
TB_RUNTIME_HOST="${TB_RUNTIME_HOST:-127.0.0.1}"
TB_RUNTIME_PORT="${TB_RUNTIME_PORT:-8081}"
TB_STARTUP_TIMEOUT="${TB_STARTUP_TIMEOUT:-300}"
TB_MAX_LORAS="${TB_MAX_LORAS:-4}"
TB_MAX_LORA_RANK="${TB_MAX_LORA_RANK:-64}"

echo "[entrypoint] TrustBridge Runtime starting..."
echo "[entrypoint] Waiting for ready signal at: $TB_READY_SIGNAL"
echo "[entrypoint] Timeout: ${TB_STARTUP_TIMEOUT}s"

# Wait for ready signal with timeout
ELAPSED=0
while [ ! -f "$TB_READY_SIGNAL" ]; do
    if [ "$ELAPSED" -ge "$TB_STARTUP_TIMEOUT" ]; then
        echo "[entrypoint] ERROR: Timeout waiting for ready signal after ${TB_STARTUP_TIMEOUT}s"
        exit 1
    fi
    sleep 1
    ELAPSED=$((ELAPSED + 1))
    if [ $((ELAPSED % 10)) -eq 0 ]; then
        echo "[entrypoint] Still waiting... ${ELAPSED}s elapsed"
    fi
done

echo "[entrypoint] Ready signal received!"
echo "[entrypoint] Model path: $TB_PIPE_PATH"
echo "[entrypoint] Binding to: ${TB_RUNTIME_HOST}:${TB_RUNTIME_PORT}"

# Start vLLM server with LoRA support
# The model is read from FIFO (decrypted weights from sentinel)
exec vllm serve \
    --model "$TB_PIPE_PATH" \
    --host "$TB_RUNTIME_HOST" \
    --port "$TB_RUNTIME_PORT" \
    --enable-lora \
    --max-loras "$TB_MAX_LORAS" \
    --max-lora-rank "$TB_MAX_LORA_RANK"
