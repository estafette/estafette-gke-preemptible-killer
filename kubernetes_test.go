package main

import (
	"testing"

	"github.com/ericchiang/k8s"
	apiv1 "github.com/ericchiang/k8s/api/v1"
	metav1 "github.com/ericchiang/k8s/apis/meta/v1"
)

func TestFilterOutPodByOwnerReferenceKind(t *testing.T) {
	podList := []*apiv1.Pod{
		{
			Metadata: &metav1.ObjectMeta{
				Name: k8s.String("node-1"),
				OwnerReferences: []*metav1.OwnerReference{
					{
						Kind: k8s.String("DaemonSet"),
						Name: k8s.String("daemon-set"),
					},
				},
			},
		},
		{
			Metadata: &metav1.ObjectMeta{
				Name: k8s.String("node-2"),
				OwnerReferences: []*metav1.OwnerReference{
					{
						Kind: k8s.String("ReplicaSet"),
						Name: k8s.String("replica-set"),
					},
				},
			},
		},
	}

	filteredPodList := filterOutPodByOwnerReferenceKind(podList, "DaemonSet")

	if len(filteredPodList) != 1 {
		t.Errorf("Expect pod list to have 1 item, instead got %d", len(filteredPodList))
	}

	if *filteredPodList[0].Metadata.Name != "node-2" {
		t.Errorf("Expect first item name to be 'node-2', instead got %s", *filteredPodList[0].Metadata.Name)
	}
}
