package main

import (
	"testing"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestFilterOutPodByOwnerReferenceKind(t *testing.T) {
	podList := []v1.Pod{
		{
			ObjectMeta: metav1.ObjectMeta{
				Name: "node-1",
				OwnerReferences: []metav1.OwnerReference{
					{
						Kind: "DaemonSet",
						Name: "daemon-set",
					},
				},
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{
				Name: "node-2",
				OwnerReferences: []metav1.OwnerReference{
					{
						Kind: "ReplicaSet",
						Name: "replica-set",
					},
				},
			},
		},
	}

	filteredPodList := filterOutPodByOwnerReferenceKind(podList, "DaemonSet")

	if len(filteredPodList) != 1 {
		t.Errorf("Expect pod list to have 1 item, instead got %d", len(filteredPodList))
	}

	if filteredPodList[0].ObjectMeta.Name != "node-2" {
		t.Errorf("Expect first item name to be 'node-2', instead got %s", filteredPodList[0].ObjectMeta.Name)
	}
}
