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

// SetWatchErrorHandler will attempt to set the watch error handler on the informer
func (i *ScopedInformer) SetWatchErrorHandler(handler toolscache.WatchErrorHandler) error {
	for _, ri := range i.nsInformers {
		for _, informer := range ri {
			if si, ok := informer.(toolscache.SharedInformer); ok {
				err := si.SetWatchErrorHandler(handler)
				if err != nil {
					// if there is an error then we will for now assume the
					// WatchErrorHandler has already been set for the informer
					continue
				}
			}
		}
	}

	return nil
}
