.PHONY: help build build-producer build-all podman-build create-clusters delete-clusters \
        kind-load deploy-memphis deploy-cp deploy-agent status logs-cp logs-agent \
        port-forward-memphis port-forward-cp send-test-event health clean

# Variables
IMAGE_NAME    := transporter
IMAGE_TAG     := 0.1.0
CP_CLUSTER    := cp-cluster
AGENT_CLUSTER := agent-cluster

# Get cp-cluster node IP (used by agent to reach CP NodePort)
CP_NODE_IP := $(shell docker inspect $(CP_CLUSTER)-control-plane \
                --format '{{.NetworkSettings.Networks.kind.IPAddress}}' 2>/dev/null)

help: ## Show this help message
	@echo 'Usage: make [target]'
	@echo ''
	@echo 'Available targets:'
	@awk 'BEGIN {FS = ":.*?## "} /^[a-zA-Z_-]+:.*?## / {printf "  %-20s %s\n", $$1, $$2}' $(MAKEFILE_LIST)

# ── Build ─────────────────────────────────────────────────────────────────────

build: ## Build the transporter binary
	@echo "Building transporter binary..."
	@mkdir -p bin
	@go build -o bin/transporter ./cmd/transporter/
	@echo "Binary built: bin/transporter"

build-producer: ## Build the event-producer binary
	@echo "Building event-producer binary..."
	@mkdir -p bin
	@go build -o bin/event-producer ./cmd/event-producer/
	@echo "Binary built: bin/event-producer"

build-all: build build-producer ## Build all binaries

podman-build: ## Build container image with podman (arm64)
	@echo "Building image with podman..."
	@podman build --platform linux/arm64 \
		--build-arg TARGETOS=linux \
		--build-arg TARGETARCH=arm64 \
		-t $(IMAGE_NAME):$(IMAGE_TAG) .
	@echo "Image built: $(IMAGE_NAME):$(IMAGE_TAG)"

# ── Clusters ──────────────────────────────────────────────────────────────────

create-clusters: ## Create both kind clusters
	@echo "Creating cp-cluster..."
	@kind create cluster --config test-bed/kind/cp-cluster-config.yaml
	@echo "Creating agent-cluster..."
	@kind create cluster --config test-bed/kind/agent-cluster-config.yaml
	@echo "Both clusters created"

delete-clusters: ## Delete both kind clusters
	@kind delete cluster --name $(CP_CLUSTER) 2>/dev/null || true
	@kind delete cluster --name $(AGENT_CLUSTER) 2>/dev/null || true
	@echo "Clusters deleted"

kind-load: ## Save podman image and load into both kind clusters
	@echo "Saving image to tar..."
	@podman save $(IMAGE_NAME):$(IMAGE_TAG) -o /tmp/transporter.tar
	@echo "Loading into $(CP_CLUSTER)..."
	@kind load image-archive /tmp/transporter.tar --name $(CP_CLUSTER)
	@echo "Loading into $(AGENT_CLUSTER)..."
	@kind load image-archive /tmp/transporter.tar --name $(AGENT_CLUSTER)
	@rm -f /tmp/transporter.tar
	@echo "Image loaded into both clusters"

# ── Deploy ────────────────────────────────────────────────────────────────────

deploy-memphis: ## Deploy Memphis + PostgreSQL to cp-cluster
	@echo "Deploying Memphis to $(CP_CLUSTER)..."
	@kubectl config use-context kind-$(CP_CLUSTER)
	@kubectl create namespace transporter-system --dry-run=client -o yaml | kubectl apply -f -
	@kubectl apply -f test-bed/memphis-with-deps.yaml
	@echo "Waiting for PostgreSQL..."
	@kubectl rollout status statefulset/memphis-postgres -n transporter-system --timeout=120s
	@echo "Waiting for Memphis..."
	@kubectl rollout status statefulset/memphis -n transporter-system --timeout=180s
	@echo "Memphis ready"

deploy-cp: ## Deploy Control Plane to cp-cluster (Memphis + Redis via Helm)
	@echo "Deploying CP to $(CP_CLUSTER)..."
	@kubectl config use-context kind-$(CP_CLUSTER)
	@helm dependency update transporter-cp
	@helm upgrade --install transporter-cp transporter-cp \
		--namespace transporter-system --create-namespace \
		--set cp.memphis.enabled=true \
		--set cp.memphis.connectionToken=memphis \
		--set cp.memphis.host=memphis \
		--set service.type=NodePort \
		--set service.nodePort=30080 \
		--wait --timeout 5m
	@echo "CP deployed"

deploy-agent: ## Deploy Agent to agent-cluster (points at cp-cluster NodePort)
	@echo "CP node IP: $(CP_NODE_IP)"
	@kubectl config use-context kind-$(AGENT_CLUSTER)
	@helm upgrade --install transporter-agent transporter-agent \
		--namespace transporter-system --create-namespace \
		--set agent.agentID=agent-cluster-agent-1 \
		--set agent.clusterName=agent-cluster \
		--set agent.cpURL=ws://$(CP_NODE_IP):30080/ws \
		--wait --timeout 3m
	@echo "Agent deployed, connecting to ws://$(CP_NODE_IP):30080/ws"

# ── Observe ───────────────────────────────────────────────────────────────────

status: ## Show pods and services in both clusters
	@echo "=== $(CP_CLUSTER) ==="
	@kubectl config use-context kind-$(CP_CLUSTER) && kubectl get pods,svc -n transporter-system
	@echo ""
	@echo "=== $(AGENT_CLUSTER) ==="
	@kubectl config use-context kind-$(AGENT_CLUSTER) && kubectl get pods -n transporter-system

logs-cp: ## Stream Control Plane logs
	@kubectl config use-context kind-$(CP_CLUSTER)
	@kubectl logs -n transporter-system -l app.kubernetes.io/name=transporter-cp -f

logs-agent: ## Stream Agent logs
	@kubectl config use-context kind-$(AGENT_CLUSTER)
	@kubectl logs -n transporter-system -l app.kubernetes.io/name=transporter-agent -f

# ── Test ──────────────────────────────────────────────────────────────────────

port-forward-memphis: ## Port-forward Memphis broker to localhost:6666 (run in separate terminal)
	@kubectl config use-context kind-$(CP_CLUSTER)
	@kubectl port-forward -n transporter-system svc/memphis 6666:6666

port-forward-cp: ## Port-forward CP to localhost:8080 (run in separate terminal)
	@kubectl config use-context kind-$(CP_CLUSTER)
	@kubectl port-forward -n transporter-system svc/transporter-cp 8080:8080

send-test-event: build-producer ## Build event-producer and send a test k8s event via Memphis
	@echo "Sending test event via Memphis..."
	@./bin/event-producer k8s \
		--agent agent-cluster-agent-1 \
		--manifest test-bed/examples/manifests/namespace.yaml \
		--mode memphis \
		--memphis-host localhost \
		--memphis-connection-token memphis
	@echo ""
	@echo "Verify in agent-cluster:"
	@echo "  kubectl config use-context kind-$(AGENT_CLUSTER) && kubectl get namespace transporter-test"

health: ## Check CP health endpoint (requires NodePort 30080 accessible on localhost)
	@curl -s http://localhost:30080/health | python3 -m json.tool 2>/dev/null || curl -s http://localhost:30080/health

# ── Cleanup ───────────────────────────────────────────────────────────────────

clean: ## Remove Helm releases and namespaces from both clusters
	@kubectl config use-context kind-$(CP_CLUSTER) 2>/dev/null && \
		helm uninstall transporter-cp -n transporter-system --ignore-not-found 2>/dev/null && \
		kubectl delete namespace transporter-system --ignore-not-found 2>/dev/null || true
	@kubectl config use-context kind-$(AGENT_CLUSTER) 2>/dev/null && \
		helm uninstall transporter-agent -n transporter-system --ignore-not-found 2>/dev/null && \
		kubectl delete namespace transporter-system --ignore-not-found 2>/dev/null || true
