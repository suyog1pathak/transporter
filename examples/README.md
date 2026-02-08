# Transporter Examples

This directory contains sample manifests and events for testing Transporter.

## Directory Structure

```
examples/
├── manifests/          # Sample Kubernetes manifests
│   ├── namespace.yaml
│   └── nginx-deployment.yaml
└── events/            # Sample event files
    └── sample-k8s-event.yaml
```

## Using the Event Producer

### Prerequisites

1. Transporter Control Plane running with Memphis
2. At least one agent connected
3. Event producer binary built: `make build-producer`

### Example 1: Create Namespace

Create a simple namespace:

```bash
./bin/event-producer k8s \
  --agent kind-agent-1 \
  --manifest examples/manifests/namespace.yaml \
  --created-by "user@example.com"
```

### Example 2: Deploy Application

Deploy nginx with multiple manifests:

```bash
# First create the namespace
./bin/event-producer k8s \
  --agent kind-agent-1 \
  --manifest examples/manifests/namespace.yaml

# Then deploy nginx
./bin/event-producer k8s \
  --agent kind-agent-1 \
  --manifest examples/manifests/nginx-deployment.yaml
```

### Example 3: Use Pre-defined Event

Create event from a YAML file:

```bash
./bin/event-producer from-file \
  --file examples/events/sample-k8s-event.yaml
```

### Example 4: Custom Memphis Connection

Connect to Memphis in a different location:

```bash
./bin/event-producer k8s \
  --agent production-agent-1 \
  --manifest examples/manifests/namespace.yaml \
  --memphis-host memphis.prod.example.com:6666 \
  --memphis-username admin \
  --memphis-password secretpass
```

### Example 5: Event with TTL and Priority

Create an urgent event with short TTL:

```bash
./bin/event-producer k8s \
  --agent kind-agent-1 \
  --manifest examples/manifests/namespace.yaml \
  --ttl 1h \
  --priority 10 \
  --created-by "oncall@example.com"
```

## Testing End-to-End

### Setup

1. Deploy Transporter to kind:
```bash
make all
```

2. Check agent is connected:
```bash
kubectl logs -n transporter-system -l app.kubernetes.io/component=control-plane | grep "Agent connected"
```

3. Port-forward Memphis (for local event producer):
```bash
kubectl port-forward -n transporter-system svc/memphis 6666:6666
```

### Create Test Event

In another terminal:

```bash
./bin/event-producer k8s \
  --agent kind-agent-1 \
  --manifest examples/manifests/namespace.yaml \
  --memphis-host localhost:6666
```

### Verify Execution

1. Check agent logs:
```bash
kubectl logs -n transporter-system -l app.kubernetes.io/component=data-plane-agent -f
```

Expected output:
```
📥 Received event event-xxx (type: k8s_resource)
💓 Heartbeat sent
✅ Event completed successfully
```

2. Verify namespace created:
```bash
kubectl get namespace transporter-test
```

### Check Event Status

Port-forward Control Plane:
```bash
kubectl port-forward -n transporter-system svc/transporter-cp 8080:8080
```

Check metrics:
```bash
curl http://localhost:8080/metrics
```

## Creating Custom Events

### Kubernetes Resource Event

Create a YAML file with your event:

```yaml
# my-event.yaml
type: "k8s_resource"
target_agent: "my-agent-id"
created_by: "automation@example.com"
ttl: "12h"
priority: 5

payload:
  manifests:
    - |
      apiVersion: v1
      kind: ConfigMap
      metadata:
        name: my-config
        namespace: default
      data:
        key: value
```

Publish it:
```bash
./bin/event-producer from-file --file my-event.yaml
```

## Troubleshooting

### Event Producer Can't Connect to Memphis

Check Memphis is accessible:
```bash
# If running in kind
kubectl port-forward -n transporter-system svc/memphis 6666:6666

# Test connection
nc -zv localhost 6666
```

### Event Not Processed

1. Check event was published:
```bash
# View Memphis UI
kubectl port-forward -n transporter-system svc/memphis 9000:9000
# Open http://localhost:9000
```

2. Check Control Plane logs:
```bash
kubectl logs -n transporter-system -l app.kubernetes.io/component=control-plane
```

3. Check agent is connected:
```bash
curl http://localhost:8080/metrics | jq '.agents'
```

### Agent Not Executing Event

Check agent ID matches:
```bash
# Get agent ID from CP logs
kubectl logs -n transporter-system -l app.kubernetes.io/component=control-plane | grep "Agent connected"

# Or check agent deployment
helm get values transporter-agent -n transporter-system | grep agentID
```

## Next Steps

- Create more complex multi-manifest deployments
- Test event expiration (short TTL)
- Test agent disconnection/reconnection
- Create events for multiple agents
- Build automation scripts around event producer
