FROM python:3.11-slim

WORKDIR /app

# Install dependencies
COPY requirements.txt .
RUN pip install --no-cache-dir -r requirements.txt

# Copy server code
COPY server.py .

# Create data directory
RUN mkdir -p /data

# Environment defaults
ENV BLOB_PORT=9000
ENV BLOB_DATA_DIR=/data

# Expose port
EXPOSE 9000

# Health check
HEALTHCHECK --interval=10s --timeout=5s --start-period=5s --retries=3 \
    CMD curl -f http://localhost:9000/health || exit 1

# Run server
CMD ["python", "server.py"]
