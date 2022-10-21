package cache

import (
	"context"
	"fmt"
	"strings"

	apimeta "k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/apiutil"
)

// Cases
// --------------------
// 1. Watch a resource across the cluster
// 2. Watch a resource in specific namespaces
// 3. Watch a specific resource in a namespace
// --------------------

// Cache Stuff
// --------------------------------

type ScopedCache struct {
	nsCache      *NamespaceScopedCache
	clusterCache *ClusterScopedCache
	RESTMapper   apimeta.RESTMapper
	Scheme       *runtime.Scheme
	isStarted    bool
	cli          dynamic.Interface
}

func ScopedCacheBuilder() cache.NewCacheFunc {
	return func(config *rest.Config, opts cache.Options) (cache.Cache, error) {
		opts, err := defaultOpts(config, opts)
		if err != nil {
			return nil, err
		}

		namespacedCache := NewNamespaceScopedCache()
		namespacedCache.Scheme = opts.Scheme

		clusterCache := NewClusterScopedCache()
		clusterCache.Scheme = opts.Scheme

		cli, err := dynamic.NewForConfig(config)
		if err != nil {
			return nil, fmt.Errorf("error creating dynamic client: %w", err)
		}

		return &ScopedCache{nsCache: namespacedCache, Scheme: opts.Scheme, RESTMapper: opts.Mapper, clusterCache: clusterCache, cli: cli}, nil
	}
}

// client.Reader implementation
// ----------------------

func (sc *ScopedCache) Get(ctx context.Context, key client.ObjectKey, obj client.Object) error {
	var fetchObj runtime.Object
	isNamespaced, err := IsAPINamespaced(obj, sc.Scheme, sc.RESTMapper)
	if err != nil {
		return err
	}

	// obj could have an empty GVK, lets make sure we have a proper gvk
	gvk, err := apiutil.GVKForObject(obj, sc.Scheme)
	if err != nil {
		return fmt.Errorf("encountered an error getting GVK for object: %w", err)
	}

	_, gvkClusterScoped := sc.clusterCache.GvkInformers[gvk]

	if !isNamespaced || gvkClusterScoped || len(sc.nsCache.Namespaces) == 0 {
		fetchObj, err = sc.clusterCache.Get(key, gvk)
		if err != nil {
			return err
		}
	} else {
		fetchObj, err = sc.nsCache.Get(key, gvk)
		if err != nil {
			return err
		}
	}

	obj = fetchObj.(client.Object)
	return nil
}

func (sc *ScopedCache) List(ctx context.Context, list client.ObjectList, opts ...client.ListOption) error {
	listOpts := client.ListOptions{}
	listOpts.ApplyOptions(opts)

	var objs []runtime.Object

	isNamespaced, err := IsAPINamespaced(list, sc.Scheme, sc.RESTMapper)
	if err != nil {
		return err
	}

	// obj could have an empty GVK, lets make sure we have a proper gvk
	gvk, err := apiutil.GVKForObject(list, sc.Scheme)
	if err != nil {
		return fmt.Errorf("encountered an error getting GVK for object: %w", err)
	}

	gvkForListItems := schema.GroupVersionKind{
		Group:   gvk.Group,
		Version: gvk.Version,
		Kind:    strings.TrimSuffix(gvk.Kind, "List"),
	}

	_, gvkClusterScoped := sc.clusterCache.GvkInformers[gvkForListItems]

	allItems, err := apimeta.ExtractList(list)
	if err != nil {
		return err
	}

	if !isNamespaced || gvkClusterScoped || len(sc.nsCache.Namespaces) == 0 {
		// Look at the global cache to get the objects with the specified GVK
		objs, err = sc.clusterCache.List(listOpts, gvkForListItems)
		if err != nil {
			return err
		}
	} else {
		objs, err = sc.nsCache.List(listOpts, gvkForListItems)
		if err != nil {
			return err
		}
	}

	allItems = append(allItems, objs...)

	allItems, err = deduplicateList(allItems)
	if err != nil {
		return fmt.Errorf("encountered an error attempting to list: %w", err)
	}

	return apimeta.SetList(list, allItems)
}

// deduplicateList is meant to remove duplicate objects from a list of objects
func deduplicateList(objs []runtime.Object) ([]runtime.Object, error) {
	uidMap := make(map[types.UID]struct{})
	objList := []runtime.Object{}

	for _, obj := range objs {
		// turn runtime.Object to a client.Object so we can get the resource UID
		crObj, ok := obj.(client.Object)
		if !ok {
			return nil, fmt.Errorf("could not convert list item to client.Object")
		}
		// if the UID of the resource is not already in the map then it is a new object
		// and can be added to the list.
		if _, ok := uidMap[crObj.GetUID()]; !ok {
			objList = append(objList, obj)
		}
	}

	return objList, nil
}

// ----------------------

// cache.Informers implementation
// ----------------------

// Open Questions:
// 1. Should we attempt to block informer creation if permissions are not allowed? Essentially do an SSAR to see if list/watch permissions for GVR in namespace
// 2. Have users set a custom WatchErrorHandler? We could create a wrapper around this that a user could use to stop an informer if a permission error occurs. Would require changes to controller-runtime to propagate this option to the SharedIndexInformer that is created

// My thoughts:
// We allow all informer creation, but recommend that users set a custom WatchErrorHandler.
// Having the WatchErrorHandler would allow for silencing errors of an informer not having watch permissions but still keep the informer active.
// The moment the ServiceAccount has permissions the informer would be able to connect and work as expected.

// Update to this: It turns out that if the informer is not able to connect, it blocks. This makes it so that any other CRs don't get reconciled.
// We will have to omit getting particular informers if the ServiceAccount does not have permissions
func (sc *ScopedCache) GetInformer(ctx context.Context, obj client.Object) (cache.Informer, error) {
	// TODO: figure out how to properly implement this with the new layered approach
	return nil, fmt.Errorf("UNIMPLEMENTED")
}

func (sc *ScopedCache) GetInformerForKind(ctx context.Context, gvk schema.GroupVersionKind) (cache.Informer, error) {
	// TODO: figure out how to properly implement this with the new layered approach
	return nil, fmt.Errorf("UNIMPLEMENTED")
}

func (sc *ScopedCache) Start(ctx context.Context) error {
	sc.clusterCache.Start()
	sc.nsCache.Start()

	sc.isStarted = true

	return nil
}

func (sc *ScopedCache) WaitForCacheSync(ctx context.Context) bool {
	// TODO: figure out how to properly implement this with the new layered approach
	return true
}

func (sc *ScopedCache) IndexField(ctx context.Context, obj client.Object, field string, extractValue client.IndexerFunc) error {
	// TODO: figure out how to properly implement this with the new layered approach
	return fmt.Errorf("UNIMPLEMENTED")
}

// ----------------------

// Custom functions for ScopedCache

func (sc *ScopedCache) AddInformer(infOpts InformerOptions) {
	if infOpts.Namespace != "" {
		sc.nsCache.AddInformer(infOpts)
	} else {
		sc.clusterCache.AddInformer(infOpts)
	}
}

func (sc *ScopedCache) RemoveInformer(infOpts InformerOptions) {
	if infOpts.Namespace != "" {
		sc.nsCache.RemoveInformer(infOpts)
	} else {
		sc.clusterCache.RemoveInformer(infOpts)
	}
}

// --------------------------------
