package main

import (
	"context"
	"testing"
	"time"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	gomock "github.com/golang/mock/gomock"
	"github.com/rs/zerolog"
)

func TestGetCurrentNodeState(t *testing.T) {
	zerolog.SetGlobalLevel(zerolog.Disabled)

	node := v1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "node-1",
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

func TestGetDesiredNodeState_BetweenTwelveAndTwentyFour(t *testing.T) {
	zerolog.SetGlobalLevel(zerolog.Disabled)

	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	ctx := context.Background()

	creationTimestamp := time.Date(2017, 11, 11, 12, 00, 00, 0, time.UTC)
	certainlyDeadBy := creationTimestamp.Add(24 * time.Hour).Add(1 * time.Minute)
	twelveAfterCreation := creationTimestamp.Add(12 * time.Hour)
	now := creationTimestamp.Add(4 * time.Hour)

	node := v1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "node-1",
			CreationTimestamp: metav1.Time{Time: creationTimestamp},
		},
	}

	client := NewMockKubernetesClient(ctrl)
	client.EXPECT().SetNodeAnnotation(gomock.Any(), "node-1", "estafette.io/gke-preemptible-killer-state", gomock.Any()).AnyTimes()

	whitelistInstance.parseArguments()
	state, _ := getDesiredNodeState(now, ctx, client, node)
	stateTS, _ := time.Parse(time.RFC3339, state.ExpiryDatetime)

	if stateTS.Before(now) && !stateTS.Before(certainlyDeadBy) && !stateTS.After(twelveAfterCreation) {
		t.Errorf("Expect expiry date time to be between 12 and 24h after the creation date %s, instead got %s", creationTimestamp, state.ExpiryDatetime)
	}

	now = creationTimestamp.Add(20 * time.Hour)

	state, _ = getDesiredNodeState(now, ctx, client, node)
	stateTS, _ = time.Parse(time.RFC3339, state.ExpiryDatetime)

	if stateTS.Before(now) && !stateTS.Before(certainlyDeadBy) {
		t.Errorf("Expect expiry date time to be between 12 and 24h after the creation date, but not before now %s, instead got %s", creationTimestamp, state.ExpiryDatetime)
	}
}

func TestGetDesiredNodeState_NotBeforeNow(t *testing.T) {
	zerolog.SetGlobalLevel(zerolog.Disabled)

	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	ctx := context.Background()

	creationTimestamp := time.Date(2017, 11, 11, 12, 00, 00, 0, time.UTC)
	certainlyDeadBy := creationTimestamp.Add(24 * time.Hour).Add(1 * time.Minute)

	//We will expect between a 0/4 hour offset from now
	now := creationTimestamp.Add(20 * time.Hour)

	node := v1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "node-1",
			CreationTimestamp: metav1.Time{Time: creationTimestamp},
		},
	}

	client := NewMockKubernetesClient(ctrl)
	client.EXPECT().SetNodeAnnotation(gomock.Any(), "node-1", "estafette.io/gke-preemptible-killer-state", gomock.Any()).AnyTimes()

	whitelistInstance.parseArguments()
	state, _ := getDesiredNodeState(now, ctx, client, node)
	stateTS, _ := time.Parse(time.RFC3339, state.ExpiryDatetime)

	if stateTS.Before(now) && !stateTS.Before(certainlyDeadBy) {
		t.Errorf("Expect expiry date time should not be before now %s, instead got %s", creationTimestamp, state.ExpiryDatetime)
	}
}
