package cache

import (
	"context"
	"fmt"

	. "github.com/onsi/ginkgo" //nolint:golint
	. "github.com/onsi/gomega" //nolint:golint
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var _ = Describe("Running ScopedCache tests", func() {
	Context("When using ScopedCacheBuilder() to create a new ScopedCache", func() {
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

	Context("When using Get() to get a specific resource from the ScopedCache", func() {
		var (
			scopedCache *ScopedCache
			ctx         context.Context
			ctxCancel   context.CancelFunc
			c           client.Client
			testPod     *corev1.Pod
		)

		BeforeEach(func() {
			// build and start the cache
			cache, err := ScopedCacheBuilder()(testenv.Config, cache.Options{})
			Expect(err).Should(BeNil())
			scopedCache = cache.(*ScopedCache)
			ctx, ctxCancel = context.WithCancel(context.TODO())
			go scopedCache.Start(ctx)

			// create a test pod
			testPod = &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test",
					Namespace: "default",
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						corev1.Container{
							Image: "nginx",
							Name:  "test-pod",
						},
					},
				},
			}

			c, err = client.New(testenv.Config, client.Options{})
			Expect(err).Should(BeNil())
			err = c.Create(context.TODO(), testPod)
			Expect(err).Should(BeNil())
		})

		AfterEach(func() {
			err := c.Delete(context.TODO(), testPod, &client.DeleteOptions{})
			Expect(err).Should(BeNil())
			ctxCancel()
		})

		It("Should Get() from the cluster cache if no NamespaceResourceCaches have been created", func() {
			obj := &corev1.Pod{}
			err := scopedCache.Get(context.TODO(), client.ObjectKey{Name: "test", Namespace: "default"}, obj)
			Expect(err).Should(BeNil())

			Expect(obj.Spec.Containers[0].Image).Should(Equal("nginx"))
			Expect(obj.Spec.Containers[0].Name).Should(Equal("test-pod"))

			Expect(scopedCache.gvkClusterScoped).Should(HaveKey(corev1.SchemeGroupVersion.WithKind("Pod")))
		})

		It("Should Get() from the NamespaceResourceCaches if at least one has been created AND the GVK/API for an object is not mapped to the cluster cache", func() {
			// Create a deployment to use as the resource for a cache
			resource := &appsv1.Deployment{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-resource",
					Namespace: "default",
					UID:       types.UID("test-resource"),
				},
			}

			// Create the cache
			resCache, err := cache.New(testenv.Config, cache.Options{Namespace: "default"})
			Expect(err).Should(BeNil())

			// Add the cache to the ScopedCache for the deployment resource
			scopedCache.nsCache[resource.GetNamespace()] = make(ResourceCache)
			scopedCache.nsCache[resource.GetNamespace()][resource.GetUID()] = resCache

			// Start the cache so it can be used
			go resCache.Start(ctx)

			// Get the test pod from the NamespacedResourceCache we just created
			obj := &corev1.Pod{}
			err = scopedCache.Get(context.TODO(), client.ObjectKeyFromObject(testPod), obj)
			Expect(err).Should(BeNil())

			Expect(obj.Spec.Containers[0].Image).Should(Equal("nginx"))
			Expect(obj.Spec.Containers[0].Name).Should(Equal("test-pod"))

			Expect(scopedCache.gvkClusterScoped).ShouldNot(HaveKey(corev1.SchemeGroupVersion.WithKind("Pod")))
		})

		It("Should continue if there is an error returned by the Get() function when looping through the ResourceCaches for a given namespace", func() {
			// Create a deployment to use as the resource for a cache
			resource := &appsv1.Deployment{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-resource",
					Namespace: "default",
					UID:       types.UID("test-resource"),
				},
			}

			// Create the cache
			cacheOne, err := cache.New(testenv.Config, cache.Options{Namespace: "not"})
			Expect(err).Should(BeNil())

			// Add the cache to the ScopedCache for the deployment resource
			scopedCache.nsCache[resource.GetNamespace()] = make(ResourceCache)
			scopedCache.nsCache[resource.GetNamespace()][resource.GetUID()] = cacheOne

			// Start the cache so it can be used
			go cacheOne.Start(ctx)

			// Create another cache but don't start it so it causes an error
			// Create a deployment to use as the resource for a cache
			resourceTwo := &appsv1.Deployment{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-resource",
					Namespace: "default",
					UID:       types.UID("test-resource-two"),
				},
			}

			cacheTwo, err := cache.New(testenv.Config, cache.Options{Namespace: "default"})
			Expect(err).Should(BeNil())
			scopedCache.nsCache[resourceTwo.GetNamespace()][resourceTwo.GetUID()] = cacheTwo
			go cacheTwo.Start(ctx)

			// Get the test pod from the NamespacedResourceCache we just created
			obj := &corev1.Pod{}
			err = scopedCache.Get(context.TODO(), client.ObjectKeyFromObject(testPod), obj)
			Expect(err).Should(BeNil())

			Expect(obj.Spec.Containers[0].Image).Should(Equal("nginx"))
			Expect(obj.Spec.Containers[0].Name).Should(Equal("test-pod"))

			Expect(scopedCache.gvkClusterScoped).ShouldNot(HaveKey(corev1.SchemeGroupVersion.WithKind("Pod")))
		})

		It("Should return a NotFound error if the object could not be found in any NamespacedResourceCaches", func() {
			// Create a deployment to use as the resource for a cache
			resource := &appsv1.Deployment{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-resource",
					Namespace: "default",
					UID:       types.UID("test-resource"),
				},
			}

			// Create the cache
			resCache, err := cache.New(testenv.Config, cache.Options{Namespace: "default"})
			Expect(err).Should(BeNil())

			// Add the cache to the ScopedCache for the deployment resource
			scopedCache.nsCache[resource.GetNamespace()] = make(ResourceCache)
			scopedCache.nsCache[resource.GetNamespace()][resource.GetUID()] = resCache

			// Get the test pod from the NamespacedResourceCache we just created
			obj := &corev1.Pod{}
			err = scopedCache.Get(context.TODO(), client.ObjectKeyFromObject(testPod), obj)
			Expect(err).ShouldNot(BeNil())
			Expect(errors.IsNotFound(err)).Should(BeTrue())
		})

		It("Should return an error if the Namespace in the provided client.ObjectKey does not exist in the NamespacedResourceCache mapping", func() {
			// Create a deployment to use as the resource for a cache
			resource := &appsv1.Deployment{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-resource",
					Namespace: "default",
					UID:       types.UID("test-resource"),
				},
			}

			// Create the cache
			resCache, err := cache.New(testenv.Config, cache.Options{Namespace: "default"})
			Expect(err).Should(BeNil())

			// Add the cache to the ScopedCache for the deployment resource
			scopedCache.nsCache[resource.GetNamespace()] = make(ResourceCache)
			scopedCache.nsCache[resource.GetNamespace()][resource.GetUID()] = resCache

			// Start the cache so it can be used
			go resCache.Start(ctx)

			// Get the test pod from the NamespacedResourceCache we just created
			obj := &corev1.Pod{}
			key := client.ObjectKey{Name: "test", Namespace: "na"}
			err = scopedCache.Get(context.TODO(), key, obj)
			Expect(err).ShouldNot(BeNil())
			Expect(err.Error()).Should(Equal(fmt.Sprintf("unable to get: %v because of unknown namespace for the cache", key)))
		})
	})

	Context("When using List() to get a list of resources from the ScopedCache", func() {
		var (
			scopedCache *ScopedCache
			ctx         context.Context
			ctxCancel   context.CancelFunc
			c           client.Client
			testPod     *corev1.Pod
		)

		BeforeEach(func() {
			// build and start the cache
			cache, err := ScopedCacheBuilder()(testenv.Config, cache.Options{})
			Expect(err).Should(BeNil())
			scopedCache = cache.(*ScopedCache)
			ctx, ctxCancel = context.WithCancel(context.TODO())
			go scopedCache.Start(ctx)

			// create a test pod
			testPod = &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test",
					Namespace: "default",
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						corev1.Container{
							Image: "nginx",
							Name:  "test-pod",
						},
					},
				},
			}

			c, err = client.New(testenv.Config, client.Options{})
			Expect(err).Should(BeNil())
			err = c.Create(context.TODO(), testPod)
			Expect(err).Should(BeNil())
		})

		AfterEach(func() {
			err := c.Delete(context.TODO(), testPod, &client.DeleteOptions{})
			Expect(err).Should(BeNil())
			ctxCancel()
		})

		It("Should List() from the cluster cache if no NamespaceResourceCaches have been created", func() {
			obj := &corev1.PodList{}
			err := scopedCache.List(context.TODO(), obj, &client.ListOptions{})
			Expect(err).Should(BeNil())

			Expect(obj.Items[0].Spec.Containers[0].Image).Should(Equal("nginx"))
			Expect(obj.Items[0].Spec.Containers[0].Name).Should(Equal("test-pod"))

			Expect(scopedCache.gvkClusterScoped).Should(HaveKey(corev1.SchemeGroupVersion.WithKind("Pod")))
		})

		It("Should List() from the NamespaceResourceCaches if at least one has been created AND the GVK/API for an object is not mapped to the cluster cache", func() {
			// Create a deployment to use as the resource for a cache
			resource := &appsv1.Deployment{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-resource",
					Namespace: "default",
					UID:       types.UID("test-resource"),
				},
			}

			// Create the cache
			resCache, err := cache.New(testenv.Config, cache.Options{Namespace: "default"})
			Expect(err).Should(BeNil())

			// Add the cache to the ScopedCache for the deployment resource
			scopedCache.nsCache[resource.GetNamespace()] = make(ResourceCache)
			scopedCache.nsCache[resource.GetNamespace()][resource.GetUID()] = resCache

			// Start the cache so it can be used
			go resCache.Start(ctx)

			// Get the test pod from the NamespacedResourceCache we just created
			obj := &corev1.PodList{}
			err = scopedCache.List(context.TODO(), obj, &client.ListOptions{})
			Expect(err).Should(BeNil())

			Expect(obj.Items[0].Spec.Containers[0].Image).Should(Equal("nginx"))
			Expect(obj.Items[0].Spec.Containers[0].Name).Should(Equal("test-pod"))

			Expect(scopedCache.gvkClusterScoped).ShouldNot(HaveKey(corev1.SchemeGroupVersion.WithKind("Pod")))
		})

		It("Should List() only from ResourceCaches mapped to the namespace `default`", func() {
			// Create a deployment to use as the resource for a cache
			resource := &appsv1.Deployment{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-resource",
					Namespace: "default",
					UID:       types.UID("test-resource"),
				},
			}

			// Create the cache
			resCache, err := cache.New(testenv.Config, cache.Options{Namespace: "default"})
			Expect(err).Should(BeNil())

			// Add the cache to the ScopedCache for the deployment resource
			scopedCache.nsCache[resource.GetNamespace()] = make(ResourceCache)
			scopedCache.nsCache[resource.GetNamespace()][resource.GetUID()] = resCache

			// Start the cache so it can be used
			go resCache.Start(ctx)

			// create another test Pod and resource cache in a new namespace
			ns := &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: "another",
				},
			}
			err = c.Create(context.TODO(), ns, &client.CreateOptions{})
			Expect(err).Should(BeNil())
			// create a test pod
			anotherTestPod := &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test",
					Namespace: "another",
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						corev1.Container{
							Image: "nginx",
							Name:  "test-pod-another",
						},
					},
				},
			}
			err = c.Create(context.TODO(), anotherTestPod, &client.CreateOptions{})
			Expect(err).Should(BeNil())

			// Create a deployment to use as the resource for a cache
			resourceTwo := &appsv1.Deployment{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-resource",
					Namespace: "another",
					UID:       types.UID("test-resource-another"),
				},
			}

			// Create the cache
			resCacheTwo, err := cache.New(testenv.Config, cache.Options{Namespace: "another"})
			Expect(err).Should(BeNil())

			// Add the cache to the ScopedCache for the deployment resource
			scopedCache.nsCache[resourceTwo.GetNamespace()] = make(ResourceCache)
			scopedCache.nsCache[resourceTwo.GetNamespace()][resource.GetUID()] = resCacheTwo

			// Start the cache so it can be used
			go resCacheTwo.Start(ctx)

			// Get the test pod from the NamespacedResourceCache we just created
			obj := &corev1.PodList{}
			err = scopedCache.List(context.TODO(), obj, &client.ListOptions{Namespace: "default"})
			Expect(err).Should(BeNil())

			Expect(obj.Items[0].Spec.Containers[0].Image).Should(Equal("nginx"))
			Expect(obj.Items[0].Spec.Containers[0].Name).Should(Equal("test-pod"))

			Expect(scopedCache.gvkClusterScoped).ShouldNot(HaveKey(corev1.SchemeGroupVersion.WithKind("Pod")))

			// remove the created namespace and test pod

			err = c.Delete(context.TODO(), ns, &client.DeleteOptions{})
			Expect(err).Should(BeNil())
			err = c.Delete(context.TODO(), anotherTestPod, &client.DeleteOptions{})
			Expect(err).Should(BeNil())
		})

		It("Should limit the returned list size if the client.ListOptions.Limit is set", func() {
			// Create a deployment to use as the resource for a cache
			resource := &appsv1.Deployment{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-resource",
					Namespace: "default",
					UID:       types.UID("test-resource"),
				},
			}

			// Create the cache
			resCache, err := cache.New(testenv.Config, cache.Options{Namespace: "default"})
			Expect(err).Should(BeNil())

			// Add the cache to the ScopedCache for the deployment resource
			scopedCache.nsCache[resource.GetNamespace()] = make(ResourceCache)
			scopedCache.nsCache[resource.GetNamespace()][resource.GetUID()] = resCache

			// Start the cache so it can be used
			go resCache.Start(ctx)

			// create another test Pod and resource cache in a new namespace
			ns := &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: "someother",
				},
			}
			err = c.Create(context.TODO(), ns, &client.CreateOptions{})
			Expect(err).Should(BeNil())
			// create a test pod
			anotherTestPod := &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test",
					Namespace: "someother",
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						corev1.Container{
							Image: "nginx",
							Name:  "test-pod-another",
						},
					},
				},
			}
			err = c.Create(context.TODO(), anotherTestPod, &client.CreateOptions{})
			Expect(err).Should(BeNil())

			// Create a deployment to use as the resource for a cache
			resourceTwo := &appsv1.Deployment{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-resource",
					Namespace: "someother",
					UID:       types.UID("test-resource-someother"),
				},
			}

			// Create the cache
			resCacheTwo, err := cache.New(testenv.Config, cache.Options{Namespace: "someother"})
			Expect(err).Should(BeNil())

			// Add the cache to the ScopedCache for the deployment resource
			scopedCache.nsCache[resourceTwo.GetNamespace()] = make(ResourceCache)
			scopedCache.nsCache[resourceTwo.GetNamespace()][resource.GetUID()] = resCacheTwo

			// Start the cache so it can be used
			go resCacheTwo.Start(ctx)

			// Get the test pod from the NamespacedResourceCache we just created
			obj := &corev1.PodList{}
			err = scopedCache.List(context.TODO(), obj, &client.ListOptions{Limit: 1})
			Expect(err).Should(BeNil())

			Expect(len(obj.Items)).Should(Equal(1))
			Expect(obj.Items[0].Spec.Containers[0].Image).Should(Equal("nginx"))
			Expect(obj.Items[0].Spec.Containers[0].Name).Should(Equal("test-pod"))

			Expect(scopedCache.gvkClusterScoped).ShouldNot(HaveKey(corev1.SchemeGroupVersion.WithKind("Pod")))

		})
	})

	// TODO (everettraven): Write more tests for the ScopedCache here
})
