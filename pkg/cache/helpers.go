package cache

import (
	"context"
	"errors"
	"fmt"
	"time"

	authv1 "k8s.io/api/authorization/v1"
	corev1 "k8s.io/api/core/v1"
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

func canClusterVerbResource(cli dynamic.Interface, gvr schema.GroupVersionResource, verb string) (bool, error) {
	// Check if we have cluster permissions to list the resource
	// create the cluster level SelfSubjectAccessReview
	cSSAR := &authv1.SelfSubjectAccessReview{
		Spec: authv1.SelfSubjectAccessReviewSpec{
			ResourceAttributes: &authv1.ResourceAttributes{
				Verb:     verb,
				Group:    gvr.Group,
				Version:  gvr.Version,
				Resource: gvr.Resource,
			},
		},
	}

	createdClusterSSAR, err := createSSAR(cli, cSSAR)
	if err != nil {
		return false, fmt.Errorf("encountered an error creating a cluster level SSAR: %w", err)
	}

	return createdClusterSSAR.Status.Allowed, nil
}

func canClusterListWatchResource(cli dynamic.Interface, gvr schema.GroupVersionResource) (bool, error) {
	canList, err := canClusterVerbResource(cli, gvr, "list")
	if err != nil {
		return false, err
	}

	canWatch, err := canClusterVerbResource(cli, gvr, "watch")
	if err != nil {
		return false, err
	}

	return canList && canWatch, nil
}

func getListWatchNamespacesForResource(cli dynamic.Interface, gvr schema.GroupVersionResource) (map[string]struct{}, error) {
	listNs, err := getNamespacesForVerbResource(cli, gvr, "list")
	if err != nil {
		return nil, err
	}

	watchNs, err := getNamespacesForVerbResource(cli, gvr, "watch")
	if err != nil {
		return nil, err
	}

	ns := make(map[string]struct{})

	// only add namespaces that are in both maps
	for k := range listNs {
		if _, ok := watchNs[k]; ok {
			ns[k] = struct{}{}
		}
	}

	return ns, nil
}

func getNamespacesForVerbResource(cli dynamic.Interface, gvr schema.GroupVersionResource, verb string) (map[string]struct{}, error) {
	permittedNs := make(map[string]struct{})
	nsClient := cli.Resource(corev1.SchemeGroupVersion.WithResource("namespaces"))
	nsList := &corev1.NamespaceList{}
	uNsList, err := nsClient.List(context.TODO(), metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("encountered an error when getting the list of namespaces on the cluster: %w", err)
	}

	err = runtime.DefaultUnstructuredConverter.FromUnstructured(uNsList.UnstructuredContent(), nsList)
	if err != nil {
		return nil, fmt.Errorf("encountered an error converting from unstructured: %w", err)
	}

	for _, ns := range nsList.Items {
		nsSSAR := &authv1.SelfSubjectAccessReview{
			Spec: authv1.SelfSubjectAccessReviewSpec{
				ResourceAttributes: &authv1.ResourceAttributes{
					Namespace: ns.Name,
					Verb:      verb,
					Group:     gvr.Group,
					Version:   gvr.Version,
					Resource:  gvr.Resource,
				},
			},
		}

		createdNsSSAR, err := createSSAR(cli, nsSSAR)
		if err != nil {
			return nil, fmt.Errorf("encountered an error creating a namespace level SSAR: %w", err)
		}

		if createdNsSSAR.Status.Allowed {
			permittedNs[ns.Name] = struct{}{}
		}
	}

	return permittedNs, nil
}
