# Build stage
FROM golang:1.25-alpine AS builder

# Build arguments for multi-arch
ARG TARGETOS=linux
ARG TARGETARCH=arm64

WORKDIR /build

# Copy go mod files
COPY go.mod go.sum ./
RUN go mod download

# Copy source code
COPY . .

# Build the binary (static binary with size optimization)
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -a -installsuffix cgo \
    -ldflags="-w -s -X main.version=0.1.0 -X main.buildTime=$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
    -o transporter cmd/transporter/main.go

# Verify binary exists
RUN ls -lh /build/transporter

# Final stage - using Google's distroless base image
FROM gcr.io/distroless/static-debian12:nonroot

LABEL org.opencontainers.image.source="https://github.com/suyog1pathak/transporter"
LABEL org.opencontainers.image.description="Transporter - Event-driven multi-cluster Kubernetes management"
LABEL org.opencontainers.image.licenses="MIT"

# Copy the binary from builder
COPY --from=builder /build/transporter /app/transporter

# distroless runs as nonroot user by default (uid 65532)
# No shell, no package manager - minimal attack surface

# Set working directory
WORKDIR /app

ENTRYPOINT ["/app/transporter"]
