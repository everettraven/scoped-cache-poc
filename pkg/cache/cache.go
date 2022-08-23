package cache

import (
	"context"
	"fmt"
	"strings"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
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

// use the resource UID
type ResourceCache map[types.UID]cache.Cache

type NamespacedResourceCache map[string]ResourceCache

type ScopedCache struct {
	nsCache          NamespacedResourceCache
	clusterCache     cache.Cache
	RESTMapper       apimeta.RESTMapper
	Scheme           *runtime.Scheme
	isStarted        bool
	gvkClusterScoped map[schema.GroupVersionKind]struct{}
	cli              dynamic.Interface
}

func ScopedCacheBuilder() cache.NewCacheFunc {
	return func(config *rest.Config, opts cache.Options) (cache.Cache, error) {
		opts, err := defaultOpts(config, opts)
		if err != nil {
			return nil, err
		}

		caches := make(NamespacedResourceCache)

		// create a cache for cluster scoped resources
		gCache, err := cache.New(config, opts)
		if err != nil {
			return nil, fmt.Errorf("error creating global cache: %w", err)
		}

		cli, err := dynamic.NewForConfig(config)
		if err != nil {
			return nil, fmt.Errorf("error creating dynamic client: %w", err)
		}

		return &ScopedCache{nsCache: caches, Scheme: opts.Scheme, RESTMapper: opts.Mapper, clusterCache: gCache, gvkClusterScoped: make(map[schema.GroupVersionKind]struct{}), cli: cli}, nil
	}
}

// client.Reader implementation
// ----------------------

func (sc *ScopedCache) Get(ctx context.Context, key client.ObjectKey, obj client.Object) error {
	isNamespaced, err := IsAPINamespaced(obj, sc.Scheme, sc.RESTMapper)
	if err != nil {
		return err
	}

	// obj could have an empty GVK, lets make sure we have a proper gvk
	gvk, err := apiutil.GVKForObject(obj, sc.Scheme)
	if err != nil {
		return fmt.Errorf("encountered an error getting GVK for object: %w", err)
	}

	mapping, err := sc.RESTMapper.RESTMapping(gvk.GroupKind(), gvk.Version)
	if err != nil {
		return fmt.Errorf("encountered an error getting mapping: %w", err)
	}

	_, gvkClusterScoped := sc.gvkClusterScoped[gvk]

	if !isNamespaced || gvkClusterScoped || len(sc.nsCache) == 0 {
		permitted, err := canClusterListWatchResource(sc.cli, mapping.Resource)
		if err != nil {
			return fmt.Errorf("encountered an error when checking permissions: %w", err)
		}

		if !permitted {
			return errors.NewForbidden(mapping.Resource.GroupResource(), key.Name, fmt.Errorf("not permitted to list/watch the given resource at the cluster level"))
		}

		// if we are here and there doesn't already exist an Informer for the resource,
		// then one will be created at the cluster level so we need to add it to the map
		// of cluster scoped GVKs.
		if !gvkClusterScoped {
			sc.gvkClusterScoped[gvk] = struct{}{}
		}

		// Look into the global cache to fetch the object
		return sc.clusterCache.Get(ctx, key, obj)
	}

	permitted, err := canListWatchResourceForNamespace(sc.cli, mapping.Resource, key.Namespace)
	if err != nil {
		return fmt.Errorf("encountered an error when checking list and watch permissions in namespace `%s`: %w", key.Namespace, err)
	}

	if !permitted {
		return errors.NewForbidden(mapping.Resource.GroupResource(), key.Name, fmt.Errorf("not permitted to list/watch the given resource in the namespace `%s`", key.Namespace))
	}

	rCache, ok := sc.nsCache[key.Namespace]
	if !ok {
		return fmt.Errorf("unable to get: %v because of unknown namespace for the cache", key)
	}

	// loop through all the caches, if there is no error then we found it.
	for _, cache := range rCache {
		err = cache.Get(ctx, key, obj)
		// If there is no error return
		if err != nil {
			continue
		}

		return nil
	}

	//If we have made it here, then we couldn't find it in any of the caches
	return errors.NewNotFound(schema.GroupResource{Group: gvk.Group, Resource: gvk.Kind}, key.Name)
}

func (sc *ScopedCache) List(ctx context.Context, list client.ObjectList, opts ...client.ListOption) error {
	listOpts := client.ListOptions{}
	listOpts.ApplyOptions(opts)

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

	mapping, err := sc.RESTMapper.RESTMapping(gvkForListItems.GroupKind(), gvkForListItems.Version)
	if err != nil {
		return fmt.Errorf("encountered an error getting mapping: %w", err)
	}

	_, gvkClusterScoped := sc.gvkClusterScoped[gvkForListItems]

	if !isNamespaced || gvkClusterScoped || len(sc.nsCache) == 0 {
		permitted, err := canClusterListWatchResource(sc.cli, mapping.Resource)
		if err != nil {
			return fmt.Errorf("encountered an error when checking permissions: %w", err)
		}

		if !permitted {
			return errors.NewForbidden(mapping.Resource.GroupResource(), "cluster-list", fmt.Errorf("not permitted to list/watch the given resource at the cluster level"))
		}

		// if we are here and there doesn't already exist an Informer for the resource,
		// then one will be created at the cluster level so we need to add it to the map
		// of cluster scoped GVKs.
		if !gvkClusterScoped {
			sc.gvkClusterScoped[gvk] = struct{}{}
		}

		// Look at the global cache to get the objects with the specified GVK
		return sc.clusterCache.List(ctx, list, opts...)
	}

	// For a specific namespace
	if listOpts.Namespace != corev1.NamespaceAll {
		// if the namespace doesn't have the permissions, return a new Forbidden error
		permitted, err := canListWatchResourceForNamespace(sc.cli, mapping.Resource, listOpts.Namespace)
		if err != nil {
			return fmt.Errorf("encountered an error when checking list and watch permissions in namespace `%s`: %w", listOpts.Namespace, err)
		}

		if !permitted {
			return errors.NewForbidden(mapping.Resource.GroupResource(), "namespace-list", fmt.Errorf("not permitted to list/watch the given resource in the namespace `%s`", listOpts.Namespace))
		}

		return sc.ListForNamespace(ctx, list, listOpts.Namespace, opts...)
	}

	// For all namespaces
	listAccessor, err := apimeta.ListAccessor(list)
	if err != nil {
		return err
	}

	allItems, err := apimeta.ExtractList(list)
	if err != nil {
		return err
	}

	limitSet := listOpts.Limit > 0

	var resourceVersion string
	for ns := range sc.nsCache {
		// If the namespace doesn't have the permissions, skip it.
		// Essentially only return the values that are allowed to be seen
		permitted, err := canListWatchResourceForNamespace(sc.cli, mapping.Resource, ns)
		if err != nil {
			return fmt.Errorf("encountered an error when checking list and watch permissions in namespace `%s`: %w", ns, err)
		}
		if !permitted {
			continue
		}

		listObj := list.DeepCopyObject().(client.ObjectList)
		err = sc.ListForNamespace(ctx, listObj, ns, opts...)
		if err != nil {
			return fmt.Errorf("encountered an error listing in namespace `%s`: %w", ns, err)
		}

		items, err := apimeta.ExtractList(listObj)
		if err != nil {
			return fmt.Errorf("encountered an error extracting list in namespace `%s`: %w", ns, err)
		}
		accessor, err := apimeta.ListAccessor(listObj)
		if err != nil {
			return fmt.Errorf("object: %T must be a list type", list)
		}
		allItems = append(allItems, items...)
		// The last list call should have the most correct resource version.
		resourceVersion = accessor.GetResourceVersion()
		if limitSet {
			// decrement Limit by the number of items
			// fetched from the current namespace.
			listOpts.Limit -= int64(len(items))
			// if a Limit was set and the number of
			// items read has reached this set limit,
			// then stop reading.
			if listOpts.Limit == 0 {
				break
			}
		}
	}

	listAccessor.SetResourceVersion(resourceVersion)

	// remove duplicates from the list
	allItems, err = deduplicateList(allItems)
	if err != nil {
		return fmt.Errorf("encountered an error attempting to list: %w", err)
	}

	return apimeta.SetList(list, allItems)
}

func (sc *ScopedCache) ListForNamespace(ctx context.Context, list client.ObjectList, namespace string, opts ...client.ListOption) error {
	listOpts := client.ListOptions{}
	listOpts.ApplyOptions(opts)

	rCache, ok := sc.nsCache[namespace]
	if !ok {
		return fmt.Errorf("unable to list: %v because of unknown namespace for the cache", namespace)
	}

	listAccessor, err := apimeta.ListAccessor(list)
	if err != nil {
		return err
	}

	allItems, err := apimeta.ExtractList(list)
	if err != nil {
		return err
	}

	// Loop through all the caches and get a list from each
	var resourceVersion string
	for _, cache := range rCache {
		listObj := list.DeepCopyObject().(client.ObjectList)
		err = cache.List(ctx, listObj, &listOpts)
		if err != nil {
			continue
		}

		// add items to the list
		items, err := apimeta.ExtractList(listObj)
		if err != nil {
			return err
		}

		accessor, err := apimeta.ListAccessor(listObj)
		if err != nil {
			return fmt.Errorf("object: %T must be a list type", list)
		}

		allItems = append(allItems, items...)
		resourceVersion = accessor.GetResourceVersion()
	}

	listAccessor.SetResourceVersion(resourceVersion)
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
	informers := make(NamespacedResourceInformer)

	// If the object is clusterscoped, get the informer from clusterCache,
	// if not use the namespaced caches.
	isNamespaced, err := IsAPINamespaced(obj, sc.Scheme, sc.RESTMapper)
	if err != nil {
		return nil, err
	}

	// obj could have an empty GVK, lets make sure we have a proper gvk
	gvk, err := apiutil.GVKForObject(obj, sc.Scheme)
	if err != nil {
		return nil, fmt.Errorf("encountered an error getting GVK for object: %w", err)
	}

	mapping, err := sc.RESTMapper.RESTMapping(gvk.GroupKind(), gvk.Version)
	if err != nil {
		return nil, fmt.Errorf("encountered an error getting mapping: %w", err)
	}

	// if there are no resource caches created, assume the cluster cache should be used
	if !isNamespaced || len(sc.nsCache) == 0 {
		permitted, err := canClusterListWatchResource(sc.cli, mapping.Resource)
		if err != nil {
			return nil, fmt.Errorf("encountered an error when checking permissions: %w", err)
		}

		// If not permitted just go ahead and return a ScopedInformer with no informers.
		// This makes cluster level GetInformer() requests consistent with namespace level
		// GetInformer() requests by just not getting an informer where it is not permitted.
		if !permitted {
			return &ScopedInformer{nsInformers: informers}, nil
		}

		clusterCacheInf, err := sc.clusterCache.GetInformer(ctx, obj)
		if err != nil {
			return nil, err
		}
		// Use a nil client.Object for the cluster level informers
		informers[globalCache] = ResourceInformer{
			types.UID(globalCache): clusterCacheInf,
		}

		// add gvk to cluster scoped mapping
		sc.gvkClusterScoped[gvk] = struct{}{}

		return &ScopedInformer{nsInformers: informers}, nil
	}

	for ns, rCache := range sc.nsCache {
		// If the namespace doesn't have the permissions, skip it.
		// Only get informers in namespaces where there are permissions
		permitted, err := canListWatchResourceForNamespace(sc.cli, mapping.Resource, ns)
		if err != nil {
			return nil, fmt.Errorf("encountered an error when checking list and watch permissions in namespace `%s`: %w", ns, err)
		}
		if !permitted {
			continue
		}
		informers[ns] = make(ResourceInformer)
		for r, cache := range rCache {
			informer, err := cache.GetInformer(ctx, obj)
			if err != nil {
				return nil, err
			}

			informers[ns][r] = informer
		}
	}

	return &ScopedInformer{nsInformers: informers}, nil
}

func (sc *ScopedCache) GetInformerForKind(ctx context.Context, gvk schema.GroupVersionKind) (cache.Informer, error) {
	informers := make(NamespacedResourceInformer)

	// If the object is clusterscoped, get the informer from clusterCache,
	// if not use the namespaced caches.
	isNamespaced, err := IsAPINamespacedWithGVK(gvk, sc.Scheme, sc.RESTMapper)
	if err != nil {
		return nil, err
	}

	mapping, err := sc.RESTMapper.RESTMapping(gvk.GroupKind(), gvk.Version)
	if err != nil {
		return nil, fmt.Errorf("encountered an error getting mapping: %w", err)
	}

	// if there are no resource caches created, assume the cluster cache should be used
	if !isNamespaced || len(sc.nsCache) == 0 {
		permitted, err := canClusterListWatchResource(sc.cli, mapping.Resource)
		if err != nil {
			return nil, fmt.Errorf("encountered an error when checking permissions: %w", err)
		}

		// If not permitted just go ahead and return a ScopedInformer with no informers.
		// This makes cluster level GetInformer() requests consistent with namespace level
		// GetInformer() requests by just not getting an informer where it is not permitted.
		if !permitted {
			return &ScopedInformer{nsInformers: informers}, nil
		}

		clusterCacheInf, err := sc.clusterCache.GetInformerForKind(ctx, gvk)
		if err != nil {
			return nil, err
		}
		// Use a nil client.Object for the cluster level informers
		informers[globalCache] = ResourceInformer{
			types.UID(globalCache): clusterCacheInf,
		}

		// add gvk to cluster scoped mapping
		sc.gvkClusterScoped[gvk] = struct{}{}

		return &ScopedInformer{nsInformers: informers}, nil
	}

	for ns, rCache := range sc.nsCache {
		// If the namespace doesn't have the permissions, skip it.
		// Only get informers in namespaces where there are permissions
		permitted, err := canListWatchResourceForNamespace(sc.cli, mapping.Resource, ns)
		if err != nil {
			return nil, fmt.Errorf("encountered an error when checking list and watch permissions in namespace `%s`: %w", ns, err)
		}
		if !permitted {
			continue
		}
		informers[ns] = make(ResourceInformer)
		for r, cache := range rCache {
			informer, err := cache.GetInformerForKind(ctx, gvk)
			if err != nil {
				return nil, err
			}

			informers[ns][r] = informer
		}
	}

	return &ScopedInformer{nsInformers: informers}, nil
}

func (sc *ScopedCache) Start(ctx context.Context) error {
	// start global cache
	go func() {
		err := sc.clusterCache.Start(ctx)
		if err != nil {
			fmt.Println("cluster scoped cache failed to start: ", err)
		}
	}()

	// start namespaced caches
	for ns, rCache := range sc.nsCache {
		for r, cash := range rCache {
			go func(ns string, r types.UID, c cache.Cache) {
				err := c.Start(ctx)
				if err != nil {
					fmt.Println("scoped cache failed to start informer |", "namespace", ns, "|", "resource uid", r)
				}
			}(ns, r, cash)
		}
	}

	sc.isStarted = true

	<-ctx.Done()
	return nil
}

func (sc *ScopedCache) WaitForCacheSync(ctx context.Context) bool {
	synced := true
	for _, rCache := range sc.nsCache {
		for _, cache := range rCache {
			if s := cache.WaitForCacheSync(ctx); !s {
				synced = s
			}
		}

	}

	// check if cluster scoped cache has synced
	if !sc.clusterCache.WaitForCacheSync(ctx) {
		synced = false
	}
	return synced
}

func (sc *ScopedCache) IndexField(ctx context.Context, obj client.Object, field string, extractValue client.IndexerFunc) error {
	isNamespaced, err := IsAPINamespaced(obj, sc.Scheme, sc.RESTMapper)
	if err != nil {
		return nil //nolint:nilerr
	}

	_, clusterScoped := sc.gvkClusterScoped[obj.GetObjectKind().GroupVersionKind()]

	if !isNamespaced || clusterScoped {
		return sc.clusterCache.IndexField(ctx, obj, field, extractValue)
	}

	for _, rCache := range sc.nsCache {
		for _, cache := range rCache {
			if err := cache.IndexField(ctx, obj, field, extractValue); err != nil {
				continue
			}

			return nil
		}

	}
	return fmt.Errorf("could not find index field in any of the caches")
}

// ----------------------

// Custom functions for ScopedCache

// AddResourceCache will add a cache for a particular resource
func (sc *ScopedCache) AddResourceCache(resource client.Object, cache cache.Cache) error {
	isNamespaced, err := IsAPINamespaced(resource, sc.Scheme, sc.RESTMapper)
	if err != nil {
		return err
	}

	if !isNamespaced {
		return fmt.Errorf("resource must be namespaced")
	}

	// make sure the namespace exists
	if _, ok := sc.nsCache[resource.GetNamespace()]; !ok {
		sc.nsCache[resource.GetNamespace()] = make(ResourceCache)
	}

	sc.nsCache[resource.GetNamespace()][resource.GetUID()] = cache

	return nil
}

// StartResourceCache used to start a ResourceCache after it has been added
func (sc *ScopedCache) StartResourceCache(ctx context.Context, resource client.Object) {
	// only start if the ScopedCache as a whole has been started
	if sc.isStarted {
		go sc.nsCache[resource.GetNamespace()][resource.GetUID()].Start(ctx)
	}
}

// RemoveResourceCache will remove a cache for a particular resource
func (sc *ScopedCache) RemoveResourceCache(resource client.Object) error {
	isNamespaced, err := IsAPINamespaced(resource, sc.Scheme, sc.RESTMapper)
	if err != nil {
		return err
	}

	if !isNamespaced {
		return fmt.Errorf("resource must be namespaced")
	}

	// make sure the namespace exists
	if _, ok := sc.nsCache[resource.GetNamespace()]; !ok {
		return nil
	}

	delete(sc.nsCache[resource.GetNamespace()], resource.GetUID())

	return nil
}

// GetResourceCache will return the mapping of Namespace -> Resource -> Cache
func (sc *ScopedCache) GetResourceCache() NamespacedResourceCache {
	return sc.nsCache
}

// TODO (everettraven): We could potentially optimize the number of watches being
// created by exposing a function to get informers from a specific ResourceCache.
// Likely future work and not as important in PoC

// --------------------------------
