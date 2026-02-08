.PHONY: help build docker-build podman-build podman-build-multi kind-load deploy-cp deploy-agent test clean

# Variables
IMAGE_NAME := transporter
IMAGE_TAG := 0.1.0
KIND_CLUSTER := kind
PLATFORMS := linux/arm64,linux/amd64

help: ## Show this help message
	@echo 'Usage: make [target]'
	@echo ''
	@echo 'Available targets:'
	@awk 'BEGIN {FS = ":.*?## "} /^[a-zA-Z_-]+:.*?## / {printf "  %-20s %s\n", $$1, $$2}' $(MAKEFILE_LIST)

build: ## Build the transporter binary
	@echo "🔨 Building transporter binary..."
	@mkdir -p bin
	@go build -o bin/transporter cmd/transporter/main.go
	@echo "✅ Binary built: bin/transporter"

build-producer: ## Build the event producer binary
	@echo "🔨 Building event-producer binary..."
	@mkdir -p bin
	@go build -o bin/event-producer cmd/event-producer/main.go
	@echo "✅ Binary built: bin/event-producer"

build-all: build build-producer ## Build all binaries
	@echo "✅ All binaries built"

docker-build: ## Build Docker image (for backward compatibility)
	@echo "🐳 Building Docker image with Docker..."
	@docker build -t $(IMAGE_NAME):$(IMAGE_TAG) .
	@echo "✅ Docker image built: $(IMAGE_NAME):$(IMAGE_TAG)"

podman-build: ## Build distroless image with podman (arm64 only for testing)
	@echo "🏗️  Building distroless image with podman (arm64)..."
	@podman build --platform linux/arm64 \
		--build-arg TARGETOS=linux \
		--build-arg TARGETARCH=arm64 \
		-t $(IMAGE_NAME):$(IMAGE_TAG) .
	@echo "✅ Podman distroless image built: $(IMAGE_NAME):$(IMAGE_TAG)"

podman-build-multi: ## Build multi-arch distroless image with podman
	@echo "🏗️  Building multi-arch distroless image with podman..."
	@podman build --platform $(PLATFORMS) -t $(IMAGE_NAME):$(IMAGE_TAG) .
	@echo "✅ Multi-arch distroless image built: $(IMAGE_NAME):$(IMAGE_TAG)"

kind-load: podman-build ## Load podman image into kind cluster
	@echo "📦 Loading image into kind cluster..."
	@kind load docker-image $(IMAGE_NAME):$(IMAGE_TAG) --name $(KIND_CLUSTER)
	@echo "✅ Image loaded into kind cluster"

deploy-cp: ## Deploy Control Plane to kind cluster
	@echo "🚀 Deploying Control Plane..."
	@helm dependency update deploy/helm/transporter-cp
	@helm upgrade --install transporter-cp deploy/helm/transporter-cp \
		--namespace transporter-system --create-namespace \
		--wait
	@echo "✅ Control Plane deployed"

deploy-agent: ## Deploy Agent to kind cluster
	@echo "🚀 Deploying Agent..."
	@helm upgrade --install transporter-agent deploy/helm/transporter-agent \
		--namespace transporter-system --create-namespace \
		--set agent.agentID=kind-agent-1 \
		--set agent.clusterName=kind-cluster \
		--wait
	@echo "✅ Agent deployed"

status: ## Show deployment status
	@echo "📊 Deployment Status:"
	@kubectl get pods -n transporter-system
	@echo ""
	@kubectl get svc -n transporter-system

logs-cp: ## Show Control Plane logs
	@kubectl logs -n transporter-system -l app.kubernetes.io/component=control-plane -f

logs-agent: ## Show Agent logs
	@kubectl logs -n transporter-system -l app.kubernetes.io/component=data-plane-agent -f

test: ## Run end-to-end test
	@echo "🧪 Running end-to-end test..."
	@kubectl run test-event --rm -i --restart=Never --image=curlimages/curl -- \
		curl -X POST http://transporter-cp:8080/health
	@echo "✅ Test completed"

clean: ## Clean up deployments
	@echo "🧹 Cleaning up..."
	@helm uninstall transporter-agent -n transporter-system --ignore-not-found
	@helm uninstall transporter-cp -n transporter-system --ignore-not-found
	@kubectl delete namespace transporter-system --ignore-not-found
	@echo "✅ Cleanup complete"

all: build podman-build kind-load deploy-cp deploy-agent ## Build, load, and deploy everything
	@echo "🎉 All done!"
