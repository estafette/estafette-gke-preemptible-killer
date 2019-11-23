package main

import (
	"fmt"
	"math/rand"
	"os"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/alecthomas/kingpin"
	apiv1 "github.com/ericchiang/k8s/api/v1"
	foundation "github.com/estafette/estafette-foundation"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/rs/zerolog/log"
)

const (
	// annotationGKEPreemptibleKillerState is the key of the annotation to use to store the expiry datetime
	annotationGKEPreemptibleKillerState string = "estafette.io/gke-preemptible-killer-state"
)

// GKEPreemptibleKillerState represents the state of gke-preemptible-killer
type GKEPreemptibleKillerState struct {
	ExpiryDatetime string `json:"expiryDatetime"`
}

var (
	// flags
	blacklist = kingpin.Flag("blacklist-hours", "List of UTC time intervals in the form of `09:00 - 12:00, 13:00 - 18:00` in which deletion is NOT allowed").
			Envar("BLACKLIST_HOURS").
			Default("").
			Short('b').
			String()
	drainTimeout = kingpin.Flag("drain-timeout", "Max time in second to wait before deleting a node.").
			Envar("DRAIN_TIMEOUT").
			Default("300").
			Int()
	filters = kingpin.Flag("filters", "Label filters in the form of `key1: value1[, value2[, ...]][; key2: value3[, value4[, ...]], ...]`").
		Default("").
		Envar("FILTERS").
		Short('f').
		String()
	interval = kingpin.Flag("interval", "Time in second to wait between each node check.").
			Envar("INTERVAL").
			Default("600").
			Short('i').
			Int()
	kubeConfigPath = kingpin.Flag("kubeconfig", "Provide the path to the kube config path, usually located in ~/.kube/config. For out of cluster execution").
			Envar("KUBECONFIG").
			String()
	whitelist = kingpin.Flag("whitelist-hours", "List of UTC time intervals in the form of `09:00 - 12:00, 13:00 - 18:00` in which deletion is allowed and preferred").
			Envar("WHITELIST_HOURS").
			Default("").
			Short('w').
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
	appgroup  string
	app       string
	version   string
	branch    string
	revision  string
	buildDate string
	goVersion = runtime.Version()

	// Various internals
	randomEstafette   = rand.New(rand.NewSource(time.Now().UnixNano()))
	labelFilters      = map[string]string{}
	whitelistInstance WhitelistInstance
)

func init() {
	// Metrics have to be registered to be exposed:
	prometheus.MustRegister(nodeTotals)
}

func main() {
	// parse command line parameters
	kingpin.Parse()

	// init log format from envvar ESTAFETTE_LOG_FORMAT
	foundation.InitLoggingFromEnv(appgroup, app, version, branch, revision, buildDate)

	// configure prometheus metrics endpoint
	foundation.InitMetrics()

	if *filters != "" {
		*filters = strings.Replace(*filters, " ", "", -1)
		pairs := strings.Split(*filters, ";")
		for _, pair := range pairs {
			keyValue := strings.Split(pair, ":")

			// Check format.
			if len(keyValue) != 2 {
				panic(fmt.Sprintf("filter '%v' should be of the form `label_key: label_value`", keyValue))
			}

			labelFilters[keyValue[0]] = keyValue[1]
		}
	}

	whitelistInstance.blacklist = *blacklist
	whitelistInstance.whitelist = *whitelist
	whitelistInstance.parseArguments()

	kubernetes, err := NewKubernetesClient(os.Getenv("KUBERNETES_SERVICE_HOST"), os.Getenv("KUBERNETES_SERVICE_PORT"),
		os.Getenv("KUBERNETES_NAMESPACE"), *kubeConfigPath)
	if err != nil {
		log.Fatal().Err(err).Msg("Error initializing Kubernetes client")
	}

	// define channel and wait group to gracefully shutdown the application
	gracefulShutdown, waitGroup := foundation.InitGracefulShutdownHandling()

	// process nodes
	go func(waitGroup *sync.WaitGroup) {
		for {
			log.Info().Msg("Listing all preemptible nodes for cluster...")

			sleepTime := ApplyJitter(*interval)

			nodes, err := kubernetes.GetPreemptibleNodes(labelFilters)

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

	// handle graceful shutdown after sigterm
	foundation.HandleGracefulShutdown(gracefulShutdown, waitGroup)
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
	t := time.Unix(*node.Metadata.CreationTimestamp.Seconds, 0).UTC()
	drainTimeoutTime := time.Duration(*drainTimeout) * time.Second
	// 43200 = 12h * 60m * 60s
	randomTimeBetween0to12 := time.Duration(randomEstafette.Intn((43200)-*drainTimeout)) * time.Second
	timeToBeAdded := 12*time.Hour + drainTimeoutTime + randomTimeBetween0to12

	expiryDatetime := whitelistInstance.getExpiryDate(t, timeToBeAdded)
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

		state.ExpiryDatetime = t.Format(time.RFC3339)
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

		var projectID string
		var zone string
		projectID, zone, err = k.GetProjectIdAndZoneFromNode(*node.Metadata.Name)

		if err != nil {
			log.Error().
				Err(err).
				Str("host", *node.Metadata.Name).
				Msg("Error getting project id and zone from node")
			return
		}

		var gcloud GCloudClient
		gcloud, err = NewGCloudClient(projectID, zone)

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

		// delete node from kubernetes cluster
		err = k.DeleteNode(*node.Metadata.Name)

		if err != nil {
			log.Error().
				Err(err).
				Str("host", *node.Metadata.Name).
				Msg("Error deleting node")
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
