package main

import (
	"context"
	"fmt"
	"io/ioutil"
	"net/http"
	"strings"
	"time"

	"github.com/ericchiang/k8s"
	apiv1 "github.com/ericchiang/k8s/api/v1"
	"github.com/ghodss/yaml"
)

type Kubernetes struct {
	Client *k8s.Client
}

type KubernetesClient interface {
	DrainNode(string, int) error
	GetNode(string) (*apiv1.Node, error)
	GetPreemptibleNodes() (*apiv1.NodeList, error)
	GetProjectIdAndZoneFromNode(string) (string, string, error)
	SetNodeAnnotation(string, string, string) error
	SetUnschedulableState(string, bool) error
}

// NewKubernetesClient return a Kubernetes client
func NewKubernetesClient(host string, port string, namespace string, kubeConfigPath string) (kubernetes *Kubernetes, err error) {
	var k8sClient *k8s.Client

	if len(host) > 0 && len(port) > 0 {
		k8sClient, err = k8s.NewInClusterClient()

		if err != nil {
			err = fmt.Errorf("Error loading incluster client:\n%v", err)
			return
		}
	} else if len(kubeConfigPath) > 0 {
		k8sClient, err = loadK8sClient(kubeConfigPath)

		if err != nil {
			err = fmt.Errorf("Error loading client using kubeconfig:\n%v", err)
			return
		}
	} else {
		if namespace == "" {
			namespace = "default"
		}

		k8sClient = &k8s.Client{
			Endpoint:  "http://127.0.0.1:8001",
			Namespace: namespace,
			Client:    &http.Client{},
		}
	}

	kubernetes = &Kubernetes{
		Client: k8sClient,
	}

	return
}

// GetProjectIdAndZoneFromNode returns project id and zone from given node name
// by getting informations from node spec provider id
func (k *Kubernetes) GetProjectIdAndZoneFromNode(name string) (projectId string, zone string, err error) {
	node, err := k.GetNode(name)

	if err != nil {
		return
	}

	s := strings.Split(*node.Spec.ProviderID, "/")
	projectId = s[2]
	zone = s[3]

	return
}

// GetPreemptibleNodes return a list of preemptible node
func (k *Kubernetes) GetPreemptibleNodes() (nodes *apiv1.NodeList, err error) {
	labels := new(k8s.LabelSelector)
	labels.Eq("cloud.google.com/gke-preemptible", "true")
	nodes, err = k.Client.CoreV1().ListNodes(context.Background(), labels.Selector())
	return
}

// GetNode return the node object from given name
func (k *Kubernetes) GetNode(name string) (node *apiv1.Node, err error) {
	node, err = k.Client.CoreV1().GetNode(context.Background(), name)
	return
}

// SetNodeAnnotation add an annotation (key/value) to a node from a given node name
// As the nodes are constantly being updated, the k8s client doesn't support patch feature yet and
// to reduce the chance to hit a failure 409 we fetch the node before update
func (k *Kubernetes) SetNodeAnnotation(name string, key string, value string) (err error) {
	newNode, err := k.GetNode(name)

	if err != nil {
		err = fmt.Errorf("Error getting node information before setting annotation:\n%v", err)
		return
	}

	newNode.Metadata.Annotations[key] = value

	_, err = k.Client.CoreV1().UpdateNode(context.Background(), newNode)
	return
}

// SetUnschedulableState set the unschedulable state of a given node name
func (k *Kubernetes) SetUnschedulableState(name string, unschedulable bool) (err error) {
	node, err := k.GetNode(name)

	if err != nil {
		err = fmt.Errorf("Error getting node information before setting unschedulable state:\n%v", err)
		return
	}

	node.Spec.Unschedulable = &unschedulable

	_, err = k.Client.CoreV1().UpdateNode(context.Background(), node)
	return
}

// filterOutPodByOwnerReferenceKind filter out a list of pods by its owner references kind
func filterOutPodByOwnerReferenceKind(podList []*apiv1.Pod, kind string) (output []*apiv1.Pod) {
	for _, pod := range podList {
		for _, ownerReference := range pod.Metadata.OwnerReferences {
			if *ownerReference.Kind != kind {
				output = append(output, pod)
			}
		}
	}

	return
}

// DrainNode delete every pods from a given node and wait that all pods are removed before it succeed
// it make sure we don't select DaemonSet as, they are not subject to unschedulable state
func (k *Kubernetes) DrainNode(name string, drainTimeout int) (err error) {
	// Select all pods sitting on the node except the one from kube-system
	fieldSelector := k8s.QueryParam("fieldSelector", "spec.nodeName="+name+",metadata.namespace!=kube-system")

	podList, err := k.Client.CoreV1().ListPods(context.Background(), k8s.AllNamespaces, fieldSelector)

	if err != nil {
		return
	}

	// Filter out DaemonSet from the list of pods
	filteredPodList := filterOutPodByOwnerReferenceKind(podList.Items, "DaemonSet")

	Logger.Info().
		Str("host", name).
		Msgf("%d pod(s) found", len(filteredPodList))

	for _, pod := range filteredPodList {
		Logger.Info().
			Str("host", name).
			Msgf("Deleting pod %s", *pod.Metadata.Name)

		err = k.Client.CoreV1().DeletePod(context.Background(), *pod.Metadata.Name, *pod.Metadata.Namespace)

		if err != nil {
			Logger.Error().
				Err(err).
				Str("host", name).
				Msgf("Error draining pod %s", *pod.Metadata.Name)
			continue
		}
	}

	doneDraining := make(chan bool)

	// Wait until all pods are deleted
	go func() {
		for {
			sleepTime := ApplyJitter(10)
			sleepDuration := time.Duration(sleepTime) * time.Second
			pendingPodList, err := k.Client.CoreV1().ListPods(context.Background(), k8s.AllNamespaces, fieldSelector)

			if err != nil {
				Logger.Error().
					Err(err).
					Str("host", name).
					Msgf("Error getting list of pods, sleeping %ds", sleepTime)

				time.Sleep(sleepDuration)
				continue
			}

			// Filter out DaemonSet from the list of pods
			filteredPendingPodList := filterOutPodByOwnerReferenceKind(pendingPodList.Items, "DaemonSet")
			podsPending := len(filteredPendingPodList)

			if podsPending == 0 {
				doneDraining <- true
				return
			}

			Logger.Info().
				Str("host", name).
				Msgf("%d pod(s) pending deletion, sleeping %ds", podsPending, sleepTime)

			time.Sleep(sleepDuration)
		}
	}()

	select {
	case <-doneDraining:
		break
	case <-time.After(time.Duration(drainTimeout) * time.Second):
		Logger.Warn().
			Str("host", name).
			Msg("Draining node timeout reached")
		return
	}

	Logger.Info().
		Str("host", name).
		Msg("Done draining node")

	return
}

// loadK8sClient parses a kubeconfig from a file and returns a Kubernetes
// client. It does not support extensions or client auth providers.
func loadK8sClient(kubeconfigPath string) (*k8s.Client, error) {
	data, err := ioutil.ReadFile(kubeconfigPath)
	if err != nil {
		return nil, fmt.Errorf("Read kubeconfig error:\n%v", err)
	}

	// Unmarshal YAML into a Kubernetes config object.
	var config k8s.Config
	if err := yaml.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("Unmarshal kubeconfig error:\n%v", err)
	}

	// fmt.Printf("%#v", config)
	return k8s.NewClient(&config)
}
