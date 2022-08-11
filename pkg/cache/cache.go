package cache

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// Cache Stuff
// --------------------------------

type ResourceCache map[client.Object]cache.Cache

type NamespacedResourceCache map[string]ResourceCache

type ScopedCache struct {
	nsCache      NamespacedResourceCache
	clusterCache cache.Cache // Should this be Resource based as well?
	RESTMapper   apimeta.RESTMapper
	Scheme       *runtime.Scheme
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

		return &ScopedCache{nsCache: caches, Scheme: opts.Scheme, RESTMapper: opts.Mapper, clusterCache: gCache}, nil
	}
}

// client.Reader implementation
// ----------------------

func (sc *ScopedCache) Get(ctx context.Context, key client.ObjectKey, obj client.Object) error {
	isNamespaced, err := IsAPINamespaced(obj, sc.Scheme, sc.RESTMapper)
	if err != nil {
		return err
	}

	if !isNamespaced {
		// Look into the global cache to fetch the object
		return sc.clusterCache.Get(ctx, key, obj)
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
	return fmt.Errorf("unable to get: %v because it wasn't found in any of the caches", key)
}

func (sc *ScopedCache) List(ctx context.Context, list client.ObjectList, opts ...client.ListOption) error {
	listOpts := client.ListOptions{}
	listOpts.ApplyOptions(opts)

	isNamespaced, err := IsAPINamespaced(list, sc.Scheme, sc.RESTMapper)
	if err != nil {
		return err
	}

	if !isNamespaced {
		// Look at the global cache to get the objects with the specified GVK
		return sc.clusterCache.List(ctx, list, opts...)
	}

	// For a specific namespace
	if listOpts.Namespace != corev1.NamespaceAll {
		rCache, ok := sc.nsCache[listOpts.Namespace]
		if !ok {
			return fmt.Errorf("unable to get: %v because of unknown namespace for the cache", listOpts.Namespace)
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
	for _, rCache := range sc.nsCache {
		for _, cache := range rCache {
			listObj := list.DeepCopyObject().(client.ObjectList)
			err = cache.List(ctx, listObj, &listOpts)
			if err != nil {
				return err
			}
			items, err := apimeta.ExtractList(listObj)
			if err != nil {
				return err
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
	}

	listAccessor.SetResourceVersion(resourceVersion)
	return apimeta.SetList(list, allItems)
}

// ----------------------

// cache.Informers implementation
// ----------------------

func (sc *ScopedCache) GetInformer(ctx context.Context, obj client.Object) (cache.Informer, error) {
	informers := make(NamespacedResourceInformer)

	// If the object is clusterscoped, get the informer from clusterCache,
	// if not use the namespaced caches.
	isNamespaced, err := IsAPINamespaced(obj, sc.Scheme, sc.RESTMapper)
	if err != nil {
		return nil, err
	}
	if !isNamespaced {
		clusterCacheInf, err := sc.clusterCache.GetInformer(ctx, obj)
		if err != nil {
			return nil, err
		}
		// Use a nil client.Object for the cluster level informers
		informers[globalCache] = ResourceInformer{
			nil: clusterCacheInf,
		}

		return &ScopedInformer{nsInformers: informers}, nil
	}

	for ns, rCache := range sc.nsCache {
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
	if !isNamespaced {
		clusterCacheInf, err := sc.clusterCache.GetInformerForKind(ctx, gvk)
		if err != nil {
			return nil, err
		}
		// Use a nil client.Object for the cluster level informers
		informers[globalCache] = ResourceInformer{
			nil: clusterCacheInf,
		}

		return &ScopedInformer{nsInformers: informers}, nil
	}

	for ns, rCache := range sc.nsCache {
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
			go func(ns string, r client.Object, c cache.Cache) {
				err := c.Start(ctx)
				if err != nil {
					fmt.Println("scoped cache failed to start informer |", "namespace", ns, "|", "resource", r)
				}
			}(ns, r, cash)
		}
	}

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

	if !isNamespaced {
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

func (sc *ScopedCache) AddResourceCache(resource client.Object, cache cache.Cache) error {
	isNamespaced, err := IsAPINamespaced(resource, sc.Scheme, sc.RESTMapper)
	if err != nil {
		return err
	}

	if !isNamespaced {
		return fmt.Errorf("resource must be namespaced")
	}

	sc.nsCache[resource.GetNamespace()][resource] = cache
	return nil
}

func (sc *ScopedCache) RemoveResourceCache(resource client.Object) error {
	isNamespaced, err := IsAPINamespaced(resource, sc.Scheme, sc.RESTMapper)
	if err != nil {
		return err
	}

	if !isNamespaced {
		return fmt.Errorf("resource must be namespaced")
	}

	delete(sc.nsCache[resource.GetNamespace()], resource)

	return nil
}

// --------------------------------
