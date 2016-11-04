package main

import (
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"k8s.io/client-go/1.5/kubernetes"
	"k8s.io/client-go/1.5/pkg/api"
	"k8s.io/client-go/1.5/pkg/api/v1"
	"k8s.io/client-go/1.5/pkg/fields"
	"k8s.io/client-go/1.5/rest"
	"k8s.io/client-go/1.5/tools/cache"
	"k8s.io/client-go/1.5/tools/clientcmd"

	// Only required to authenticate against GKE clusters
	_ "k8s.io/client-go/1.5/plugin/pkg/client/auth/gcp"
)

func main() {
	kubeconfig := flag.String("kubeconfig", "", "Path to a kube config. Only required if out-of-cluster.")
	flag.Parse()

	// Create the client config. Use kubeconfig if given, otherwise assume in-cluster.
	config, err := buildConfig(*kubeconfig)
	if err != nil {
		panic(err)
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		panic(err)
	}

	stopchan := make(chan struct{}, 1)
	source := cache.NewListWatchFromClient(
		clientset.CoreClient,
		"pods",
		api.NamespaceAll,
		fields.Everything())

	store, controller := cache.NewInformer(
		source,

		// The object type.
		&v1.Pod{},

		// resyncPeriod
		// Every resyncPeriod, all of your resources will trigger either a CREATE/UPDATE
		// event for reconciliation purposes.
		time.Second*30,

		// Your custom resource event handlers.
		cache.ResourceEventHandlerFuncs{
			// Takes a single argument of type interface{}.
			// Called on controller startup and when new resources are created.
			AddFunc: create,

			// Takes two arguments of type interface{}.
			// Called on resource update and every resyncPeriod on existing resources.
			UpdateFunc: update,

			// Takes a single argument of type interface{}.
			// Called on resource deletion.
			DeleteFunc: delete,
		})

	// store can be used to List and Get, never to write directly
	fmt.Println("listing pods from store:")
	for _, obj := range store.List() {
		pod := obj.(*v1.Pod)
		fmt.Printf("%#v\n", pod)
	}

	// the controller run starts the event processing loop
	go controller.Run(stopchan)

	// and now we block on a signal
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM)
	for {
		select {
		case s := <-signals:
			fmt.Printf("received signal %#v, exiting...\n", s)
			close(stopchan)
			os.Exit(0)
		}
	}
}

// Handler functions as per the controller above.
// Note the coercion of the interface{} into a pointer of the expected type.

func create(obj interface{}) {
	pod := obj.(*v1.Pod)

	fmt.Println("POD CREATED:", podWithNamespace(pod))
}

func update(old, new interface{}) {
	oldpod := old.(*v1.Pod)
	// newpod := new.(*v1.Pod)

	fmt.Println("POD UPDATED:", podWithNamespace(oldpod))
}

func delete(obj interface{}) {
	pod := obj.(*v1.Pod)

	fmt.Println("POD DELETED:", podWithNamespace(pod))
}

// convenience functions

func podWithNamespace(pod *v1.Pod) string {
	return fmt.Sprintf("%s/%s", pod.Namespace, pod.Name)
}

func buildConfig(kubeconfig string) (*rest.Config, error) {
	if kubeconfig != "" {
		return clientcmd.BuildConfigFromFlags("", kubeconfig)
	}
	return rest.InClusterConfig()
}
