package executor

import (
	"context"
	"fmt"
	"time"

	"github.com/suyog1pathak/transporter/internal/model"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/runtime/serializer/yaml"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/discovery/cached/memory"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/restmapper"
	"k8s.io/client-go/tools/clientcmd"
)

// K8sExecutor executes Kubernetes operations
type K8sExecutor struct {
	clientset       *kubernetes.Clientset
	dynamicClient   dynamic.Interface
	discoveryClient discovery.CachedDiscoveryInterface
	mapper          meta.RESTMapper
}

// Config holds Kubernetes client configuration
type Config struct {
	KubeconfigPath string // Path to kubeconfig file (empty for in-cluster config)
	InCluster      bool   // Use in-cluster configuration
}

// NewK8sExecutor creates a new Kubernetes executor
func NewK8sExecutor(config Config) (*K8sExecutor, error) {
	var restConfig *rest.Config
	var err error

	if config.InCluster {
		// Use in-cluster config (for agents running inside K8s)
		restConfig, err = rest.InClusterConfig()
		if err != nil {
			return nil, fmt.Errorf("failed to get in-cluster config: %w", err)
		}
	} else {
		// Use kubeconfig file
		restConfig, err = clientcmd.BuildConfigFromFlags("", config.KubeconfigPath)
		if err != nil {
			return nil, fmt.Errorf("failed to build config from kubeconfig: %w", err)
		}
	}

	// Create clientset
	clientset, err := kubernetes.NewForConfig(restConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create clientset: %w", err)
	}

	// Create dynamic client
	dynamicClient, err := dynamic.NewForConfig(restConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create dynamic client: %w", err)
	}

	// Create discovery client
	discoveryClient := memory.NewMemCacheClient(clientset.Discovery())

	// Create REST mapper
	mapper := restmapper.NewDeferredDiscoveryRESTMapper(discoveryClient)

	return &K8sExecutor{
		clientset:       clientset,
		dynamicClient:   dynamicClient,
		discoveryClient: discoveryClient,
		mapper:          mapper,
	}, nil
}

// ExecuteEvent executes a Kubernetes event
func (ke *K8sExecutor) ExecuteEvent(event *model.Event) (*model.EventResult, error) {
	startTime := time.Now()

	switch event.Type {
	case model.EventTypeK8sResource:
		return ke.executeK8sResource(event)
	case model.EventTypeScript:
		return nil, fmt.Errorf("script execution not yet implemented")
	case model.EventTypePolicy:
		return nil, fmt.Errorf("policy enforcement not yet implemented")
	default:
		return &model.EventResult{
			Success:      false,
			ErrorMessage: fmt.Sprintf("unknown event type: %s", event.Type),
			CompletedAt:  time.Now(),
			Duration:     time.Since(startTime),
		}, nil
	}
}

// executeK8sResource applies Kubernetes manifests
func (ke *K8sExecutor) executeK8sResource(event *model.Event) (*model.EventResult, error) {
	startTime := time.Now()
	resourceStatuses := make([]model.ResourceStatus, 0)

	for _, manifestYAML := range event.Payload.Manifests {
		status := ke.applyManifest(manifestYAML)
		resourceStatuses = append(resourceStatuses, status)
	}

	// Check if all succeeded
	allSucceeded := true
	var errorMessage string
	for _, status := range resourceStatuses {
		if status.Status == "failed" {
			allSucceeded = false
			if errorMessage == "" {
				errorMessage = status.Message
			} else {
				errorMessage += "; " + status.Message
			}
		}
	}

	return &model.EventResult{
		Success:        allSucceeded,
		ResourceStatus: resourceStatuses,
		ErrorMessage:   errorMessage,
		CompletedAt:    time.Now(),
		Duration:       time.Since(startTime),
	}, nil
}

// applyManifest applies a single YAML manifest
func (ke *K8sExecutor) applyManifest(manifestYAML string) model.ResourceStatus {
	// Decode YAML to unstructured object
	decoder := yaml.NewDecodingSerializer(unstructured.UnstructuredJSONScheme)
	obj := &unstructured.Unstructured{}

	_, gvk, err := decoder.Decode([]byte(manifestYAML), nil, obj)
	if err != nil {
		return model.ResourceStatus{
			Kind:    "Unknown",
			Name:    "Unknown",
			Status:  "failed",
			Message: fmt.Sprintf("failed to decode YAML: %v", err),
		}
	}

	// Find GVR using mapper
	mapping, err := ke.mapper.RESTMapping(gvk.GroupKind(), gvk.Version)
	if err != nil {
		return model.ResourceStatus{
			Kind:       obj.GetKind(),
			Name:       obj.GetName(),
			Namespace:  obj.GetNamespace(),
			APIVersion: obj.GetAPIVersion(),
			Status:     "failed",
			Message:    fmt.Sprintf("failed to find API resource: %v", err),
		}
	}

	// Get resource interface
	var dr dynamic.ResourceInterface
	if mapping.Scope.Name() == meta.RESTScopeNameNamespace {
		// Namespaced resource
		namespace := obj.GetNamespace()
		if namespace == "" {
			namespace = "default"
		}
		dr = ke.dynamicClient.Resource(mapping.Resource).Namespace(namespace)
	} else {
		// Cluster-scoped resource
		dr = ke.dynamicClient.Resource(mapping.Resource)
	}

	// Try to get existing resource
	ctx := context.Background()
	existing, err := dr.Get(ctx, obj.GetName(), metav1.GetOptions{})

	if err != nil {
		if errors.IsNotFound(err) {
			// Resource doesn't exist - create it
			_, err := dr.Create(ctx, obj, metav1.CreateOptions{})
			if err != nil {
				return model.ResourceStatus{
					Kind:       obj.GetKind(),
					Name:       obj.GetName(),
					Namespace:  obj.GetNamespace(),
					APIVersion: obj.GetAPIVersion(),
					Status:     "failed",
					Message:    fmt.Sprintf("failed to create: %v", err),
				}
			}

			return model.ResourceStatus{
				Kind:       obj.GetKind(),
				Name:       obj.GetName(),
				Namespace:  obj.GetNamespace(),
				APIVersion: obj.GetAPIVersion(),
				Status:     "created",
				Message:    "Resource created successfully",
			}
		}

		// Some other error
		return model.ResourceStatus{
			Kind:       obj.GetKind(),
			Name:       obj.GetName(),
			Namespace:  obj.GetNamespace(),
			APIVersion: obj.GetAPIVersion(),
			Status:     "failed",
			Message:    fmt.Sprintf("failed to get resource: %v", err),
		}
	}

	// Resource exists - update it
	obj.SetResourceVersion(existing.GetResourceVersion())
	_, err = dr.Update(ctx, obj, metav1.UpdateOptions{})
	if err != nil {
		return model.ResourceStatus{
			Kind:       obj.GetKind(),
			Name:       obj.GetName(),
			Namespace:  obj.GetNamespace(),
			APIVersion: obj.GetAPIVersion(),
			Status:     "failed",
			Message:    fmt.Sprintf("failed to update: %v", err),
		}
	}

	return model.ResourceStatus{
		Kind:       obj.GetKind(),
		Name:       obj.GetName(),
		Namespace:  obj.GetNamespace(),
		APIVersion: obj.GetAPIVersion(),
		Status:     "updated",
		Message:    "Resource updated successfully",
	}
}

// ValidateManifests validates Kubernetes manifests without applying them
func (ke *K8sExecutor) ValidateManifests(manifests []string) error {
	decoder := yaml.NewDecodingSerializer(unstructured.UnstructuredJSONScheme)

	for _, manifestYAML := range manifests {
		obj := &unstructured.Unstructured{}
		_, gvk, err := decoder.Decode([]byte(manifestYAML), nil, obj)
		if err != nil {
			return fmt.Errorf("invalid YAML: %w", err)
		}

		// Check if we can find the API resource
		_, err = ke.mapper.RESTMapping(gvk.GroupKind(), gvk.Version)
		if err != nil {
			return fmt.Errorf("unknown API resource %s: %w", gvk.String(), err)
		}
	}

	return nil
}

// DeleteResource deletes a Kubernetes resource
func (ke *K8sExecutor) DeleteResource(kind, name, namespace, apiVersion string) error {
	// Parse GVK
	gv, err := schema.ParseGroupVersion(apiVersion)
	if err != nil {
		return fmt.Errorf("invalid API version: %w", err)
	}

	gvk := schema.GroupVersionKind{
		Group:   gv.Group,
		Version: gv.Version,
		Kind:    kind,
	}

	// Find GVR
	mapping, err := ke.mapper.RESTMapping(gvk.GroupKind(), gvk.Version)
	if err != nil {
		return fmt.Errorf("failed to find API resource: %w", err)
	}

	// Get resource interface
	var dr dynamic.ResourceInterface
	if mapping.Scope.Name() == meta.RESTScopeNameNamespace {
		if namespace == "" {
			namespace = "default"
		}
		dr = ke.dynamicClient.Resource(mapping.Resource).Namespace(namespace)
	} else {
		dr = ke.dynamicClient.Resource(mapping.Resource)
	}

	// Delete resource
	ctx := context.Background()
	err = dr.Delete(ctx, name, metav1.DeleteOptions{})
	if err != nil && !errors.IsNotFound(err) {
		return fmt.Errorf("failed to delete resource: %w", err)
	}

	return nil
}

// GetResource retrieves a Kubernetes resource
func (ke *K8sExecutor) GetResource(kind, name, namespace, apiVersion string) (*unstructured.Unstructured, error) {
	// Similar to DeleteResource but returns the object
	gv, err := schema.ParseGroupVersion(apiVersion)
	if err != nil {
		return nil, fmt.Errorf("invalid API version: %w", err)
	}

	gvk := schema.GroupVersionKind{
		Group:   gv.Group,
		Version: gv.Version,
		Kind:    kind,
	}

	mapping, err := ke.mapper.RESTMapping(gvk.GroupKind(), gvk.Version)
	if err != nil {
		return nil, fmt.Errorf("failed to find API resource: %w", err)
	}

	var dr dynamic.ResourceInterface
	if mapping.Scope.Name() == meta.RESTScopeNameNamespace {
		if namespace == "" {
			namespace = "default"
		}
		dr = ke.dynamicClient.Resource(mapping.Resource).Namespace(namespace)
	} else {
		dr = ke.dynamicClient.Resource(mapping.Resource)
	}

	ctx := context.Background()
	obj, err := dr.Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to get resource: %w", err)
	}

	return obj, nil
}
