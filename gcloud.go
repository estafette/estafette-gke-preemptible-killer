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
	Context   context.Context
	ProjectID string
	Service   *compute.Service
	Zone      string
}

type GCloudClient interface {
	DeleteNode(string) error
}

// NewGCloudClient return a GCloud client
func NewGCloudClient(projectId string, zone string) (gcloud *GCloud, err error) {
	gcloud = &GCloud{
		Context:   context.Background(),
		ProjectID: projectId,
		Zone:      zone,
	}

	client, err := google.DefaultClient(gcloud.Context, compute.ComputeScope)

	if err != nil {
		err = fmt.Errorf("Error creating compute client:\n%v", err)
		return
	}

	gcloud.Client = client

	service, err := compute.New(client)

	if err != nil {
		err = fmt.Errorf("Error creating compute service:\n%v", err)
		return
	}

	gcloud.Service = service

	return
}

// DeleteNode delete a GCloud instance from a given node name
func (g *GCloud) DeleteNode(name string) (err error) {
	_, err = g.Service.Instances.Delete(g.ProjectID, g.Zone, name).Context(g.Context).Do()
	return
}
