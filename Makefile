# Variables
DOCKER_USERNAME=spacex81359
DOCKER_IMAGE_NAME=friend-tracker2
DOCKER_TAG=latest
SERVER_PATH=./server

.PHONY: all build push clean

# Build the Docker image for the server
build:
	@echo "Building Docker image for multiple platforms..."
	docker buildx create --use || echo "Buildx already created"
	docker buildx build \
		--platform linux/amd64,linux/arm64 \
		-t $(DOCKER_USERNAME)/$(DOCKER_IMAGE_NAME):$(DOCKER_TAG) \
		--push \
		--load \
		$(SERVER_PATH)

# Push the Docker image to Docker Hub
push:
	@echo "Pushing Docker image to Docker Hub..."
	docker push $(DOCKER_USERNAME)/$(DOCKER_IMAGE_NAME):$(DOCKER_TAG)

# Clean up any intermediate Docker images
clean:
	@echo "Cleaning up unused Docker images..."
	docker image prune -f

# Build and push the Docker image
all: build push

generate:
	protoc --go_out=./client --go-grpc_out=./client ./protos/*.proto
	protoc --go_out=./server --go-grpc_out=./server ./protos/*.proto
