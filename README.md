# estafette-gke-preemptible-killer

This small Kubernetes application loop through a given preemptibles node pool and kill a node before the regular [24h
life time of a preemptible VM](https://cloud.google.com/compute/docs/instances/preemptible#limitations).

[![License](https://img.shields.io/github/license/estafette/estafette-gke-preemptible-killer.svg)](https://github.com/estafette/estafette-gke-preemptible-killer/blob/master/LICENSE)


## Why?

When creating a cluster, all the node are created at the same time and should be deleted after 24h of activity. To
prevent large disruption, the estafette-gke-preemptible-killer can be used to kill instances during a random period
of time between 12 and 24h. It make use of the node annotation to store the time to kill value.


## How does that work

At a given interval, the application get the list of preemptible nodes and check weither the node should be
deleted or not. If the annotation doesn't exist, we define a new time to kill annotation with a random range
between 12h and 24h based on the node creation time stamp.
When the time to kill time is passed, the Kubernetes node is marked as unschedulable, drained and the instance
deleted on GCloud.


## Usage

Available flags:

- drain-node-timeout=300 max time in second to wait before deleting a node
- shutdown-listen-address=:8080 the address to listen on for graceful shutdown
- shutdown-timeout=120 max time in second to wait before shutting down the application
- prometheus-listen-address=:9109 the address to listen on for HTTP requests
- watch-interval=120 time in second to wait between each node check


### In cluster

First deploy the application to Kubernetes cluster using the manifest below.


```yaml
apiVersion: v1
kind: Namespace
metadata:
  name: estafette
---
apiVersion: extensions/v1beta1
kind: Deployment
metadata:
  name: estafette-gke-preemptible-killer
  namespace: estafette
  labels:
    app: estafette-gke-preemptible-killer
spec:
  replicas: 1
  strategy:
    type: Recreate
  selector:
    matchLabels:
      app: estafette-gke-preemptible-killer
  template:
    metadata:
      labels:
        app: estafette-gke-preemptible-killer
    spec:
      containers:
      - name: estafette-gke-preemptible-killer
        image: estafette/estafette-gke-preemptible-killer:latest
        resources:
          requests:
            cpu: 10m
            memory: 16Mi
          limits:
            cpu: 50m
            memory: 128Mi
        livenessProbe:
          httpGet:
            path: /metrics
            port: 9101
          initialDelaySeconds: 30
          timeoutSeconds: 1
        lifecycle:
          preStop:
            exec:
              command: ["curl http://localhost:8080/quit"]
```

### Local development

```
# proxy master
kubectl proxy

# in another shell
go build && ./estafette-gke-preemptible-killer
```

Note: KUBECONFIG=~/.kube/config as environment variable can also be used if you don't want to use the `kubectl proxy` command.
