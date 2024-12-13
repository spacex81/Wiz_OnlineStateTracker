# Variables
DOCKER_USERNAME=spacex81359
DOCKER_IMAGE_NAME=friend-tracker2
DOCKER_TAG=latest
SERVER_PATH=./server
# 
AWS_ACCOUNT_ID=784319439312
REGION=ap-northeast-2
REPO_NAME=friend-tracker2
IMAGE_NAME=$(AWS_ACCOUNT_ID).dkr.ecr.$(REGION).amazonaws.com/$(REPO_NAME):latest
DOCKERFILE_PATH=./server/Dockerfile
BUILD_CONTEXT=./server
# 

.PHONY: all build push clean

# Default target (runs when you just type 'make')
# all: build login
all: login build

# Build the Docker image
build:
	@echo "Building Docker image for multiple platforms..."
	docker buildx create --use || echo "Buildx already created"
	docker buildx build \
		--no-cache \
		--platform linux/amd64 \
		--push \
		-t $(IMAGE_NAME) \
		-f $(DOCKERFILE_PATH) \
		$(BUILD_CONTEXT)



# Login to ECR
login:
	aws ecr get-login-password --region $(REGION) | docker login --username AWS --password-stdin $(AWS_ACCOUNT_ID).dkr.ecr.$(REGION).amazonaws.com



# Clean up Docker images (optional)
clean:
	docker rmi $(REPO_NAME):latest $(IMAGE_NAME) || true


# Help message
help:
	@echo "Makefile Commands:"
	@echo "  make build   - Build the Docker image"
	@echo "  make tag     - Tag the image for ECR"
	@echo "  make login   - Login to AWS ECR"
	@echo "  make push    - Push the image to ECR"
	@echo "  make deploy  - Full process: build, tag, login, and push"
	@echo "  make clean   - Remove local images"

generate:
	protoc --go_out=./client --go-grpc_out=./client ./protocol/*.proto
	protoc --go_out=./server --go-grpc_out=./server ./protocol/*.proto
