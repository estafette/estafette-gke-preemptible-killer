package main

import (
	"math/rand"
	"testing"
	"time"

	"github.com/ericchiang/k8s"
	apiv1 "github.com/ericchiang/k8s/api/v1"
	metav1 "github.com/ericchiang/k8s/apis/meta/v1"
	"github.com/rs/zerolog"
)

type FakeKubernetes struct {
}

func FakeNewKubernetesClient() KubernetesClient {
	return &FakeKubernetes{}
}

func (k *FakeKubernetes) GetProjectIdAndZoneFromNode(name string) (string, string, error) {
	return "", "", nil
}

func (k *FakeKubernetes) DrainNode(node string, drainTimeout int) error {
	return nil
}

func (k *FakeKubernetes) DrainKubeDNSFromNode(node string, drainTimeout int) error {
	return nil
}

func (k *FakeKubernetes) GetNode(name string) (*apiv1.Node, error) {
	return &apiv1.Node{}, nil
}

func (k *FakeKubernetes) DeleteNode(name string) error {
	return nil
}

func (k *FakeKubernetes) SetNodeAnnotation(name string, key string, value string) error {
	return nil
}
func (k *FakeKubernetes) SetUnschedulableState(name string, unschedulable bool) error {
	return nil
}

func (k *FakeKubernetes) GetPreemptibleNodes(map[string]string) (*apiv1.NodeList, error) {
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

	creationTimestamp := time.Date(2017, 11, 11, 12, 00, 00, 0, time.UTC)
	creationTimestampUnix := creationTimestamp.Unix()
	creationTimestamp12HoursLater := creationTimestamp.Add(12 * time.Hour)
	creationTimestamp24HoursLater := creationTimestamp.Add(24 * time.Hour)

	node := &apiv1.Node{
		Metadata: &metav1.ObjectMeta{
			Name:              k8s.String("node-1"),
			CreationTimestamp: &metav1.Time{Seconds: &creationTimestampUnix},
		},
	}

	client := FakeNewKubernetesClient()

	whitelistInstance.parseArguments()
	state, _ := getDesiredNodeState(client, node)
	stateTS, _ := time.Parse(time.RFC3339, state.ExpiryDatetime)

	if !creationTimestamp12HoursLater.Before(stateTS) && !creationTimestamp24HoursLater.After(stateTS) {
		t.Errorf("Expect expiry date time to be between 12 and 24h after the creation date %s, instead got %s", creationTimestamp, state.ExpiryDatetime)
	}

	randomEstafette = rand.New(rand.NewSource(0))
	stateWithPreseed, _ := getDesiredNodeState(client, node)
	if stateWithPreseed.ExpiryDatetime != "2017-11-12T11:27:54Z" {
		t.Errorf("Expect expiry date time to be 2017-11-12T11:27:54Z, instead got %s", stateWithPreseed.ExpiryDatetime)
	}
}
