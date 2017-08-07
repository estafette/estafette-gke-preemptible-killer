package main

import (
	"flag"
	"fmt"
	"log"
	"math/rand"
	"net/http"
	"os"
	"runtime"
	"sync"
	"time"

	"github.com/ericchiang/k8s"
	apiv1 "github.com/ericchiang/k8s/api/v1"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var (
	addr = flag.String("listen-address", ":9101", "The address to listen on for HTTP requests.")

	// define prometheus counter
	nodeAddedTotals = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "estafette_gke_preemptible_killer_node_added_totals",
			Help: "Number of added nodes.",
		},
		[]string{"name"},
	)
	nodeDeletedTotals = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "estafette_gke_preemptible_killer_node_deleted_totals",
			Help: "Number of deleted nodes.",
		},
		[]string{"name"},
	)

	// application version
	version   string
	branch    string
	revision  string
	buildDate string
	goVersion = runtime.Version()
)

const (
	annotationGKEPreemptibleKillerDeleteAfter string = "estafette.io/gke-preemptible-killer-delete-after-n-minutes"
)

// NodeStore is used to store node name and its associated time when the node need to be killed
type NodeStore struct {
	Items map[string]time.Time
	Mutex *sync.Mutex
}

func init() {
	// Metrics have to be registered to be exposed:
	prometheus.MustRegister(nodeAddedTotals)
	prometheus.MustRegister(nodeDeletedTotals)
}

func main() {
	fmt.Printf("Starting estafette-gke-preemptible-killer (version=%v, branch=%v, revision=%v, buildDate=%v, goVersion=%v)\n",
		version, branch, revision, buildDate, goVersion)

	googleProjectId := os.Getenv("GOOGLE_PROJECT_ID")
	googleInstanceZone := os.Getenv("GOOGLE_INSTANCE_ZONE")

	if googleProjectId == "" {
		log.Fatal("Error: GOOGLE_PROJECT_ID is mandatory")
	}

	if googleInstanceZone == "" {
		log.Fatal("Error: GOOGLE_INSTANCE_ZONE is mandatory")
	}

	gcloud, err := NewGCloudClient(googleProjectId, googleInstanceZone)

	if err != nil {
		log.Fatal(err)
	}

	nodePool := os.Getenv("NODE_POOL")

	if nodePool == "" {
		log.Fatal("Error: NODE_POOL is mandatory")
	}

	kubernetes, err := NewKubernetesClient(os.Getenv("KUBERNETES_SERVICE_HOST"), os.Getenv("KUBERNETES_SERVICE_PORT"),
		os.Getenv("KUBERNETES_NAMESPACE"), os.Getenv("KUBECONFIG"))

	if err != nil {
		log.Fatal(err)
	}

	nodeListStore := &NodeStore{
		Mutex: &sync.Mutex{},
		Items: make(map[string]time.Time),
	}

	// start prometheus
	go func() {
		fmt.Println("Serving Prometheus metrics at :9101/metrics...")
		http.Handle("/metrics", promhttp.Handler())
		log.Fatal(http.ListenAndServe(*addr, nil))
	}()

	// watch for new nodes and initialise the node list store
	go func() {
		for {
			fmt.Println("Watching nodes...")
			watcher, err := kubernetes.WatchNodes(nodePool)

			defer nodeListStore.Mutex.Unlock()

			if err != nil {
				log.Println(err)
			} else {
				// loop indefinitely, unless it errors
				for {
					event, node, err := watcher.Next()
					if err != nil {
						log.Println(err)
						break
					}

					if *event.Type == k8s.EventAdded {
						deleteAfter, err := processNode(kubernetes, node)

						if err != nil {
							log.Println(err)
							continue
						}

						nodeListStore.Mutex.Lock()
						nodeListStore.Items[*node.Metadata.Name] = deleteAfter
						nodeListStore.Mutex.Unlock()

						nodeAddedTotals.With(prometheus.Labels{"name": *node.Metadata.Name}).Inc()

						fmt.Printf("[%s] node added to the store\n", *node.Metadata.Name)

					} else if *event.Type == k8s.EventDeleted {
						nodeListStore.Mutex.Lock()
						delete(nodeListStore.Items, *node.Metadata.Name)
						nodeListStore.Mutex.Unlock()

						nodeDeletedTotals.With(prometheus.Labels{"name": *node.Metadata.Name}).Inc()

						fmt.Printf("[%s] node deleted from the store")
					}
				}
			}

			// sleep random time between 22 and 37 seconds
			sleepTime := ApplyJitter(30)
			fmt.Printf("Sleeping for %v seconds...\n", sleepTime)
			time.Sleep(time.Duration(sleepTime) * time.Second)
		}
	}()

	// loop and wait 1 minute before checking if a node should be killed
	for {
		now := time.Now()

		nodeListStore.Mutex.Lock()
		for nodeName, deleteAfter := range nodeListStore.Items {
			timeDiff := deleteAfter.Sub(now).Minutes()
			fmt.Printf("[%s] Time diff: %f\n", nodeName, timeDiff)

			if timeDiff < 0 {
				fmt.Printf("[%s] Deleting node...\n", nodeName)

				// set node unschedulable
				err = kubernetes.SetSchedulableState(nodeName, false)
				if err != nil {
					err = fmt.Errorf("Error setting schedulable state to node %s: %v", nodeName, err)
					return
				}

				// delete kubernetes node
				err = kubernetes.DeleteNode(nodeName)
				if err != nil {
					err = fmt.Errorf("Error deleting node %s: %v", nodeName, err)
					return
				}

				// delete gcloud instance
				err = gcloud.DeleteNode(nodeName)

				if err != nil {
					err = fmt.Errorf("Error deleting node %s: %v", nodeName, err)
					return
				}

				fmt.Printf("[%s] Deleted\n", nodeName)
				continue
			}

			fmt.Printf("[%s] Keeping node\n", nodeName)
		}
		nodeListStore.Mutex.Unlock()

		time.Sleep(60 * time.Second)
	}
}

// processNode returns the time to delete a node after n minutes
func processNode(k *Kubernetes, node *apiv1.Node) (deleteAfter time.Time, err error) {
	var keyExist bool = false

	fmt.Printf("[%s] Processing\n", *node.Metadata.Name)

	for key, value := range node.Metadata.Annotations {
		if key == annotationGKEPreemptibleKillerDeleteAfter {
			deleteAfter, err = time.Parse("2006-01-02 15:04:05 -0700 MST", value)

			if err != nil {
				err = fmt.Errorf("Error parsing metadata %s with value '%s':\n%v", annotationGKEPreemptibleKillerDeleteAfter, deleteAfter, err)
				return
			}
			keyExist = true
			break
		}
	}

	if !keyExist {
		t := time.Unix(*node.Metadata.CreationTimestamp.Seconds, 0)
		deleteAfter = t.Add(24*time.Hour - time.Duration(rand.Int63n(24-12))*time.Hour).UTC()

		fmt.Printf("[%s] Annotation not found, adding %s to %s\n", *node.Metadata.Name, annotationGKEPreemptibleKillerDeleteAfter, deleteAfter)

		err = k.SetNodeAnnotation(node, annotationGKEPreemptibleKillerDeleteAfter, deleteAfter.String())

		if err != nil {
			err = fmt.Errorf("Error updating node %s metadata: %v", *node.Metadata.Name, err)
			return
		}
	}

	return
}
