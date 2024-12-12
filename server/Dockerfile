# Use the official Golang image as the base image
FROM --platform=$BUILDPLATFORM golang:1.23 AS builder

# Set the working directory in the container
WORKDIR /app

# Copy go mod and sum files first (for caching)
COPY go.mod go.sum ./

# Download all dependencies
RUN go mod download

# Copy the source code into the container
COPY . .

# Build the Go application
RUN GOOS=linux GOARCH=$(if [ "$(TARGETARCH)" = "arm64" ]; then echo "arm64"; else echo "amd64"; fi) go build -o server ./server.go

# Use a minimal image to run the server
FROM alpine:latest

# Set the working directory
WORKDIR /root/

# Copy the built binary from the builder stage
COPY --from=builder /app/server .

# Copy the necessary service files for gRPC
COPY --from=builder /app/service /service

# Expose the port for gRPC
EXPOSE 50051

# Run the binary
CMD ["./server"]
