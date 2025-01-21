# Build Go app
FROM golang:1.23-alpine
WORKDIR /app

# Copy go mod files first to leverage Docker cache
COPY go.mod go.sum ./
RUN go mod download
# Copy source code
COPY . .
WORKDIR /app/cmd/main
# Build with specific optimizations
RUN CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -ldflags="-w -s" -o ../../server

WORKDIR /app

# Set permissions
RUN chmod +x server

# Expose ports
EXPOSE 3002

CMD ["./server"]