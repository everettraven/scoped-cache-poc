package cache

import (
	"fmt"
	"time"

	. "github.com/onsi/ginkgo" //nolint:golint
	. "github.com/onsi/gomega" //nolint:golint
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/cache"
	toolscache "k8s.io/client-go/tools/cache"
)

// TODO (everettraven): Write tests for the ScopedInformer

var _ = Describe("Testing ScopedInformer", func() {
	var (
		nsInformers    NamespacedResourceInformer
		scopedInformer *ScopedInformer
	)

	BeforeEach(func() {
		nsInformers = make(NamespacedResourceInformer)
		nsInformers["default"] = make(ResourceInformer)

		// create a couple fake informers for the "default" namespace
		nsInformers["default"][types.UID("informer-one")] = newFakeInformer()
		nsInformers["default"][types.UID("informer-two")] = newFakeInformer()

		scopedInformer = &ScopedInformer{nsInformers: nsInformers}
	})

	Context("When adding event handlers to the ScopedInformer", func() {
		It("Should add the event handler to all informers in the nsInformers field", func() {
			handler := cache.ResourceEventHandlerFuncs{}
			scopedInformer.AddEventHandler(handler)

			for _, inf := range nsInformers["default"] {
				fi, ok := inf.(*fakeInformer)
				Expect(ok).Should(BeTrue())
				Expect(fi.EventHandlers[0]).Should(Equal(handler))
			}
		})
	})

	Context("When adding event handlers with a resync period to the ScopedInformer", func() {
		It("Should add the event handler with resync period to all informers in the nsInformers field", func() {
			handler := cache.ResourceEventHandlerFuncs{}
			resync := 10 * time.Second
			scopedInformer.AddEventHandlerWithResyncPeriod(handler, resync)

			for _, inf := range nsInformers["default"] {
				fi, ok := inf.(*fakeInformer)
				Expect(ok).Should(BeTrue())
				Expect(fi.EventHandlersWithResyncPeriod).Should(HaveKeyWithValue(resync, []cache.ResourceEventHandler{handler}))
			}
		})
	})

	Context("When adding indexers to the ScopedInformer", func() {
		It("Should add the indexer to all informers in the nsInformers field", func() {
			indexers := make(cache.Indexers)
			err := scopedInformer.AddIndexers(indexers)
			Expect(err).Should(BeNil())

			for _, inf := range nsInformers["default"] {
				fi, ok := inf.(*fakeInformer)
				Expect(ok).Should(BeTrue())
				Expect(fi.Indexers[0]).Should(Equal(indexers))
			}
		})

		It("Should return an error if it cannot add an indexer to an informer", func() {
			// add a new informer that will error on it's AddIndexers function
			aiErrInf := newFakeInformer()
			aiErrInf.ErrorInFunc = "AddIndexers"
			scopedInformer.nsInformers["default"]["addindexers-error"] = aiErrInf

			indexers := make(cache.Indexers)
			err := scopedInformer.AddIndexers(indexers)
			Expect(err).ShouldNot(BeNil())
			Expect(err.Error()).Should(Equal("erroring at AddIndexers"))
		})
	})

	Context("When checking if ScopedInformer has synced", func() {
		It("Should return true if all informers in nsInformers field have synced", func() {
			scopedInformer.nsInformers["default"]["informer-one"].(*fakeInformer).Synced = true
			scopedInformer.nsInformers["default"]["informer-two"].(*fakeInformer).Synced = true
			Expect(scopedInformer.HasSynced()).Should(BeTrue())
		})

		It("Should return false if any informers in nsInformers field have not synced", func() {
			scopedInformer.nsInformers["default"]["informer-one"].(*fakeInformer).Synced = true
			Expect(scopedInformer.HasSynced()).Should(BeFalse())
		})
	})

	Context("When setting the WatchErrorHandler on the ScopedInformer", func() {
		It("Should set the WatchErrorHandler for all informers in the nsInformers field", func() {
			handler := func(r *cache.Reflector, err error) {}
			err := scopedInformer.SetWatchErrorHandler(handler)
			Expect(err).Should(BeNil())

			for _, inf := range nsInformers["default"] {
				fi, ok := inf.(*fakeInformer)
				Expect(ok).Should(BeTrue())
				Expect(fi.WatchErrorHandler).ShouldNot(BeNil())
			}
		})

		It("Should continue if it gets an error when setting the WatchErrorHandler on an informer", func() {
			scopedInformer.nsInformers["default"]["informer-one"].(*fakeInformer).ErrorInFunc = "SetWatchErrorHandler"
			handler := func(r *cache.Reflector, err error) {}
			err := scopedInformer.SetWatchErrorHandler(handler)
			Expect(err).Should(BeNil())

			for k, inf := range nsInformers["default"] {
				fi, ok := inf.(*fakeInformer)
				Expect(ok).Should(BeTrue())
				if k == "informer-one" {
					Expect(fi.WatchErrorHandler).Should(BeNil())
				} else {
					Expect(fi.WatchErrorHandler).ShouldNot(BeNil())
				}
			}
		})
	})
})

type fakeInformer struct {
	cache.SharedInformer
	EventHandlers                 []cache.ResourceEventHandler
	EventHandlersWithResyncPeriod map[time.Duration][]cache.ResourceEventHandler
	Indexers                      []cache.Indexers
	Synced                        bool
	WatchErrorHandler             cache.WatchErrorHandler
	ErrorInFunc                   string
}

func newFakeInformer() *fakeInformer {
	return &fakeInformer{
		EventHandlers:                 []cache.ResourceEventHandler{},
		EventHandlersWithResyncPeriod: make(map[time.Duration][]cache.ResourceEventHandler),
		Indexers:                      []cache.Indexers{},
	}
}

func (i *fakeInformer) AddEventHandler(handler cache.ResourceEventHandler) {
	i.EventHandlers = append(i.EventHandlers, handler)
}

func (i *fakeInformer) AddEventHandlerWithResyncPeriod(handler toolscache.ResourceEventHandler, resyncPeriod time.Duration) {
	i.EventHandlersWithResyncPeriod[resyncPeriod] = append(i.EventHandlersWithResyncPeriod[resyncPeriod], handler)
}

func (i *fakeInformer) AddIndexers(indexers toolscache.Indexers) error {
	if i.ErrorInFunc == "AddIndexers" {
		return fmt.Errorf("erroring at AddIndexers")
	}

	i.Indexers = append(i.Indexers, indexers)
	return nil
}

func (i *fakeInformer) HasSynced() bool {
	return i.Synced
}

func (i *fakeInformer) SetWatchErrorHandler(handler toolscache.WatchErrorHandler) error {
	if i.ErrorInFunc == "SetWatchErrorHandler" {
		return fmt.Errorf("erroring at SetWatchErrorHandler")
	}

	i.WatchErrorHandler = handler
	return nil
}
