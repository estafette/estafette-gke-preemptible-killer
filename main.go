package main

import (
	"fmt"
	"math/rand"
	"net/http"
	"os"
	"os/signal"
	"runtime"
	"sync"
	"syscall"
	"time"

	"github.com/alecthomas/kingpin"
	"github.com/rs/zerolog"

	apiv1 "github.com/ericchiang/k8s/api/v1"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// annotationGKEPreemptibleKillerDeleteAfter is the key of the annotation to use to store the time to kill
const annotationGKEPreemptibleKillerDeleteAfter string = "estafette.io/gke-preemptible-killer-delete-after-n-minutes"

var (
	// flags
	drainTimeout = kingpin.Flag("drain-timeout", "Max time in second to wait before deleting a node.").
			Default("300").
			Int()
	prometheusAddress = kingpin.Flag("metrics-listen-address", "The address to listen on for Prometheus metrics requests.").
				Default(":9001").
				String()
	prometheusMetricsPath = kingpin.Flag("metrics-path", "The path to listen for Prometheus metrics requests.").
				Default("/metrics").
				String()
	interval = kingpin.Flag("interval", "Time in second to wait between each node check.").
			Default("120").
			Short('i').
			Int()
	kubeConfigPath = kingpin.Flag("kubeconfig", "Provide the path to the kube config path, usually located in ~/.kube/config. For out of cluster execution").
			String()

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

// Logger is a global logger
var Logger = zerolog.New(os.Stdout).With().
	Timestamp().
	Str("app", "estafette-gke-preemptible-killer").
	Str("version", version).
	Logger()

func init() {
	// Metrics have to be registered to be exposed:
	prometheus.MustRegister(nodeAddedTotals)
	prometheus.MustRegister(nodeDeletedTotals)
}

func main() {
	kingpin.Parse()

	// log startup message
	Logger.Info().
		Str("branch", branch).
		Str("revision", revision).
		Str("buildDate", buildDate).
		Str("goVersion", goVersion).
		Msg("Starting estafette-gke-preemptible-killer...")

	kubernetes, err := NewKubernetesClient(os.Getenv("KUBERNETES_SERVICE_HOST"), os.Getenv("KUBERNETES_SERVICE_PORT"),
		os.Getenv("KUBERNETES_NAMESPACE"), *kubeConfigPath)

	if err != nil {
		Logger.Fatal().Err(err).Msg("Error initializing Kubernetes client")
	}

	// start prometheus
	go func() {
		Logger.Info().
			Str("port", *prometheusAddress).
			Str("path", *prometheusMetricsPath).
			Msg("Serving Prometheus metrics...")

		http.Handle(*prometheusMetricsPath, promhttp.Handler())

		if err := http.ListenAndServe(*prometheusAddress, nil); err != nil {
			Logger.Fatal().Err(err).Msg("Starting Prometheus listener failed")
		}
	}()

	// define channels used to gracefully shutdown the application
	var gracefulShutdown = make(chan os.Signal)
	var shutdown = make(chan bool)

	signal.Notify(gracefulShutdown, syscall.SIGTERM, syscall.SIGINT)

	waitGroup := &sync.WaitGroup{}
	waitGroup.Add(1)

	// process nodes
	go func(shutdown chan bool, waitGroup *sync.WaitGroup) {
		defer waitGroup.Done()
		for {
			Logger.Info().Msg("Processing nodes")

			sleepTime := ApplyJitter(*interval)

			nodes, err := kubernetes.GetPreemptibleNodes()

			if err != nil {
				Logger.Error().Err(err).Msg("Error while getting the list of preemptible nodes")

				Logger.Info().Msgf("Sleeping for %v seconds...", sleepTime)
				time.Sleep(time.Duration(sleepTime) * time.Second)
				continue
			}

			for _, node := range nodes.Items {
				// run process until shutdown is requested via SIGTERM and SIGINT
				select {
				case _ = <-shutdown:
					return
				default:
				}

				err := processNode(kubernetes, node)

				if err != nil {
					Logger.Error().
						Err(err).
						Str("host", *node.Metadata.Name).
						Msg("Error while processing node")
					continue
				}
			}

			Logger.Info().Msgf("Sleeping for %v seconds...", sleepTime)
			time.Sleep(time.Duration(sleepTime) * time.Second)
		}
	}(shutdown, waitGroup)

	signalReceived := <-gracefulShutdown
	Logger.Info().
		Msgf("Received signal %v. Sending shutdown and waiting on goroutines...", signalReceived)

	shutdown <- true
	waitGroup.Wait()

	Logger.Info().Msg("Shutting down...")
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
				err = fmt.Errorf("Error parsing metadata %s with value '%s':\n%v",
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
		deleteAfter = t.Add(24*time.Hour - time.Duration(*drainTimeout)*time.Second - time.Duration(rand.Int63n(24-12))*time.Hour).UTC()

		Logger.Info().
			Str("host", *node.Metadata.Name).
			Msgf("Annotation not found, adding %s to %s", annotationGKEPreemptibleKillerDeleteAfter, deleteAfter)

		err = k.SetNodeAnnotation(*node.Metadata.Name, annotationGKEPreemptibleKillerDeleteAfter, deleteAfter.Format(time.RFC3339))

		if err != nil {
			Logger.Warn().
				Err(err).
				Str("host", *node.Metadata.Name).
				Msg("Error updating node metadata, continuing with node CreationTimestamp value instead")
		}

		nodeAddedTotals.With(prometheus.Labels{"name": *node.Metadata.Name}).Inc()
	}

	// compute time difference
	now := time.Now().UTC()
	timeDiff := deleteAfter.Sub(now).Minutes()

	// check if we need to delete the node or not
	if timeDiff < 0 {
		Logger.Info().
			Str("host", *node.Metadata.Name).
			Msgf("Time diff %f < 0, deleting node...", timeDiff)

		// set node unschedulable
		err = k.SetUnschedulableState(*node.Metadata.Name, true)
		if err != nil {
			Logger.Error().
				Err(err).
				Str("host", *node.Metadata.Name).
				Msg("Error setting node to unschedulable state")
			return
		}

		var projectId string
		var zone string
		projectId, zone, err = k.GetProjectIdAndZoneFromNode(*node.Metadata.Name)

		if err != nil {
			Logger.Error().
				Err(err).
				Str("host", *node.Metadata.Name).
				Msg("Error getting project id and zone from node")
			return
		}

		var gcloud *GCloud
		gcloud, err = NewGCloudClient(projectId, zone)

		if err != nil {
			Logger.Error().
				Err(err).
				Str("host", *node.Metadata.Name).
				Msg("Error creating GCloud client")
			return
		}

		// drain kubernetes node
		err = k.DrainNode(*node.Metadata.Name, *drainTimeout)

		if err != nil {
			Logger.Error().
				Err(err).
				Str("host", *node.Metadata.Name).
				Msg("Error deleting kubernetes node")
			return
		}

		// delete gcloud instance
		err = gcloud.DeleteNode(*node.Metadata.Name)

		if err != nil {
			Logger.Error().
				Err(err).
				Str("host", *node.Metadata.Name).
				Msg("Error deleting GCloud instance")
			return
		}

		nodeDeletedTotals.With(prometheus.Labels{"name": *node.Metadata.Name}).Inc()

		Logger.Info().
			Str("host", *node.Metadata.Name).
			Msg("Node deleted")

		return
	}

	Logger.Info().
		Str("host", *node.Metadata.Name).
		Msgf("Time diff %f, keeping node", timeDiff)

	return
}
