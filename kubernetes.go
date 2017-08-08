package main

import (
	"context"
	"fmt"
	"io/ioutil"
	"log"
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
	GetPreemptibleNodes() (*apiv1.NodeList, error)
	SetNodeAnnotation(*apiv1.Node, string, string) (*apiv1.Node, error)
	SetSchedulableState(*apiv1.Node, bool) (*apiv1.Node, error)
}

// NewKubernetesClient return a Kubernetes client
func NewKubernetesClient(host string, port string, namespace string, kubeConfigPath string) (kubernetes *Kubernetes, err error) {
	var k8sClient *k8s.Client

	if len(host) > 0 && len(port) > 0 {
		k8sClient, err = k8s.NewInClusterClient()

		if err != nil {
			err = fmt.Errorf("Error loading incluster client: %v", err)
			return
		}
	} else if len(kubeConfigPath) > 0 {
		k8sClient, err = loadK8sClient(kubeConfigPath)

		if err != nil {
			err = fmt.Errorf("Error loading client using kubeconfig: %v", err)
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

// SetNodeAnnotation add an annotation (key/value) to a given node
func (k *Kubernetes) SetNodeAnnotation(node *apiv1.Node, key string, value string) (err error) {
	node.Metadata.Annotations[key] = value
	_, err = k.Client.CoreV1().UpdateNode(context.Background(), node)
	return
}

// SetUnschedulableState set the unschedulable state of a given node
func (k *Kubernetes) SetUnschedulableState(name string, unschedulable bool) (err error) {
	node, err := k.GetNode(name)

	if err != nil {
		err = fmt.Errorf("[%s] Error getting node information before setting unschedulable state:", name, err)
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
func (k *Kubernetes) DrainNode(name string) (err error) {
	// Select all pods sitting on the node except the one from kube-system
	fieldSelector := k8s.QueryParam("fieldSelector", "spec.nodeName="+name+",metadata.namespace!=kube-system")

	podList, err := k.Client.CoreV1().ListPods(context.Background(), k8s.AllNamespaces, fieldSelector)

	// Filter out DaemonSet from the list of pods
	filteredPodList := filterOutPodByOwnerReferenceKind(podList.Items, "DaemonSet")

	if err != nil {
		return
	}

	log.Printf("[%s] %d pod(s) found", name, len(filteredPodList))

	for _, pod := range podList.Items {
		err = k.Client.CoreV1().DeletePod(context.Background(), *pod.Metadata.Name, *pod.Metadata.Namespace)

		if err != nil {
			log.Printf("[%s] Error draining pod %s", name, *pod.Metadata.Name)
			continue
		}

		log.Printf("[%s] Deleting pod %s", name, *pod.Metadata.Name)
	}

	doneDraining := make(chan bool)

	// Wait until all pods are deleted
	go func() {
		for {
			sleepTime := ApplyJitter(10)
			sleepDuration := time.Duration(sleepTime) * time.Second
			pendingPodList, err := k.Client.CoreV1().ListPods(context.Background(), k8s.AllNamespaces, fieldSelector)

			if err != nil {
				log.Printf("[%s] Error getting list of pods, sleeping %ds", name, sleepTime)
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

			log.Printf("[%s] %d pod(s) pending deletion, sleeping %ds", name, podsPending, sleepTime)
			time.Sleep(sleepDuration)
		}
	}()

	select {
	case <-doneDraining:
		break
	case <-time.After(time.Duration(*DrainNodeTimeout) * time.Second):
		log.Printf("[%s] Draining node timeout reached", name)
		return
	}

	log.Printf("[%s] Done draining node", name)

	return
}

// loadK8sClient parses a kubeconfig from a file and returns a Kubernetes
// client. It does not support extensions or client auth providers.
func loadK8sClient(kubeconfigPath string) (*k8s.Client, error) {
	data, err := ioutil.ReadFile(kubeconfigPath)
	if err != nil {
		return nil, fmt.Errorf("Read kubeconfig error: %v", err)
	}

	// Unmarshal YAML into a Kubernetes config object.
	var config k8s.Config
	if err := yaml.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("Unmarshal kubeconfig error: %v", err)
	}

	// fmt.Printf("%#v", config)
	return k8s.NewClient(&config)
}
