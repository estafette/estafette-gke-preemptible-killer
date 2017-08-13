package main

import (
	"context"
	"fmt"
	"net/http"

	"golang.org/x/oauth2/google"
	"google.golang.org/api/compute/v1"
)

type GCloud struct {
	Client    *http.Client
	ProjectID string
	Service   *compute.Service
	Zone      string
}

type GCloudClient interface {
	DeleteNode(string) error
}

// NewGCloudClient return a GCloud client
func NewGCloudClient(projectId string, zone string) (gcloud GCloudClient, err error) {
	client, err := google.DefaultClient(context.Background(), compute.ComputeScope)

	if err != nil {
		err = fmt.Errorf("Error creating compute client:\n%v", err)
		return
	}

	service, err := compute.New(client)

	if err != nil {
		err = fmt.Errorf("Error creating compute service:\n%v", err)
		return
	}

	gcloud = &GCloud{
		Client:    client,
		ProjectID: projectId,
		Service:   service,
		Zone:      zone,
	}

	return
}

// DeleteNode delete a GCloud instance from a given node name
func (g *GCloud) DeleteNode(name string) (err error) {
	_, err = g.Service.Instances.Delete(g.ProjectID, g.Zone, name).Context(context.Background()).Do()
	return
}
