package cache

import (
	"context"
	"errors"
	"fmt"
	"time"

	authv1 "k8s.io/api/authorization/v1"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
	"k8s.io/kubectl/pkg/scheme"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client/apiutil"
)

// Some default/helpful stuff
// --------------------------------
var defaultResyncTime = 10 * time.Hour

var globalCache = "__cluster-cache"

func defaultOpts(config *rest.Config, opts cache.Options) (cache.Options, error) {
	// Use the default Kubernetes Scheme if unset
	if opts.Scheme == nil {
		opts.Scheme = scheme.Scheme
	}

	// Construct a new Mapper if unset
	if opts.Mapper == nil {
		var err error
		opts.Mapper, err = apiutil.NewDiscoveryRESTMapper(config)
		if err != nil {
			return opts, fmt.Errorf("could not create RESTMapper from config")
		}
	}

	// Default the resync period to 10 hours if unset
	if opts.Resync == nil {
		opts.Resync = &defaultResyncTime
	}
	return opts, nil
}

// IsAPINamespaced returns true if the object is namespace scoped.
// For unstructured objects the gvk is found from the object itself.
func IsAPINamespaced(obj runtime.Object, scheme *runtime.Scheme, restmapper apimeta.RESTMapper) (bool, error) {
	gvk, err := apiutil.GVKForObject(obj, scheme)
	if err != nil {
		return false, err
	}

	return IsAPINamespacedWithGVK(gvk, scheme, restmapper)
}

// IsAPINamespacedWithGVK returns true if the object having the provided
// GVK is namespace scoped.
func IsAPINamespacedWithGVK(gk schema.GroupVersionKind, scheme *runtime.Scheme, restmapper apimeta.RESTMapper) (bool, error) {
	restmapping, err := restmapper.RESTMapping(schema.GroupKind{Group: gk.Group, Kind: gk.Kind})
	if err != nil {
		return false, fmt.Errorf("failed to get restmapping: %w", err)
	}

	scope := restmapping.Scope.Name()

	if scope == "" {
		return false, errors.New("scope cannot be identified, empty scope returned")
	}

	if scope != apimeta.RESTScopeNameRoot {
		return true, nil
	}
	return false, nil
}

// createSSAR is a helper function to create the SelfSubjectAccessReview on cluster and return
// the resulting SelfSubjectAccessReview in the response.
func createSSAR(cli dynamic.Interface, ssar *authv1.SelfSubjectAccessReview) (*authv1.SelfSubjectAccessReview, error) {
	ssarUC, err := runtime.DefaultUnstructuredConverter.ToUnstructured(ssar)
	if err != nil {
		return nil, fmt.Errorf("encountered an error converting to unstructured: %w", err)
	}

	uSSAR := &unstructured.Unstructured{}
	uSSAR.SetGroupVersionKind(ssar.GroupVersionKind())
	uSSAR.Object = ssarUC

	ssarClient := cli.Resource(authv1.SchemeGroupVersion.WithResource("selfsubjectaccessreviews"))
	uCreatedSSAR, err := ssarClient.Create(context.TODO(), uSSAR, metav1.CreateOptions{})
	if err != nil {
		return nil, fmt.Errorf("encountered an error creating a cluster level SSAR: %w", err)
	}

	createdSSAR := &authv1.SelfSubjectAccessReview{}
	err = runtime.DefaultUnstructuredConverter.FromUnstructured(uCreatedSSAR.UnstructuredContent(), createdSSAR)
	if err != nil {
		return nil, fmt.Errorf("encountered an error converting from unstructured: %w", err)
	}

	return createdSSAR, nil
}

// canVerbResource will create a SelfSubjectAccessReview for a given resource, verb, and namespace and return whether or
// not a user/ServiceAccount has the permissions to "verb" (get, list, watch, etc.) the given resource in the given namespace.
// A namespace value of ""(empty) will result in checking permissions in all namespaces (cluster-scoped)
func canVerbResource(cli dynamic.Interface, gvr schema.GroupVersionResource, verb string, namespace string) (bool, error) {
	// Check if we have cluster permissions to list the resource
	// create the cluster level SelfSubjectAccessReview
	ssar := &authv1.SelfSubjectAccessReview{
		Spec: authv1.SelfSubjectAccessReviewSpec{
			ResourceAttributes: &authv1.ResourceAttributes{
				Namespace: namespace,
				Verb:      verb,
				Group:     gvr.Group,
				Version:   gvr.Version,
				Resource:  gvr.Resource,
			},
		},
	}

	createdSSAR, err := createSSAR(cli, ssar)
	if err != nil {
		return false, fmt.Errorf("encountered an error creating a cluster level SSAR: %w", err)
	}

	return createdSSAR.Status.Allowed, nil
}

// canClusterListWatchResource is a helper function to determine if a user/ServiceAccount has permissions
// to list and watch a given resource across the cluster (all namespaces).
func canClusterListWatchResource(cli dynamic.Interface, gvr schema.GroupVersionResource) (bool, error) {
	return canListWatchResourceForNamespace(cli, gvr, "")
}

// canListWatchResourceForNamespace will create a SelfSubjectAccessReview to see if a user/ServiceAccount has
// permissions to list and watch a given resource in a given namespace. If the namespace is ""(empty) then it will
// check if the permissions are available in all namespaces (cluster-scoped). It returns true if the user/ServiceAccount
// has list and watch permissions for the given resource in the given namespace
func canListWatchResourceForNamespace(cli dynamic.Interface, gvr schema.GroupVersionResource, namespace string) (bool, error) {
	canList, err := canVerbResource(cli, gvr, "list", namespace)
	if err != nil {
		return false, err
	}

	canWatch, err := canVerbResource(cli, gvr, "watch", namespace)
	if err != nil {
		return false, err
	}

	return canList && canWatch, nil
}
