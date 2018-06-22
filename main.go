package main

import (
	stdlog "log"
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
	"github.com/rs/zerolog/log"

	apiv1 "github.com/ericchiang/k8s/api/v1"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// annotationGKEPreemptibleKillerState is the key of the annotation to use to store the expiry datetime
const annotationGKEPreemptibleKillerState string = "estafette.io/gke-preemptible-killer-state"

// GKEPreemptibleKillerState represents the state of gke-preemptible-killer
type GKEPreemptibleKillerState struct {
	ExpiryDatetime string `json:"expiryDatetime"`
}

var (
	// flags
	drainTimeout = kingpin.Flag("drain-timeout", "Max time in second to wait before deleting a node.").
			Envar("DRAIN_TIMEOUT").
			Default("300").
			Int()
	prometheusAddress = kingpin.Flag("metrics-listen-address", "The address to listen on for Prometheus metrics requests.").
				Envar("METRICS_LISTEN_ADDRESS").
				Default(":9001").
				String()
	prometheusMetricsPath = kingpin.Flag("metrics-path", "The path to listen for Prometheus metrics requests.").
				Envar("METRICS_PATH").
				Default("/metrics").
				String()
	interval = kingpin.Flag("interval", "Time in second to wait between each node check.").
			Envar("INTERVAL").
			Default("600").
			Short('i').
			Int()
	kubeConfigPath = kingpin.Flag("kubeconfig", "Provide the path to the kube config path, usually located in ~/.kube/config. For out of cluster execution").
			Envar("KUBECONFIG").
			String()

	// define prometheus counter
	nodeTotals = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "estafette_gke_preemptible_killer_node_totals",
			Help: "Number of processed nodes.",
		},
		[]string{"status"},
	)

	// application version
	version         string
	branch          string
	revision        string
	buildDate       string
	goVersion       = runtime.Version()
	randomEstafette = rand.New(rand.NewSource(time.Now().UnixNano()))
)

func init() {
	// Metrics have to be registered to be exposed:
	prometheus.MustRegister(nodeTotals)
}

func main() {
	kingpin.Parse()

	// log as severity for stackdriver logging to recognize the level
	zerolog.LevelFieldName = "severity"

	// set some default fields added to all logs
	log.Logger = zerolog.New(os.Stdout).With().
		Timestamp().
		Str("app", "estafette-gke-preemptible-killer").
		Str("version", version).
		Logger()

	// use zerolog for any logs sent via standard log library
	stdlog.SetFlags(0)
	stdlog.SetOutput(log.Logger)

	// log startup message
	log.Info().
		Str("branch", branch).
		Str("revision", revision).
		Str("buildDate", buildDate).
		Str("goVersion", goVersion).
		Msg("Starting estafette-gke-preemptible-killer...")

	kubernetes, err := NewKubernetesClient(os.Getenv("KUBERNETES_SERVICE_HOST"), os.Getenv("KUBERNETES_SERVICE_PORT"),
		os.Getenv("KUBERNETES_NAMESPACE"), *kubeConfigPath)

	if err != nil {
		log.Fatal().Err(err).Msg("Error initializing Kubernetes client")
	}

	// start prometheus
	go func() {
		log.Info().
			Str("port", *prometheusAddress).
			Str("path", *prometheusMetricsPath).
			Msg("Serving Prometheus metrics...")

		http.Handle(*prometheusMetricsPath, promhttp.Handler())

		if err := http.ListenAndServe(*prometheusAddress, nil); err != nil {
			log.Fatal().Err(err).Msg("Starting Prometheus listener failed")
		}
	}()

	// define channel and wait group to gracefully shutdown the application
	gracefulShutdown := make(chan os.Signal)
	signal.Notify(gracefulShutdown, syscall.SIGTERM, syscall.SIGINT)
	waitGroup := &sync.WaitGroup{}

	// process nodes
	go func(waitGroup *sync.WaitGroup) {
		for {
			log.Info().Msg("Listing all preemptible nodes for cluster...")

			sleepTime := ApplyJitter(*interval)

			nodes, err := kubernetes.GetPreemptibleNodes()

			if err != nil {
				log.Error().Err(err).Msg("Error while getting the list of preemptible nodes")
				log.Info().Msgf("Sleeping for %v seconds...", sleepTime)
				time.Sleep(time.Duration(sleepTime) * time.Second)
				continue
			}

			log.Info().Msgf("Cluster has %v preemptible nodes", len(nodes.Items))

			for _, node := range nodes.Items {
				waitGroup.Add(1)
				err := processNode(kubernetes, node)
				waitGroup.Done()

				if err != nil {
					nodeTotals.With(prometheus.Labels{"status": "failed"}).Inc()
					log.Error().
						Err(err).
						Str("host", *node.Metadata.Name).
						Msg("Error while processing node")
					continue
				}
			}

			log.Info().Msgf("Sleeping for %v seconds...", sleepTime)
			time.Sleep(time.Duration(sleepTime) * time.Second)
		}
	}(waitGroup)

	signalReceived := <-gracefulShutdown
	log.Info().
		Msgf("Received signal %v. Sending shutdown and waiting on goroutines...", signalReceived)

	waitGroup.Wait()

	log.Info().Msg("Shutting down...")
}

// getCurrentNodeState return the state of the node by reading its metadata annotations
func getCurrentNodeState(node *apiv1.Node) (state GKEPreemptibleKillerState) {
	var ok bool

	state.ExpiryDatetime, ok = node.Metadata.Annotations[annotationGKEPreemptibleKillerState]

	if !ok {
		state.ExpiryDatetime = ""
	}
	return
}

// getDesiredNodeState define the state of the node, update node annotations if not present
func getDesiredNodeState(k KubernetesClient, node *apiv1.Node) (state GKEPreemptibleKillerState, err error) {
	t := time.Unix(*node.Metadata.CreationTimestamp.Seconds, 0)
	drainTimeoutTime := time.Duration(*drainTimeout) * time.Second
	// 43200 = 12h * 60m * 60s
	randomTimeBetween0to12 := time.Duration(randomEstafette.Intn((43200)-*drainTimeout)) * time.Second
	expiryDatetime := t.Add(12*time.Hour + drainTimeoutTime + randomTimeBetween0to12).UTC()

	state.ExpiryDatetime = expiryDatetime.Format(time.RFC3339)

	log.Info().
		Str("host", *node.Metadata.Name).
		Msgf("Annotation not found, adding %s to %s", annotationGKEPreemptibleKillerState, state.ExpiryDatetime)

	err = k.SetNodeAnnotation(*node.Metadata.Name, annotationGKEPreemptibleKillerState, state.ExpiryDatetime)

	if err != nil {
		log.Warn().
			Err(err).
			Str("host", *node.Metadata.Name).
			Msg("Error updating node metadata, continuing with node CreationTimestamp value instead")

		state.ExpiryDatetime = t.UTC().Format(time.RFC3339)
		nodeTotals.With(prometheus.Labels{"status": "failed"}).Inc()

		return
	}

	nodeTotals.With(prometheus.Labels{"status": "annotated"}).Inc()

	return
}

// processNode returns the time to delete a node after n minutes
func processNode(k KubernetesClient, node *apiv1.Node) (err error) {
	// get current node state
	state := getCurrentNodeState(node)

	// set node state if doesn't already have annotations
	if state.ExpiryDatetime == "" {
		state, _ = getDesiredNodeState(k, node)
	}

	// compute time difference
	now := time.Now().UTC()
	expiryDatetime, err := time.Parse(time.RFC3339, state.ExpiryDatetime)

	if err != nil {
		log.Error().
			Err(err).
			Str("host", *node.Metadata.Name).
			Msgf("Error parsing expiry datetime with value '%s'", state.ExpiryDatetime)
		return
	}

	timeDiff := expiryDatetime.Sub(now).Minutes()

	// check if we need to delete the node or not
	if timeDiff < 0 {
		log.Info().
			Str("host", *node.Metadata.Name).
			Msgf("Node expired %.0f minute(s) ago, deleting...", timeDiff)

		// set node unschedulable
		err = k.SetUnschedulableState(*node.Metadata.Name, true)
		if err != nil {
			log.Error().
				Err(err).
				Str("host", *node.Metadata.Name).
				Msg("Error setting node to unschedulable state")
			return
		}

		var projectId string
		var zone string
		projectId, zone, err = k.GetProjectIdAndZoneFromNode(*node.Metadata.Name)

		if err != nil {
			log.Error().
				Err(err).
				Str("host", *node.Metadata.Name).
				Msg("Error getting project id and zone from node")
			return
		}

		var gcloud GCloudClient
		gcloud, err = NewGCloudClient(projectId, zone)

		if err != nil {
			log.Error().
				Err(err).
				Str("host", *node.Metadata.Name).
				Msg("Error creating GCloud client")
			return
		}

		// drain kubernetes node
		err = k.DrainNode(*node.Metadata.Name, *drainTimeout)

		if err != nil {
			log.Error().
				Err(err).
				Str("host", *node.Metadata.Name).
				Msg("Error draining kubernetes node")
			return
		}

		// drain kube-dns from kubernetes node
		err = k.DrainKubeDNSFromNode(*node.Metadata.Name, *drainTimeout)

		if err != nil {
			log.Error().
				Err(err).
				Str("host", *node.Metadata.Name).
				Msg("Error draining kube-dns from kubernetes node")
			return
		}

		// delete gcloud instance
		err = gcloud.DeleteNode(*node.Metadata.Name)

		if err != nil {
			log.Error().
				Err(err).
				Str("host", *node.Metadata.Name).
				Msg("Error deleting GCloud instance")
			return
		}

		nodeTotals.With(prometheus.Labels{"status": "killed"}).Inc()

		log.Info().
			Str("host", *node.Metadata.Name).
			Msg("Node deleted")

		return
	}

	nodeTotals.With(prometheus.Labels{"status": "skipped"}).Inc()

	log.Info().
		Str("host", *node.Metadata.Name).
		Msgf("%.0f minute(s) to go before kill, keeping node", timeDiff)

	return
}
