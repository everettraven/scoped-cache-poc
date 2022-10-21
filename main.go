package main

import (
	"context"
	"fmt"
	"os"
	"time"

	scopecache "github.com/everettraven/scoped-cache-poc/pkg/cache"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/config"
)

func main() {
	// namespacedCache()
	// clusterCache()
	scopedCache()
}

func scopedCache() {
	cfg := config.GetConfigOrDie()

	cacheScheme := runtime.NewScheme()
	err := corev1.AddToScheme(cacheScheme)
	if err != nil {
		fmt.Println("error adding to cache scheme:", err)
		os.Exit(1)
	}

	scopeCacheBuild, err := scopecache.ScopedCacheBuilder()(cfg, cache.Options{Scheme: cacheScheme})
	if err != nil {
		fmt.Println("error building ScopedCache:", err)
		os.Exit(3)
	}

	scopeCache := scopeCacheBuild.(*scopecache.ScopedCache)

	cli, err := client.New(cfg, client.Options{})
	if err != nil {
		fmt.Println("error creating client: %w", err)
		os.Exit(2)
	}

	createResources(cli)

	// create an informer at the cluster level to look for ServiceAccounts
	k8sClient := kubernetes.NewForConfigOrDie(cfg)
	resync := 5 * time.Second

	saGvk := corev1.SchemeGroupVersion.WithKind("ServiceAccount")
	infFact := informers.NewSharedInformerFactoryWithOptions(k8sClient, resync)

	saInf, err := infFact.ForResource(corev1.SchemeGroupVersion.WithResource("serviceaccounts"))
	if err != nil {
		fmt.Println("error in getting pod informer | error:", err)
	}

	scopeCache.AddInformer(scopecache.InformerOptions{
		Gvk:      saGvk,
		Key:      "serviceaccount-informer",
		Informer: saInf,
		Dependent: &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				UID: types.UID("dependent-0"),
			},
		},
	})

	// create some informers for pods in specific namespaces
	podGvk := corev1.SchemeGroupVersion.WithKind("Pod")
	for i := 0; i < 3; i++ {
		namespace := fmt.Sprintf("testns-%d", i)
		infFact := informers.NewSharedInformerFactoryWithOptions(k8sClient, resync, informers.WithNamespace(namespace))

		podInf, err := infFact.ForResource(corev1.SchemeGroupVersion.WithResource("pods"))
		if err != nil {
			fmt.Println("error in getting pod informer for ns", namespace, "| error:", err)
			continue
		}

		scopeCache.AddInformer(scopecache.InformerOptions{
			Namespace: namespace,
			Gvk:       podGvk,
			Key:       "pod-informer",
			Informer:  podInf,
			Dependent: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					UID: types.UID(fmt.Sprintf("dependent-%d", i)),
				},
			},
		})
	}

	scopeCache.Start(context.Background())

	// give resources time to populate
	time.Sleep(10 * time.Second)

	fmt.Println("Get Pod `testns-0/testpod-0`")
	fmt.Println("=================================")

	// get a pod
	pod := &corev1.Pod{}
	err = scopeCache.Get(context.Background(), types.NamespacedName{Namespace: "testns-0", Name: "testpod-0"}, pod)
	if err != nil {
		fmt.Println("error getting pod from cache:", err)
	}

	fmt.Println("Pod:", pod)

	fmt.Println("=================================")

	fmt.Println("Pods in all watched namespaces:")
	fmt.Println("=================================")
	podList := &corev1.PodList{}
	err = scopeCache.List(context.Background(), podList, &client.ListOptions{LabelSelector: labels.Everything()})
	if err != nil {
		fmt.Println("error listing pods:", err)
	}
	for _, podObj := range podList.Items {
		fmt.Println("pod.Namespace:", podObj.Namespace, "pod.Name:", podObj.Name)
	}
	fmt.Println("=================================")

	fmt.Println("Get ServiceAccount `default/default`")
	fmt.Println("=================================")

	sa := &corev1.ServiceAccount{}
	err = scopeCache.Get(context.Background(), types.NamespacedName{Namespace: "default", Name: "default"}, sa)
	if err != nil {
		fmt.Println("error getting ServiceAccount:", err)
	}

	fmt.Println("ServiceAccount:", sa)

	fmt.Println("=================================")

	fmt.Println("ServiceAccounts across the cluster")
	fmt.Println("=================================")
	saList := &corev1.ServiceAccountList{}
	err = scopeCache.List(context.Background(), saList, &client.ListOptions{LabelSelector: labels.Everything()})
	if err != nil {
		fmt.Println("error listing serviceaccounts:", err)
	}
	for _, saObj := range saList.Items {
		fmt.Println("serviceaccount.Namespace:", saObj.Namespace, "serviceaccount.Name:", saObj.Name)
	}
	fmt.Println("=================================")

}

func clusterCache() {
	cfg := config.GetConfigOrDie()

	clusterCache := scopecache.NewClusterScopedCache()
	err := corev1.AddToScheme(clusterCache.Scheme)
	if err != nil {
		fmt.Println("error adding to cache scheme:", err)
		os.Exit(1)
	}

	cli, err := client.New(cfg, client.Options{})
	if err != nil {
		fmt.Println("error creating client: %w", err)
		os.Exit(2)
	}

	createResources(cli)

	// create some informers
	k8sClient := kubernetes.NewForConfigOrDie(cfg)
	resync := 5 * time.Second

	podGvk := corev1.SchemeGroupVersion.WithKind("Pod")
	infFact := informers.NewSharedInformerFactoryWithOptions(k8sClient, resync)

	podInf, err := infFact.ForResource(corev1.SchemeGroupVersion.WithResource("pods"))
	if err != nil {
		fmt.Println("error in getting pod informer | error:", err)
	}

	clusterCache.AddInformer(scopecache.InformerOptions{
		Gvk:      podGvk,
		Key:      "pod-informer",
		Informer: podInf,
		Dependent: &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				UID: types.UID("dependent-0"),
			},
		},
	})

	// add the informer again with a different dependent
	clusterCache.AddInformer(scopecache.InformerOptions{
		Gvk:      podGvk,
		Key:      "pod-informer",
		Informer: podInf,
		Dependent: &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				UID: types.UID("dependent-1"),
			},
		},
	})

	fmt.Println("pod-informer dependents", len(clusterCache.GvkInformers[podGvk]["pod-informer"].Dependents))

	clusterCache.Start()

	// give resources time to populate
	time.Sleep(10 * time.Second)

	fmt.Println("Get Pod `testns-0/testpod-0`")
	fmt.Println("=================================")

	// get a pod
	pod, err := clusterCache.Get(types.NamespacedName{Namespace: "testns-0", Name: "testpod-0"}, podGvk)
	if err != nil {
		fmt.Println("error getting pod from cache:", err)
	}

	fmt.Println("Pod:", pod)

	fmt.Println("=================================")

	fmt.Println("Pods in all namespaces:")
	fmt.Println("=================================")
	allList, err := clusterCache.List(client.ListOptions{LabelSelector: labels.Everything()}, podGvk)
	for _, obj := range allList {
		podObj := obj.(*corev1.Pod)
		fmt.Println("pod.Namespace:", podObj.Namespace, "pod.Name:", podObj.Name)
	}
	fmt.Println("=================================")
}

// function for running stuff based on the namespaced cache
func namespacedCache() {
	cfg := config.GetConfigOrDie()

	namespacedCache := scopecache.NewNamespaceScopedCache()
	err := corev1.AddToScheme(namespacedCache.Scheme)
	if err != nil {
		fmt.Println("error adding to cache scheme:", err)
		os.Exit(1)
	}

	cli, err := client.New(cfg, client.Options{})
	if err != nil {
		fmt.Println("error creating client: %w", err)
		os.Exit(2)
	}

	createResources(cli)

	// create some informers
	k8sClient := kubernetes.NewForConfigOrDie(cfg)
	resync := 5 * time.Second

	podGvk := corev1.SchemeGroupVersion.WithKind("Pod")
	for i := 0; i < 6; i++ {
		namespace := fmt.Sprintf("testns-%d", i)
		// create a duplicate informer
		if i == 5 {
			namespace = "testns-0"
		}
		infFact := informers.NewSharedInformerFactoryWithOptions(k8sClient, resync, informers.WithNamespace(namespace))

		podInf, err := infFact.ForResource(corev1.SchemeGroupVersion.WithResource("pods"))
		if err != nil {
			fmt.Println("error in getting pod informer for ns", namespace, "| error:", err)
			continue
		}

		namespacedCache.AddInformer(scopecache.InformerOptions{
			Namespace: namespace,
			Gvk:       podGvk,
			Key:       "pod-informer",
			Informer:  podInf,
			Dependent: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					UID: types.UID(fmt.Sprintf("dependent-%d", i)),
				},
			},
		})
	}

	fmt.Println("testns-0 pod informer dependents:", len(namespacedCache.Namespaces["testns-0"][podGvk]["pod-informer"].Dependents))

	namespacedCache.Start()

	// give resources time to populate
	time.Sleep(10 * time.Second)

	fmt.Println("Get Pod `testns-0/testpod-0`")
	fmt.Println("=================================")
	// get a pod
	pod, err := namespacedCache.Get(types.NamespacedName{Namespace: "testns-0", Name: "testpod-0"}, podGvk)
	if err != nil {
		fmt.Println("error getting pod from cache:", err)
	}

	fmt.Println("Pod:", pod)
	fmt.Println("=================================")

	fmt.Println("Pods in all namespaces:")
	fmt.Println("=================================")
	allList, err := namespacedCache.List(client.ListOptions{LabelSelector: labels.Everything()}, podGvk)
	for _, obj := range allList {
		podObj := obj.(*corev1.Pod)
		fmt.Println("pod.Namespace:", podObj.Namespace, "pod.Name:", podObj.Name)
	}
	fmt.Println("=================================")

	fmt.Println("Pods in `testns-0` namespace")
	fmt.Println("=================================")
	nsList, err := namespacedCache.List(client.ListOptions{LabelSelector: labels.Everything(), Namespace: "testns-0"}, podGvk)
	for _, obj := range nsList {
		podObj := obj.(*corev1.Pod)
		fmt.Println("pod.Namespace:", podObj.Namespace, "pod.Name:", podObj.Name)
	}
	fmt.Println("=================================")
}

// create some resources for testing
func createResources(cli client.Client) {
	for i := 0; i < 5; i++ {
		namespace := fmt.Sprintf("testns-%d", i)

		err := cli.Create(context.TODO(), namespaceManifest(namespace))
		if err != nil {
			fmt.Println("error creating ns", namespace, "| error:", err)
		}

		for j := 0; j < 5; j++ {
			podName := fmt.Sprintf("testpod-%d", j)
			err = cli.Create(context.TODO(), podManifest(namespace, podName))
			if err != nil {
				fmt.Println("error creating pod", podName, "| error:", err)
			}
		}
	}
}

func namespaceManifest(name string) *corev1.Namespace {
	return &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
	}
}

func podManifest(namespace string, name string) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
			Name:      name,
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name:  "nginx",
					Image: "nginx:1.14.2",
					Ports: []corev1.ContainerPort{
						{
							ContainerPort: 80,
						},
					},
				},
			},
		},
	}
}
