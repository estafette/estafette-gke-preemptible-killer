package main

import (
	"context"
	"fmt"
	"io/ioutil"
	"net/http"
	"strings"

	"github.com/ericchiang/k8s"
	apiv1 "github.com/ericchiang/k8s/api/v1"
	"github.com/ghodss/yaml"
)

type Kubernetes struct {
	Client *k8s.Client
}

type KubernetesClient interface {
	DeleteNode(string)
	GetPreemptibleNodes() (*apiv1.NodeList, error)
	PreemptibleNodeLabel() k8s.Option
	SetNodeAnnotation(*apiv1.Node, string, string) (*apiv1.Node, error)
	SetSchedulableState(*apiv1.Node, bool) (*apiv1.Node, error)
	WatchPreemptibleNodes() (*k8s.CoreV1NodeWatcher, error)
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

// PreemptibleNodeLabel return a labels selector for a preemptible node pool
func (k *Kubernetes) PreemptibleNodeLabel() k8s.Option {
	labels := new(k8s.LabelSelector)
	labels.Eq("cloud.google.com/gke-preemptible", "true")
	return labels.Selector()
}

// GetPreemptibleNodes return a list of preemptible node from a given node pool name
func (k *Kubernetes) GetPreemptibleNodes() (nodes *apiv1.NodeList, err error) {
	nodes, err = k.Client.CoreV1().ListNodes(context.Background(), k.PreemptibleNodeLabel())
	return
}

// GetNode return the node object from given name
func (k *Kubernetes) GetNode(name string) (node *apiv1.Node, err error) {
	node, err = k.Client.CoreV1().GetNode(context.Background(), name)
	return
}

// SetNodeAnnotation add an annotation (key/value) to a given node
func (k *Kubernetes) SetNodeAnnotation(node *apiv1.Node, annotationKey string, annotationValue string) (err error) {
	node.Metadata.Annotations[annotationKey] = annotationValue
	_, err = k.Client.CoreV1().UpdateNode(context.Background(), node)
	return
}

// SetSchedulableState set the schedulable state of a given node
func (k *Kubernetes) SetSchedulableState(name string, schedulable bool) (err error) {
	node, err := k.GetNode(name)
	node.Spec.Unschedulable = &schedulable
	_, err = k.Client.CoreV1().UpdateNode(context.Background(), node)
	return
}

// DeleteNode delete a node from a given name
func (k *Kubernetes) DeleteNode(name string) (err error) {
	err = k.Client.CoreV1().DeleteNode(context.Background(), name)
	return
}

// WatchNodes watch for updated preemptible node from a given node pool
func (k *Kubernetes) WatchPreemptibleNodes() (watcher *k8s.CoreV1NodeWatcher, err error) {
	watcher, err = k.Client.CoreV1().WatchNodes(context.Background(), k.PreemptibleNodeLabel())
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
