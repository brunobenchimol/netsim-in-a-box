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
# Copy main.go and handlers.go 
COPY main.go handlers.go ./

# Build the static, CGO-disabled binary
# We output it to a known location.
RUN CGO_ENABLED=0 go build -ldflags="-w -s" -o /app/tc-ui .

# --- STAGE 2: V3 Tailwind Builder (builder-css) ---
# Builds the UI from /frontend
FROM node:20-alpine AS builder-css
WORKDIR /src/frontend
# Copy package.json. Use * to tolerate npm/yarn lockfiles
COPY frontend/package.json frontend/package-lock.json* ./
RUN npm ci
COPY frontend/tailwind.config.js frontend/input.css frontend/index.html frontend/app.js ./
RUN npm run build:css

# --- STAGE 3: Final Runtime Image (final) ---
# Use modern Ubuntu 24.04, supporting the build architecture
FROM ${ARCH}/ubuntu:24.04

# Set non-interactive for package installs
ENV DEBIAN_FRONTEND=noninteractive

# Install all runtime dependencies identified by our preflight checks
RUN apt update && apt install -y --no-install-recommends \
    iproute2 \
    iptables \
    ufw \
    kmod \
    ca-certificates \
    iperf3 \
    squid \
    supervisor \
    && \
    # Clean up apt cache
    apt clean \
    && rm -rf /var/lib/apt/lists/*

# Set the working directory for the application
WORKDIR /app

# Copy the compiled Go binary
COPY --from=builder-go /app/tc-ui /usr/local/bin/tc-ui

# Copy the built V4 UI
# This serves /
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
# Expose iperf3 port
EXPOSE 5202

# Run supervisord as the main command
CMD ["/usr/bin/supervisord", "-c", "/etc/supervisord.conf"]
