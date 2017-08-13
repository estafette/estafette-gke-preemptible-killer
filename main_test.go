package main

import (
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/ericchiang/k8s"
	apiv1 "github.com/ericchiang/k8s/api/v1"
	metav1 "github.com/ericchiang/k8s/apis/meta/v1"
)

type FakeKubernetes struct {
}

func FakeNewKubernetesClient() KubernetesClient {
	return &FakeKubernetes{}
}

func (k *FakeKubernetes) GetProjectIdAndZoneFromNode(name string) (string, string, error) {
	return "", "", nil
}

func (k *FakeKubernetes) DrainNode(nodepool string, drainTimeout int) error {
	return nil
}

func (k *FakeKubernetes) GetNode(name string) (*apiv1.Node, error) {
	return &apiv1.Node{}, nil
}

func (k *FakeKubernetes) SetNodeAnnotation(name string, key string, value string) error {
	return nil
}
func (k *FakeKubernetes) SetUnschedulableState(name string, unschedulable bool) error {
	return nil
}

func (k *FakeKubernetes) GetPreemptibleNodes() (*apiv1.NodeList, error) {
	return &apiv1.NodeList{}, nil
}

func TestGetCurrentNodeState(t *testing.T) {
	zerolog.SetGlobalLevel(zerolog.Disabled)

	node := &apiv1.Node{
		Metadata: &metav1.ObjectMeta{
			Name: k8s.String("node-1"),
			Annotations: map[string]string{
				"estafette.io/gke-preemptible-killer-state": "2017-11-11T11:11:11Z",
			},
		},
	}

	state := getCurrentNodeState(node)

	if state.ExpiryDatetime != "2017-11-11T11:11:11Z" {
		t.Errorf("Expect expiry date time to be 2017-11-11T11:11:11Z, instead got %s", state.ExpiryDatetime)
	}
}

func TestGetDesiredNodeState(t *testing.T) {
	zerolog.SetGlobalLevel(zerolog.Disabled)

	creationTimestamp := time.Date(2017, 11, 11, 11, 11, 11, 0, time.UTC).Unix()

	node := &apiv1.Node{
		Metadata: &metav1.ObjectMeta{
			Name:              k8s.String("node-1"),
			CreationTimestamp: &metav1.Time{Seconds: &creationTimestamp},
		},
	}

	client := FakeNewKubernetesClient()

	state, _ := getDesiredNodeState(client, node)

	if state.ExpiryDatetime != "2017-11-12T04:11:11Z" {
		t.Errorf("Expect expiry date time to be 2017-11-12T04:11:11Z, instead got %s", state.ExpiryDatetime)
	}
}
