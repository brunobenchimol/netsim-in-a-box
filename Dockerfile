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
COPY api_v2.go ./

# Build the static, CGO-disabled binary
# We output it to a known location.
RUN CGO_ENABLED=0 go build -ldflags="-w -s" -o /app/tc-ui .

# --- STAGE 2: V1 Tailwind Builder (builder-css-v1) ---
# Builds the V1 UI from /frontend-v1
FROM node:20-alpine AS builder-css-v1
WORKDIR /src/frontend-v1
# We must use frontend-v1/ prefix for all V1 files
COPY frontend-v1/package.json frontend-v1/package-lock.json ./
RUN npm ci
COPY frontend-v1/tailwind.config.js ./
COPY frontend-v1/input.css ./
COPY frontend-v1/index.html ./
COPY frontend-v1/app.js ./
RUN npm run build:css

# --- STAGE 3: V2 Tailwind Builder (builder-css-v2) ---
# Builds the V2 UI from /frontend
FROM node:20-alpine AS builder-css-v2
WORKDIR /src/frontend
# We use frontend/ prefix for all V2 files
COPY frontend/package.json frontend/package-lock.json ./
RUN npm ci
COPY frontend/tailwind.config.js ./
COPY frontend/input.css ./
COPY frontend/index.html ./
COPY frontend/app.js ./
RUN npm run build:css

# --- STAGE 4: Final Runtime Image (final) ---
# Use modern Ubuntu 24.04, supporting the build architecture
FROM ${ARCH}/ubuntu:24.04

# Set non-interactive for package installs
ENV DEBIAN_FRONTEND=noninteractive

# Install all runtime dependencies identified by our preflight checks
RUN apt update && apt install -y --no-install-recommends \
    tcpdump \
    iproute2 \
    iptables \
    ufw \
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

# Copy the compiled Go binary
COPY --from=builder-go /app/tc-ui /usr/local/bin/tc-ui

# Copy the built V1 UI from the 'builder-css-v1' stage
# This serves /old/
COPY --from=builder-css-v1 /src/frontend-v1/index.html ./frontend-v1/
COPY --from=builder-css-v1 /src/frontend-v1/app.js ./frontend-v1/
COPY --from=builder-css-v1 /src/frontend-v1/production.css ./frontend-v1/production.css

# Copy the built V2 UI from the 'builder-css-v2' stage
# This serves /
COPY --from=builder-css-v2 /src/frontend/index.html ./frontend/
COPY --from=builder-css-v2 /src/frontend/app.js ./frontend/
COPY --from=builder-css-v2 /src/frontend/production.css ./frontend/production.css


# Copy squid + supervisord config files
COPY squid/squid.conf /etc/squid/squid.conf
COPY supervisord/supervisord.conf /etc/supervisord.conf

# Expose the API port
EXPOSE 2023

# Expose the Squid proxy port
EXPOSE 3128

# Run supervisord as the main command
CMD ["/usr/bin/supervisord", "-c", "/etc/supervisord.conf"]
