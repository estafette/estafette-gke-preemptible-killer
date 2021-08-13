package main

import (
	"context"
	"fmt"
	"math"
	"math/rand"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/alecthomas/kingpin"
	foundation "github.com/estafette/estafette-foundation"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/rs/zerolog/log"

	v1 "k8s.io/api/core/v1"
	k8sruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
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
	foundation.InitLoggingFromEnv(foundation.NewApplicationInfo(appgroup, app, version, branch, revision, buildDate))

	// create context to handle cancellation
	ctx := foundation.InitCancellationContext(context.Background())

	// init /liveness endpoint
	foundation.InitLiveness()

	// configure prometheus metrics endpoint
	foundation.InitMetrics()

	// create kubernetes api client
	kubeClientConfig, err := rest.InClusterConfig()
	if err != nil {
		log.Fatal().Err(err)
	}
	// creates the clientset
	kubeClientset, err := kubernetes.NewForConfig(kubeClientConfig)
	if err != nil {
		log.Fatal().Err(err)
	}

	// create the shared informer factory and use the client to connect to Kubernetes API
	// factory := informers.NewSharedInformerFactory(kubeClientset, 0)

	// create a channel to stop the shared informers gracefully
	stopper := make(chan struct{})
	defer close(stopper)

	// handle kubernetes API crashes
	defer k8sruntime.HandleCrash()

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

	kubernetesClient, err := NewKubernetesClient(kubeClientset)
	if err != nil {
		log.Fatal().Err(err).Msg("Error initializing Kubernetes client")
	}

	// define channel and wait group to gracefully shutdown the application
	gracefulShutdown, waitGroup := foundation.InitGracefulShutdownHandling()

	// process nodes
	go func(waitGroup *sync.WaitGroup, kubernetesClient KubernetesClient, ctx context.Context) {
		for {
			log.Info().Msg("Listing all preemptible nodes for cluster...")

			sleepTime := ApplyJitter(*interval)

			nodes, err := kubernetesClient.GetPreemptibleNodes(ctx, labelFilters)

			if err != nil {
				log.Error().Err(err).Msg("Error while getting the list of preemptible nodes")
				log.Info().Msgf("Sleeping for %v seconds...", sleepTime)
				time.Sleep(time.Duration(sleepTime) * time.Second)
				continue
			}

			log.Info().Msgf("Cluster has %v preemptible nodes", len(nodes.Items))

			for _, node := range nodes.Items {
				waitGroup.Add(1)
				err := processNode(ctx, kubernetesClient, node)
				waitGroup.Done()

				if err != nil {
					nodeTotals.With(prometheus.Labels{"status": "failed"}).Inc()
					log.Error().
						Err(err).
						Str("host", node.ObjectMeta.Name).
						Msg("Error while processing node")
					continue
				}
			}

			log.Info().Msgf("Sleeping for %v seconds...", sleepTime)
			time.Sleep(time.Duration(sleepTime) * time.Second)
		}
	}(waitGroup, kubernetesClient, ctx)

	// handle graceful shutdown after sigterm
	foundation.HandleGracefulShutdown(gracefulShutdown, waitGroup)
}

// getCurrentNodeState return the state of the node by reading its metadata annotations
func getCurrentNodeState(node v1.Node) (state GKEPreemptibleKillerState) {
	var ok bool

	state.ExpiryDatetime, ok = node.ObjectMeta.Annotations[annotationGKEPreemptibleKillerState]

	if !ok {
		state.ExpiryDatetime = ""
	}
	return
}

// getDesiredNodeState define the state of the node, update node annotations if not present
func getDesiredNodeState(now time.Time, ctx context.Context, kubernetesClient KubernetesClient, node v1.Node) (state GKEPreemptibleKillerState, err error) {

	twelveHours := 12 * time.Hour
	twentyFourHours := 24 * time.Hour

	drainTimeoutTime := time.Duration(*drainTimeout) * time.Second

	creationTime := node.ObjectMeta.CreationTimestamp.Time
	nodeDeletedBy := creationTime.Add(twentyFourHours)

	expectedRemainingLife := time.Duration(math.Max(float64(nodeDeletedBy.Sub(now)), 0))
	kickOffDeletionBy := time.Duration(math.Max(float64(expectedRemainingLife-drainTimeoutTime), 0))

	var randomOffset time.Duration
	if kickOffDeletionBy > 0 {
		randomOffset = time.Duration(randomEstafette.Intn(int(kickOffDeletionBy)))
	}
	if expectedRemainingLife > twelveHours && randomOffset < twelveHours {
		randomOffset += twelveHours
	}

	expiryDateTime := whitelistInstance.getExpiryDate(now, randomOffset)
	state.ExpiryDatetime = expiryDateTime.Format(time.RFC3339)

	log.Info().
		Str("host", node.ObjectMeta.Name).
		Msgf("Annotation not found, adding %s to %s", annotationGKEPreemptibleKillerState, state.ExpiryDatetime)

	err = kubernetesClient.SetNodeAnnotation(ctx, node.ObjectMeta.Name, annotationGKEPreemptibleKillerState, state.ExpiryDatetime)

	if err != nil {
		log.Warn().
			Err(err).
			Str("host", node.ObjectMeta.Name).
			Msg("Error updating node metadata")

		nodeTotals.With(prometheus.Labels{"status": "failed"}).Inc()
		return
	}

	nodeTotals.With(prometheus.Labels{"status": "annotated"}).Inc()

	return
}

// processNode returns the time to delete a node after n minutes
func processNode(ctx context.Context, kubernetesClient KubernetesClient, node v1.Node) (err error) {
	// get current node state
	state := getCurrentNodeState(node)

	// set node state if doesn't already have annotations
	if state.ExpiryDatetime == "" {
		state, _ = getDesiredNodeState(time.Now(), ctx, kubernetesClient, node)
	}

	// compute time difference
	now := time.Now().UTC()
	expiryDatetime, err := time.Parse(time.RFC3339, state.ExpiryDatetime)

	if err != nil {
		log.Error().
			Err(err).
			Str("host", node.ObjectMeta.Name).
			Msgf("Error parsing expiry datetime with value '%s'", state.ExpiryDatetime)
		return
	}

	timeDiff := expiryDatetime.Sub(now).Minutes()

	// check if we need to delete the node or not
	if timeDiff < 0 {
		log.Info().
			Str("host", node.ObjectMeta.Name).
			Msgf("Node expired %.0f minute(s) ago, deleting...", timeDiff)

		// set node unschedulable
		err = kubernetesClient.SetUnschedulableState(ctx, node.ObjectMeta.Name, true)
		if err != nil {
			log.Error().
				Err(err).
				Str("host", node.ObjectMeta.Name).
				Msg("Error setting node to unschedulable state")
			return
		}

		var projectID string
		var zone string
		projectID, zone, err = kubernetesClient.GetProjectIdAndZoneFromNode(ctx, node.ObjectMeta.Name)

		if err != nil {
			log.Error().
				Err(err).
				Str("host", node.ObjectMeta.Name).
				Msg("Error getting project id and zone from node")
			return
		}

		var gcloud GCloudClient
		gcloud, err = NewGCloudClient(projectID, zone)

		if err != nil {
			log.Error().
				Err(err).
				Str("host", node.ObjectMeta.Name).
				Msg("Error creating GCloud client")
			return
		}

		// drain kubernetes node
		err = kubernetesClient.DrainNode(ctx, node.ObjectMeta.Name, *drainTimeout)

		if err != nil {
			log.Error().
				Err(err).
				Str("host", node.ObjectMeta.Name).
				Msg("Error draining kubernetes node")
			return
		}

		// drain kube-dns from kubernetes node
		err = kubernetesClient.DrainKubeDNSFromNode(ctx, node.ObjectMeta.Name, *drainTimeout)

		if err != nil {
			log.Error().
				Err(err).
				Str("host", node.ObjectMeta.Name).
				Msg("Error draining kube-dns from kubernetes node")
			return
		}

		// delete node from kubernetes cluster
		err = kubernetesClient.DeleteNode(ctx, node.ObjectMeta.Name)

		if err != nil {
			log.Error().
				Err(err).
				Str("host", node.ObjectMeta.Name).
				Msg("Error deleting node")
			return
		}

		// delete gcloud instance
		err = gcloud.DeleteNode(node.ObjectMeta.Name)

		if err != nil {
			log.Error().
				Err(err).
				Str("host", node.ObjectMeta.Name).
				Msg("Error deleting GCloud instance")
			return
		}

		nodeTotals.With(prometheus.Labels{"status": "killed"}).Inc()

		log.Info().
			Str("host", node.ObjectMeta.Name).
			Msg("Node deleted")

		return
	}

	nodeTotals.With(prometheus.Labels{"status": "skipped"}).Inc()

	log.Info().
		Str("host", node.ObjectMeta.Name).
		Msgf("%.0f minute(s) to go before kill, keeping node", timeDiff)

	return
}
