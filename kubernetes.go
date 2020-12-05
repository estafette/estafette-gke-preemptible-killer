package main

import (
	"context"
	"fmt"
	"strings"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8s "k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"

	_ "k8s.io/client-go/plugin/pkg/client/auth/gcp"
)

type Kubernetes struct {
	Client *k8s.Clientset
}

type KubernetesClient interface {
	DrainNode(string, int) error
	DrainKubeDNSFromNode(string, int) error
	GetNode(string) (*v1.Node, error)
	DeleteNode(string) error
	GetPreemptibleNodes(map[string]string) (*v1.NodeList, error)
	GetProjectIdAndZoneFromNode(string) (string, string, error)
	SetNodeAnnotation(string, string, string) error
	SetUnschedulableState(string, bool) error
}

// NewKubernetesClient return a Kubernetes client
func NewKubernetesClient(host string, port string, namespace string, kubeConfigPath string) (kubernetes KubernetesClient, err error) {
	var config *rest.Config
	var k8sClient *k8s.Clientset

	if len(host) > 0 && len(port) > 0 {
		config, err = rest.InClusterConfig()
	} else if len(kubeConfigPath) > 0 {
		config, err = clientcmd.BuildConfigFromFlags("", kubeConfigPath)
	}

	if err != nil {
		err = fmt.Errorf("Error loading in cluster client configuration:\n%v", err)
		return
	}

	k8sClient, err = k8s.NewForConfig(config)

	if err != nil {
		err = fmt.Errorf("Error loading client:\n%v", err)
		return
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

	s := strings.Split(node.Spec.ProviderID, "/")
	projectId = s[2]
	zone = s[3]

	return
}

// GetPreemptibleNodes return a list of preemptible node
func (k *Kubernetes) GetPreemptibleNodes(filters map[string]string) (nodes *v1.NodeList, err error) {
	return k.Client.CoreV1().Nodes().List(context.Background(), metav1.ListOptions{LabelSelector: "cloud.google.com/gke-preemptible=true"})
}

// GetNode return the node object from given name
func (k *Kubernetes) GetNode(name string) (node *v1.Node, err error) {
	return k.Client.CoreV1().Nodes().Get(context.Background(), name, metav1.GetOptions{})
}

func (k *Kubernetes) DeleteNode(name string) (err error) {
	return k.Client.CoreV1().Nodes().Delete(context.Background(), name, metav1.DeleteOptions{})
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

	newNode.ObjectMeta.Annotations[key] = value

	_, err = k.Client.CoreV1().Nodes().Update(context.Background(), newNode, metav1.UpdateOptions{})
	return err
}

// SetUnschedulableState set the unschedulable state of a given node name
func (k *Kubernetes) SetUnschedulableState(name string, unschedulable bool) (err error) {
	node, err := k.GetNode(name)

	if err != nil {
		err = fmt.Errorf("Error getting node information before setting unschedulable state:\n%v", err)
		return
	}

	node.Spec.Unschedulable = unschedulable

	_, err = k.Client.CoreV1().Nodes().Update(context.Background(), node, metav1.UpdateOptions{})
	return err
}

// filterOutPodByOwnerReferenceKind filter out a list of pods by its owner references kind
func filterOutPodByOwnerReferenceKind(podList []*v1.Pod, kind string) (output []*v1.Pod) {
	for _, pod := range podList {
		for _, ownerReference := range pod.ObjectMeta.OwnerReferences {
			if ownerReference.Kind != kind {
				output = append(output, pod)
			}
		}
	}

	return
}

// filterOutPodByNode filters out a list of pods by its node
func filterOutPodByNode(podList []*v1.Pod, nodeName string) (output []*v1.Pod) {
	for _, pod := range podList {
		if pod.Spec.NodeName == nodeName {
			output = append(output, pod)
		}
	}

	return
}

// DrainNode delete every pods from a given node and wait that all pods are removed before it succeed
// it also make sure we don't select DaemonSet because they are not subject to unschedulable state
func (k *Kubernetes) DrainNode(name string, drainTimeout int) (err error) {
	//var drainer   *drain.Helper
	return nil
}

// DrainKubeDNSFromNode deletes any kube-dns pods running on the node
func (k *Kubernetes) DrainKubeDNSFromNode(name string, drainTimeout int) (err error) {
	return nil
}
