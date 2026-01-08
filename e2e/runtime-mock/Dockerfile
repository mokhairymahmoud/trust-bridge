# TrustBridge Runtime Mock Container
# For E2E testing - simulates runtime behavior without GPU requirements

FROM python:3.11-slim

# Labels
LABEL org.opencontainers.image.title="TrustBridge Runtime Mock"
LABEL org.opencontainers.image.description="Mock runtime for E2E testing of TrustBridge"

# Create non-root user for security
RUN useradd --create-home --shell /bin/bash appuser

# Set working directory
WORKDIR /app

# Copy requirements first for better layer caching
COPY requirements.txt .

# Install dependencies
RUN pip install --no-cache-dir -r requirements.txt

# Copy application code
COPY app.py .

# Set ownership
RUN chown -R appuser:appuser /app

# Environment defaults (matching sentinel config)
ENV TB_PIPE_PATH=/dev/shm/model-pipe
ENV TB_READY_SIGNAL=/dev/shm/weights/ready.signal
ENV TB_RUNTIME_HOST=127.0.0.1
ENV TB_RUNTIME_PORT=8081

# Expose the runtime port
EXPOSE 8081

# Health check
HEALTHCHECK --interval=10s --timeout=5s --start-period=5s --retries=3 \
    CMD python -c "import urllib.request; urllib.request.urlopen('http://127.0.0.1:8081/health')" || exit 1

# Run as non-root user
USER appuser

# Start the mock server
CMD ["python", "app.py"]
