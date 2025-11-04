# Set a default architecture (can be overridden with --build-arg)
ARG ARCH="amd64"

# --- STAGE 1: Go Builder (builder-go) ---
# Use a modern, secure Go version on Alpine for a small build stage
FROM golang:1.24-alpine AS builder-go

# Set the working directory
WORKDIR /src

# Copy only the files needed to build the Go binary
# This optimizes the Docker layer cache.
COPY go.mod go.sum ./
RUN go mod download

# Copy the rest of the Go source code
COPY main.go ./
COPY tc.go ./

# Build the static, CGO-disabled binary
# We output it to a known location.
RUN CGO_ENABLED=0 go build -ldflags="-w -s" -o /app/tc-ui .

# --- STAGE 2: Tailwind CSS Builder (builder-css) ---
# This stage builds our production.css
FROM node:20-alpine AS builder-css

# Set working directory for the UI
WORKDIR /src/frontend

# --- CACHE OPTIMIZATION ---
# 1. Copy *only* the package definition files.
COPY frontend/package.json frontend/package-lock.json ./

# 2. Install dependencies.
# This layer will only be rebuilt if package.json/lock changes.
RUN npm ci

# 3. Now, copy the rest of the UI source code.
# If only index.html or app.js changes, 'npm ci' above won't re-run.
COPY frontend/tailwind.config.js ./
COPY frontend/input.css ./
COPY frontend/index.html ./
COPY frontend/app.js ./

# 4. Build the production CSS.
# This will run whenever the UI source code changes.
RUN npm run build:css
# --- END OPTIMIZATION ---

# --- STAGE 3: Final Runtime Image (final) ---
# Use modern Ubuntu 24.04, supporting the build architecture
FROM ${ARCH}/ubuntu:24.04

# Set non-interactive for package installs
ENV DEBIAN_FRONTEND=noninteractive

# Install all runtime dependencies identified by our preflight checks
RUN apt update && apt install -y --no-install-recommends \
    tcpdump \
    iproute2 \
    kmod \
    ca-certificates \
    curl \
    jq \
    python3-pip \
    # Add Squid and Supervisor
    squid \
    supervisor \
    && \
    # Install tcconfig using the official script
    curl -sSL https://raw.githubusercontent.com/thombashi/tcconfig/master/scripts/installer.sh | bash \
    && \
    # Clean up apt cache
    apt clean \
    && rm -rf /var/lib/apt/lists/*

# Set the working directory for the application
WORKDIR /app

# Copy the compiled Go binary from the build stage
COPY --from=builder-go /app/tc-ui /usr/local/bin/tc-ui

# Copy the built UI files from the 'builder-css' stage
# The main.go handler expects them in './frontend'
COPY --from=builder-css /src/frontend/index.html ./frontend/
COPY --from=builder-css /src/frontend/app.js ./frontend/
COPY --from=builder-css /src/frontend/production.css ./frontend/production.css

# Copy squid + supervisord config files
COPY squid/squid.conf /etc/squid/squid.conf
COPY supervisord/supervisord.conf /etc/supervisord.conf

# Expose the API port
EXPOSE 2023

# Expose the Squid proxy port
EXPOSE 3128

# Run supervisord as the main command
CMD ["/usr/bin/supervisord", "-c", "/etc/supervisord.conf"]
