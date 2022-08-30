package cache

import (
	. "github.com/onsi/ginkgo" //nolint:golint
	. "github.com/onsi/gomega" //nolint:golint
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/cache"
)

var _ = Describe("Running ScopedCache tests", func() {
	Context("When creating a new ScopedCache", func() {
		It("ScopedCacheBuilder should return NewCacheFunc", func() {
			newCacheFunc := ScopedCacheBuilder()
			Expect(newCacheFunc).ShouldNot(BeNil())
		})

		It("New ScopedCache should be created and defaults populated", func() {
			newCacheFunc := ScopedCacheBuilder()
			cache, err := newCacheFunc(testenv.Config, cache.Options{})
			Expect(err).Should(BeNil())
			Expect(cache).ShouldNot(BeNil())

			// convert to ScopedCache
			scopedCache, ok := cache.(*ScopedCache)
			Expect(ok).Should(BeTrue())
			Expect(scopedCache).ShouldNot(BeNil())
			Expect(scopedCache.cli).ShouldNot(BeNil())
			Expect(scopedCache.Scheme).ShouldNot(BeNil())
			Expect(scopedCache.RESTMapper).ShouldNot(BeNil())
			Expect(scopedCache.clusterCache).ShouldNot(BeNil())
			Expect(scopedCache.isStarted).ShouldNot(BeTrue())
			Expect(scopedCache.gvkClusterScoped).Should(BeEmpty())
			Expect(scopedCache.nsCache).Should(BeEmpty())
		})

		It("Should return an error when config is invalid", func() {
			newCacheFunc := ScopedCacheBuilder()
			cache, err := newCacheFunc(&rest.Config{}, cache.Options{})
			Expect(err).ShouldNot(BeNil())
			Expect(cache).Should(BeNil())
		})
	})

	// Context("When ")

	// TODO (everettraven): Write more tests for the ScopedCache here
})
