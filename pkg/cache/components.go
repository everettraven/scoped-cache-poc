package cache

import (
	"context"
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/informers"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// ---------------------
// Cache Components
// ---------------------

type ScopeInformer struct {
	// Since we can't index our mapping on an informer
	// we will need to store the informer here
	Informer informers.GenericInformer

	Cancel context.CancelFunc

	// This could be a different mapping or
	// type. I haven't quite thought about
	// The *best* way for tracking the dependents
	Dependents map[types.UID]metav1.Object
}

type Informers map[string]ScopeInformer

type GvkToInformers map[schema.GroupVersionKind]Informers

// -------------------

type InformerOptions struct {
	Namespace string
	Gvk       schema.GroupVersionKind
	Key       string
	Informer  informers.GenericInformer
	Dependent metav1.Object
}

// -------------------
// NamespaceScopedCache
// -------------------

type NamespaceScopedCache struct {
	Namespaces map[string]GvkToInformers
	Scheme     *runtime.Scheme
	started    bool
}

func NewNamespaceScopedCache() *NamespaceScopedCache {
	return &NamespaceScopedCache{
		Namespaces: make(map[string]GvkToInformers),
		Scheme:     runtime.NewScheme(),
		started:    false,
	}
}

func (nsc *NamespaceScopedCache) Get(key types.NamespacedName, gvk schema.GroupVersionKind) (runtime.Object, error) {
	found := false
	var obj runtime.Object
	var err error
	for _, si := range nsc.Namespaces[key.Namespace][gvk] {
		obj, err = si.Informer.Lister().Get(key.String())
		if err != nil {
			// ignore error because it *could* be found by another informer
			continue
		}
		found = true
	}

	if found {
		return obj, nil
	} else {
		return nil, fmt.Errorf("could not find the given resource")
	}
}

func (nsc *NamespaceScopedCache) List(listOpts client.ListOptions, gvk schema.GroupVersionKind) ([]runtime.Object, error) {
	retList := []runtime.Object{}
	if listOpts.Namespace != "" {
		for _, si := range nsc.Namespaces[listOpts.Namespace][gvk] {
			list, err := si.Informer.Lister().List(listOpts.LabelSelector)
			if err != nil {
				// we should be able to list from all informers so in this case return the error
				return nil, err
			}

			// for each of the informers we need to append the list of objects to the return list
			retList = append(retList, list...)
		}
	} else {
		// if no namespace given, we want to list from ALL namespaces that we know of
		for ns := range nsc.Namespaces {
			for _, si := range nsc.Namespaces[ns][gvk] {
				list, err := si.Informer.Lister().List(listOpts.LabelSelector)
				if err != nil {
					// we should be able to list from all informers so in this case return the error
					return nil, err
				}

				// for each of the informers we need to append the list of objects to the return list
				retList = append(retList, list...)
			}
		}
	}

	deduplicatedList, err := deduplicateList(retList)
	if err != nil {
		return nil, err
	}

	return deduplicatedList, nil
}

func (nsc *NamespaceScopedCache) AddInformer(infOpts InformerOptions) {
	if _, ok := nsc.Namespaces[infOpts.Namespace]; !ok {
		nsc.Namespaces[infOpts.Namespace] = make(GvkToInformers)
	}

	if _, ok := nsc.Namespaces[infOpts.Namespace][infOpts.Gvk]; !ok {
		nsc.Namespaces[infOpts.Namespace][infOpts.Gvk] = make(Informers)
	}

	si := ScopeInformer{Informer: infOpts.Informer, Dependents: make(map[types.UID]metav1.Object)}
	if nsc.IsStarted() {
		ctx, cancel := context.WithCancel(context.Background())
		si.Cancel = cancel
		go si.Informer.Informer().Run(ctx.Done())
	}

	if _, ok := nsc.Namespaces[infOpts.Namespace][infOpts.Gvk][infOpts.Key]; !ok {
		si.Dependents[infOpts.Dependent.GetUID()] = infOpts.Dependent
		nsc.Namespaces[infOpts.Namespace][infOpts.Gvk][infOpts.Key] = si
	} else {
		si = nsc.Namespaces[infOpts.Namespace][infOpts.Gvk][infOpts.Key]
		if _, ok := si.Dependents[infOpts.Dependent.GetUID()]; !ok {
			si.Dependents[infOpts.Dependent.GetUID()] = infOpts.Dependent
			nsc.Namespaces[infOpts.Namespace][infOpts.Gvk][infOpts.Key] = si
		}
	}

}

func (nsc *NamespaceScopedCache) RemoveInformer(infOpts InformerOptions) {
	// delete the dependent resource from the informer
	delete(nsc.Namespaces[infOpts.Namespace][infOpts.Gvk][infOpts.Key].Dependents, infOpts.Dependent.GetUID())

	if len(nsc.Namespaces[infOpts.Namespace][infOpts.Gvk][infOpts.Key].Dependents) == 0 {
		delete(nsc.Namespaces[infOpts.Namespace][infOpts.Gvk], infOpts.Key)
	}
}

func (nsc *NamespaceScopedCache) Start() {
	for nsKey := range nsc.Namespaces {
		for gvkKey := range nsc.Namespaces[nsKey] {
			for infKey, si := range nsc.Namespaces[nsKey][gvkKey] {
				ctx, cancel := context.WithCancel(context.Background())
				go si.Informer.Informer().Run(ctx.Done())
				si.Cancel = cancel
				nsc.Namespaces[nsKey][gvkKey][infKey] = si
			}
		}
	}

	nsc.started = true
}

func (nsc *NamespaceScopedCache) IsStarted() bool {
	return nsc.started
}

// -------------------

// -------------------
// ClusterScopedCache
// -------------------

type ClusterScopedCache struct {
	GvkInformers GvkToInformers
	Scheme       *runtime.Scheme
	started      bool
}

func NewClusterScopedCache() *ClusterScopedCache {
	return &ClusterScopedCache{
		GvkInformers: make(GvkToInformers),
		Scheme:       runtime.NewScheme(),
		started:      false,
	}
}

func (csc *ClusterScopedCache) Get(key types.NamespacedName, gvk schema.GroupVersionKind) (runtime.Object, error) {
	found := false
	var obj runtime.Object
	var err error
	for _, si := range csc.GvkInformers[gvk] {
		obj, err = si.Informer.Lister().Get(key.String())
		if err != nil {
			// ignore error because it *could* be found by another informer
			continue
		}
		found = true
	}

	if found {
		return obj, nil
	} else {
		return nil, fmt.Errorf("could not find the given resource")
	}
}

func (csc *ClusterScopedCache) List(listOpts client.ListOptions, gvk schema.GroupVersionKind) ([]runtime.Object, error) {
	retList := []runtime.Object{}

	// For the time being don't worry about handling listing in
	// a particular namespace for cluster scoped caching

	for _, si := range csc.GvkInformers[gvk] {
		list, err := si.Informer.Lister().List(listOpts.LabelSelector)
		if err != nil {
			// we should be able to list from all informers so in this case return the error
			return nil, err
		}

		// for each of the informers we need to append the list of objects to the return list
		retList = append(retList, list...)
	}

	deduplicatedList, err := deduplicateList(retList)
	if err != nil {
		return nil, err
	}

	return deduplicatedList, nil
}

func (csc *ClusterScopedCache) AddInformer(infOpts InformerOptions) {
	if _, ok := csc.GvkInformers[infOpts.Gvk]; !ok {
		csc.GvkInformers[infOpts.Gvk] = make(Informers)
	}

	si := ScopeInformer{Informer: infOpts.Informer, Dependents: make(map[types.UID]metav1.Object)}
	if csc.IsStarted() {
		ctx, cancel := context.WithCancel(context.Background())
		si.Cancel = cancel
		go si.Informer.Informer().Run(ctx.Done())
	}

	if _, ok := csc.GvkInformers[infOpts.Gvk][infOpts.Key]; !ok {
		si.Dependents[infOpts.Dependent.GetUID()] = infOpts.Dependent
		csc.GvkInformers[infOpts.Gvk][infOpts.Key] = si
	} else {
		si = csc.GvkInformers[infOpts.Gvk][infOpts.Key]
		if _, ok := si.Dependents[infOpts.Dependent.GetUID()]; !ok {
			si.Dependents[infOpts.Dependent.GetUID()] = infOpts.Dependent
			csc.GvkInformers[infOpts.Gvk][infOpts.Key] = si
		}
	}
}

func (csc *ClusterScopedCache) RemoveInformer(infOpts InformerOptions) {
	// delete the dependent resource from the informer
	delete(csc.GvkInformers[infOpts.Gvk][infOpts.Key].Dependents, infOpts.Dependent.GetUID())

	if len(csc.GvkInformers[infOpts.Gvk][infOpts.Key].Dependents) == 0 {
		delete(csc.GvkInformers[infOpts.Gvk], infOpts.Key)
	}
}

func (csc *ClusterScopedCache) Start() {
	for gvkKey := range csc.GvkInformers {
		for infKey, si := range csc.GvkInformers[gvkKey] {
			ctx, cancel := context.WithCancel(context.Background())
			go si.Informer.Informer().Run(ctx.Done())
			si.Cancel = cancel
			csc.GvkInformers[gvkKey][infKey] = si
		}
	}

	csc.started = true
}

func (csc *ClusterScopedCache) IsStarted() bool {
	return csc.started
}

// -------------------
