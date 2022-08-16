package cache

import (
	"time"

	"k8s.io/apimachinery/pkg/types"
	toolscache "k8s.io/client-go/tools/cache"
	"sigs.k8s.io/controller-runtime/pkg/cache"
)

type ResourceInformer map[types.UID]cache.Informer

type NamespacedResourceInformer map[string]ResourceInformer

type ScopedInformer struct {
	nsInformers NamespacedResourceInformer
}

var _ cache.Informer = &ScopedInformer{}

// AddEventHandler adds the handler to each informer.
func (i *ScopedInformer) AddEventHandler(handler toolscache.ResourceEventHandler) {
	for _, ri := range i.nsInformers {
		for _, informer := range ri {
			informer.AddEventHandler(handler)
		}
	}
}

// AddEventHandlerWithResyncPeriod adds the handler with a resync period to each informer.
func (i *ScopedInformer) AddEventHandlerWithResyncPeriod(handler toolscache.ResourceEventHandler, resyncPeriod time.Duration) {
	for _, ri := range i.nsInformers {
		for _, informer := range ri {
			informer.AddEventHandlerWithResyncPeriod(handler, resyncPeriod)
		}
	}
}

// AddIndexers adds the indexer for each informer.
func (i *ScopedInformer) AddIndexers(indexers toolscache.Indexers) error {
	for _, ri := range i.nsInformers {
		for _, informer := range ri {
			err := informer.AddIndexers(indexers)
			if err != nil {
				return err
			}
		}
	}
	return nil
}

// HasSynced checks if each informer has synced.
func (i *ScopedInformer) HasSynced() bool {
	for _, ri := range i.nsInformers {
		for _, informer := range ri {
			if ok := informer.HasSynced(); !ok {
				return ok
			}
		}
	}
	return true
}
