# Build stage
FROM golang:1.22-alpine AS builder

WORKDIR /app
COPY go.mod go.sum* ./
RUN go mod download
COPY main.go .
RUN CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo -o token-watcher .

# Final stage
FROM alpine:3.19
RUN apk --no-cache add ca-certificates

WORKDIR /

COPY --from=builder /app/token-watcher .
COPY --from=builder /app/token-watcher /usr/local/bin/token-watcher

# The token file is usually replaced with new inode, so we watch the directory
# Environment variables needed:
# - KUBERNETES_APISERVER_ENDPOINT: the Kubernetes API server endpoint
# - namespace: the namespace to use in the kubeconfig
ENTRYPOINT ["/token-watcher"]
