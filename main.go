package main

import (
	"flag"
	"fmt"
	"log"
	"math/rand"
	"net/http"
	"os"
	"runtime"
	"time"

	apiv1 "github.com/ericchiang/k8s/api/v1"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var (
	// flags
	DrainNodeTimeout        = flag.Int("drain-node-timeout", 300, " Max time in second to wait before deleting a node.")
	gracefulShutdownAddr    = flag.String("shutdown-listen-address", ":8080", "The address to listen on for graceful shutdown.")
	gracefulShutdownTimeout = flag.Int("shutdown-timeout", 120, "Max time in second to wait before shutting down the application.")
	prometheusAddr          = flag.String("prometheus-listen-address", ":9101", "The address to listen on for HTTP requests.")
	watchInterval           = flag.Int("watch-interval", 120, "Time in second to wait between each node check.")

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

	// safeQuit should be set to true if no process are pending (pod/node deletion in progress)
	safeQuit bool = true

	// application version
	version   string
	branch    string
	revision  string
	buildDate string
	goVersion = runtime.Version()
)

// annotationGKEPreemptibleKillerDeleteAfter is the key of the annotation to use to store the time to kill
const annotationGKEPreemptibleKillerDeleteAfter string = "estafette.io/gke-preemptible-killer-delete-after-n-minutes"

func init() {
	// Metrics have to be registered to be exposed:
	prometheus.MustRegister(nodeAddedTotals)
	prometheus.MustRegister(nodeDeletedTotals)
}

func main() {
	fmt.Printf("Starting estafette-gke-preemptible-killer (version=%v, branch=%v, revision=%v, buildDate=%v, goVersion=%v)\n",
		version, branch, revision, buildDate, goVersion)

	kubernetes, err := NewKubernetesClient(os.Getenv("KUBERNETES_SERVICE_HOST"), os.Getenv("KUBERNETES_SERVICE_PORT"),
		os.Getenv("KUBERNETES_NAMESPACE"), os.Getenv("KUBECONFIG"))

	if err != nil {
		log.Fatal(err)
	}

	// start prometheus
	go func() {
		log.Println("Start serving Prometheus metrics at :9101/metrics...")
		http.Handle("/metrics", promhttp.Handler())
		log.Fatal(http.ListenAndServe(*prometheusAddr, nil))
	}()

	// gracefull shutdown of the application
	go gracefulShutdown()

	// process nodes
	for {
		log.Printf("Processing nodes")

		sleepTime := ApplyJitter(*watchInterval)

		nodes, err := kubernetes.GetPreemptibleNodes()

		if err != nil {
			log.Printf("Error while getting the list of preemptible nodes: %v\n", err)
			log.Printf("Sleeping for %v seconds...\n", sleepTime)
			time.Sleep(time.Duration(sleepTime) * time.Second)
			continue
		}

		for _, node := range nodes.Items {
			// specify to the gracefulShutdown that we cannot quit until it finish
			safeQuit = false
			err := processNode(kubernetes, node)

			if err != nil {
				log.Printf("[%s] Error while processing node: %v\n", *node.Metadata.Name, err)
				safeQuit = true
				continue
			}
			safeQuit = true
		}

		log.Printf("Sleeping for %v seconds...\n", sleepTime)
		time.Sleep(time.Duration(sleepTime) * time.Second)
	}
}

// gracefulShutdown serve endpoint to graceful stop the application :8080/quit
func gracefulShutdown() {
	http.HandleFunc("/quit", func(w http.ResponseWriter, r *http.Request) {
		defer os.Exit(0)

		// Wait for all processes to finish (pod/node deletion)
		done := make(chan bool)
		go func() {
			for {
				if safeQuit {
					done <- true
					break
				}
			}
		}()

		select {
		case <-done:
			log.Println("Quitting...")
			break
		case <-time.After(time.Duration(*gracefulShutdownTimeout) * time.Second):
			log.Println("Reached graceful shutdown timeout, quitting now")
		}

		w.Write([]byte("Quit"))
	})

	log.Fatal(http.ListenAndServe(*gracefulShutdownAddr, nil))
}

// processNode returns the time to delete a node after n minutes
func processNode(k *Kubernetes, node *apiv1.Node) (err error) {
	var deleteAfter time.Time
	var keyExist bool = false

	// parse node annotation if it exist
	for key, value := range node.Metadata.Annotations {
		if key == annotationGKEPreemptibleKillerDeleteAfter {
			deleteAfter, err = time.Parse(time.RFC3339, value)

			if err != nil {
				err = fmt.Errorf("[%s] Error parsing metadata %s with value '%s':\n%v", *node.Metadata.Name,
					annotationGKEPreemptibleKillerDeleteAfter, deleteAfter, err)
				return
			}
			keyExist = true
			break
		}
	}

	// add the annotation if it doesn't exit
	if !keyExist {
		t := time.Unix(*node.Metadata.CreationTimestamp.Seconds, 0)
		deleteAfter = t.Add(24*time.Hour - time.Duration(*DrainNodeTimeout)*time.Second - time.Duration(rand.Int63n(24-12))*time.Hour).UTC()

		log.Printf("[%s] Annotation not found, adding %s to %s\n", *node.Metadata.Name,
			annotationGKEPreemptibleKillerDeleteAfter, deleteAfter)

		err = k.SetNodeAnnotation(node, annotationGKEPreemptibleKillerDeleteAfter, deleteAfter.Format(time.RFC3339))

		if err != nil {
			err = fmt.Errorf("[%s] Error updating node metadata: %v, continuing with node CreationTimestamp value instead",
				*node.Metadata.Name, err)
		}

		nodeAddedTotals.With(prometheus.Labels{"name": *node.Metadata.Name}).Inc()
	}

	// compute time difference
	now := time.Now().UTC()
	timeDiff := deleteAfter.Sub(now).Minutes()

	// check if we need to delete the node or not
	if timeDiff < 0 {
		log.Printf("[%s] Time diff %f < 0, deleting node...\n", *node.Metadata.Name, timeDiff)

		// set node unschedulable
		err = k.SetUnschedulableState(*node.Metadata.Name, true)
		if err != nil {
			err = fmt.Errorf("[%s] Error setting node to unschedulable state: %v\n", *node.Metadata.Name, err)
			return
		}

		var projectId string
		var zone string
		projectId, zone, err = k.GetProjectIdAndZoneFromNode(*node.Metadata.Name)

		if err != nil {
			err = fmt.Errorf("[%s] Error getting project id and zone from node: %v\n", *node.Metadata.Name, err)
			return
		}

		var gcloud *GCloud
		gcloud, err = NewGCloudClient(projectId, zone)

		if err != nil {
			err = fmt.Errorf("[%s] Error creating GCloud client: %v\n", *node.Metadata.Name, err)
			return
		}

		// drain kubernetes node
		err = k.DrainNode(*node.Metadata.Name)

		if err != nil {
			err = fmt.Errorf("[%s] Error deleting kubernetes node: %v\n", *node.Metadata.Name, err)
			return
		}

		// delete gcloud instance
		err = gcloud.DeleteNode(*node.Metadata.Name)

		if err != nil {
			err = fmt.Errorf("[%s] Error deleting GCloud instance: %v\n", *node.Metadata.Name, err)
			return
		}

		nodeDeletedTotals.With(prometheus.Labels{"name": *node.Metadata.Name}).Inc()

		log.Printf("[%s] Node deleted\n", *node.Metadata.Name)

		return
	}

	log.Printf("[%s] Time diff %f, keeping node\n", *node.Metadata.Name, timeDiff)

	return
}
