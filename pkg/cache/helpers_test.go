package cache

import (
	. "github.com/onsi/ginkgo" //nolint:golint
	. "github.com/onsi/gomega" //nolint:golint
	authv1 "k8s.io/api/authorization/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/rest"
	"k8s.io/kubectl/pkg/scheme"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client/apiutil"
)

var _ = Describe("Testing helper functions", func() {
	Context("When using the defaultOpts() helper function", func() {
		It("Should populate a cache.Options object with sensible defaults", func() {
			opts := cache.Options{}
			opts, err := defaultOpts(testenv.Config, opts)
			Expect(err).Should(BeNil())
			Expect(opts.Scheme).Should(Equal(scheme.Scheme))
			Expect(opts.Mapper).ShouldNot(BeNil())
			Expect(*opts.Resync).Should(Equal(defaultResyncTime))
		})

		It("Should return an error if RESTMapper cannot be created", func() {
			_, err := defaultOpts(&rest.Config{}, cache.Options{})
			Expect(err).ShouldNot(BeNil())
			Expect(err.Error()).Should(ContainSubstring("could not create RESTMapper from config"))
		})
	})

	Context("When using the IsAPINamespaceWithGVK() helper function", func() {
		It("Should return true for Pod GroupVersionKind", func() {
			rm, err := apiutil.NewDiscoveryRESTMapper(testenv.Config)
			Expect(err).Should(BeNil())

			namespaced, err := IsAPINamespacedWithGVK(corev1.SchemeGroupVersion.WithKind("Pod"), scheme.Scheme, rm)
			Expect(err).Should(BeNil())
			Expect(namespaced).Should(BeTrue())
		})

		It("Should return false for Namespace GroupVersionKind", func() {
			rm, err := apiutil.NewDiscoveryRESTMapper(testenv.Config)
			Expect(err).Should(BeNil())

			namespaced, err := IsAPINamespacedWithGVK(corev1.SchemeGroupVersion.WithKind("Namespace"), scheme.Scheme, rm)
			Expect(err).Should(BeNil())
			Expect(namespaced).Should(BeFalse())
		})

		It("Should return an error if unable to get a RESTMapping", func() {
			_, err := IsAPINamespacedWithGVK(corev1.SchemeGroupVersion.WithKind("Namespace"), scheme.Scheme, &meta.DefaultRESTMapper{})
			Expect(err).ShouldNot(BeNil())
			Expect(err.Error()).Should(ContainSubstring("failed to get restmapping:"))
		})

		It("Should return an error if RESTMapping.Scope.Name is an empty string", func() {
			addToMapper := func(baseMapper *meta.DefaultRESTMapper) {
				baseMapper.Add(corev1.SchemeGroupVersion.WithKind("Pod"), &emptyRESTScope{})
			}
			rm, err := apiutil.NewDynamicRESTMapper(testenv.Config, apiutil.WithCustomMapper(func() (meta.RESTMapper, error) {
				basemapper := meta.NewDefaultRESTMapper([]schema.GroupVersion{corev1.SchemeGroupVersion})
				addToMapper(basemapper)

				return basemapper, nil
			}))
			Expect(err).Should(BeNil())

			_, err = IsAPINamespacedWithGVK(corev1.SchemeGroupVersion.WithKind("Pod"), scheme.Scheme, rm)
			Expect(err).ShouldNot(BeNil())
			Expect(err.Error()).Should(Equal("scope cannot be identified, empty scope returned"))
		})
	})

	Context("When using the IsAPINamespaced() helper function", func() {
		It("Should return true for a Pod object", func() {
			rm, err := apiutil.NewDiscoveryRESTMapper(testenv.Config)
			Expect(err).Should(BeNil())

			pod := &corev1.Pod{}
			namespaced, err := IsAPINamespaced(pod, scheme.Scheme, rm)
			Expect(err).Should(BeNil())
			Expect(namespaced).Should(BeTrue())
		})

		It("Should return false for a Namespace object", func() {
			rm, err := apiutil.NewDiscoveryRESTMapper(testenv.Config)
			Expect(err).Should(BeNil())

			namespace := &corev1.Namespace{}
			namespaced, err := IsAPINamespaced(namespace, scheme.Scheme, rm)
			Expect(err).Should(BeNil())
			Expect(namespaced).Should(BeFalse())
		})

		It("Should return an error if a GVK cannot be determined from the given runtime.Object", func() {
			rm, err := apiutil.NewDiscoveryRESTMapper(testenv.Config)
			Expect(err).Should(BeNil())

			namespaced, err := IsAPINamespaced(nil, scheme.Scheme, rm)
			Expect(err).ShouldNot(BeNil())
			Expect(namespaced).Should(BeFalse())
		})
	})

	Context("When using the createSSAR() helper function", func() {
		It("Should return the response SSAR", func() {
			ssar := &authv1.SelfSubjectAccessReview{
				Spec: authv1.SelfSubjectAccessReviewSpec{
					ResourceAttributes: &authv1.ResourceAttributes{
						Namespace: "default",
						Verb:      "list",
						Group:     "",
						Version:   "v1",
						Resource:  "pods",
					},
				},
			}

			cli, err := dynamic.NewForConfig(testenv.Config)
			Expect(err).Should(BeNil())

			resp, err := createSSAR(cli, ssar)
			Expect(err).Should(BeNil())
			Expect(resp.Status.Allowed).Should(BeTrue())
		})

		It("Should return an error if it can not convert SSAR to Unstructured", func() {
			cli, err := dynamic.NewForConfig(testenv.Config)
			Expect(err).Should(BeNil())

			_, err = createSSAR(cli, nil)
			Expect(err).ShouldNot(BeNil())
			Expect(err.Error()).Should(ContainSubstring("encountered an error converting to unstructured:"))
		})
	})

	Context("When using the canVerbResource() helper function", func() {
		It("Should return true if can list pods in namespace `default`", func() {
			cli, err := dynamic.NewForConfig(testenv.Config)
			Expect(err).Should(BeNil())

			canVerb, err := canVerbResource(cli, corev1.SchemeGroupVersion.WithResource("pods"), "list", "default")
			Expect(err).Should(BeNil())
			Expect(canVerb).Should(BeTrue())
		})

		It("Should return false if cannot list pods in namespace `default`", func() {
			cli := fake.NewSimpleDynamicClient(scheme.Scheme)

			canVerb, err := canVerbResource(cli, corev1.SchemeGroupVersion.WithResource("pods"), "list", "default")
			Expect(err).Should(BeNil())
			Expect(canVerb).Should(BeFalse())
		})

		It("Should return an error if it cannot create the SSAR", func() {
			cli := fake.NewSimpleDynamicClient(scheme.Scheme)

			// Create an SSAR to force an error based on how the
			// fake.NewSimpleDynamicClient handles temporarily storing created resources.
			canVerbResource(cli, corev1.SchemeGroupVersion.WithResource("pods"), "list", "default")

			canVerb, err := canVerbResource(cli, corev1.SchemeGroupVersion.WithResource("pods"), "list", "default")
			Expect(err).ShouldNot(BeNil())
			Expect(err.Error()).Should(ContainSubstring("encountered an error creating a SSAR"))
			Expect(canVerb).Should(BeFalse())
		})
	})

	Context("When using canListWatchResourceForNamespace() helper function", func() {
		It("Should return true if can list && watch Pod resources in namespace `default`", func() {
			cli, err := dynamic.NewForConfig(testenv.Config)
			Expect(err).Should(BeNil())

			canListWatch, err := canListWatchResourceForNamespace(cli, corev1.SchemeGroupVersion.WithResource("pods"), "default")
			Expect(err).Should(BeNil())
			Expect(canListWatch).Should(BeTrue())
		})

		// NOTE: Attempted to create a test that checks for a return of false with no error, but there is a limitation in the fake.NewSimpleDynamicClient.
		// The fake.NewSimpleDynamicClient writes all resources to a temporary location. Due to the way this is handled, creating multiple SSARs
		// results in a clash of the SSAR already existing. This behavior differs from that of a real Kubernetes cluster.

		It("Should return an error if it cannot create an SSAR to check for list permissions", func() {
			cli := fake.NewSimpleDynamicClient(scheme.Scheme)

			// Create an SSAR to force an error based on how the
			// fake.NewSimpleDynamicClient handles temporarily storing created resources.
			canVerbResource(cli, corev1.SchemeGroupVersion.WithResource("pods"), "list", "default")

			canListWatch, err := canListWatchResourceForNamespace(cli, corev1.SchemeGroupVersion.WithResource("pods"), "default")
			Expect(err).ShouldNot(BeNil())
			Expect(err.Error()).Should(ContainSubstring("encountered an error checking for list permissions"))
			Expect(canListWatch).Should(BeFalse())
		})

		It("Should return an error if it cannot create an SSAR to check for watch permissions", func() {
			cli := fake.NewSimpleDynamicClient(scheme.Scheme)

			canListWatch, err := canListWatchResourceForNamespace(cli, corev1.SchemeGroupVersion.WithResource("pods"), "default")
			Expect(err).ShouldNot(BeNil())
			Expect(err.Error()).Should(ContainSubstring("encountered an error checking for watch permissions"))
			Expect(canListWatch).Should(BeFalse())
		})
	})

	Context("When using canClusterListWatchResource() helper function", func() {
		It("Should return true if can list && watch Pod resources at the cluster level", func() {
			cli, err := dynamic.NewForConfig(testenv.Config)
			Expect(err).Should(BeNil())

			canListWatch, err := canClusterListWatchResource(cli, corev1.SchemeGroupVersion.WithResource("pods"))
			Expect(err).Should(BeNil())
			Expect(canListWatch).Should(BeTrue())
		})
	})

})

type emptyRESTScope struct{}

func (e *emptyRESTScope) Name() meta.RESTScopeName {
	return ""
}
