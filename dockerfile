# -------------------------------------------
# Stage 1 – Build (install dependencies)
# -------------------------------------------
FROM python:3.11-slim AS builder

WORKDIR /app

# Install build tools (needed for some Python wheels)
RUN apt-get update && apt-get install -y --no-install-recommends \
    build-essential \
    && rm -rf /var/lib/apt/lists/*

# Copy and install dependencies into a local dir for copying
COPY requirements.txt .
RUN pip install --no-cache-dir --prefix=/install -r requirements.txt

# -------------------------------------------
# Stage 2 – Final (minimal runtime image)
# -------------------------------------------
FROM python:3.11-slim

WORKDIR /app

# Copy installed packages from builder stage
COPY --from=builder /install /usr/local

# Create non-root user
RUN useradd -r -u 1001 -g root gateway

# Copy application source
COPY *.py ./
COPY .env* ./

# Change ownership
RUN chown -R gateway:root /app
USER gateway

EXPOSE 8080

# Run with uvicorn
CMD ["uvicorn", "main:app", "--host", "0.0.0.0", "--port", "8080"]
