# estafette-gke-preemptible-killer

This small Kubernetes application loop through a given preemptibles node pool and kill a node before the regular [24h
life time of a preemptible VM](https://cloud.google.com/compute/docs/instances/preemptible#limitations).

[![License](https://img.shields.io/github/license/estafette/estafette-gke-preemptible-killer.svg)](https://github.com/estafette/estafette-gke-preemptible-killer/blob/master/LICENSE)


## Why?

When creating a cluster, all the node are created at the same time and should be deleted after 24h of activity. To
prevent large disruption, the estafette-gke-preemptible-killer can be used to kill instances during a random period
of time between 12 and 24h. It makes use of the node annotation to store the time to kill value.


## How does that work

At a given interval, the application get the list of preemptible nodes and check weither the node should be
deleted or not. If the annotation doesn't exist, a time to kill value is added to the node annotation with a
random range between 12h and 24h based on the node creation time stamp.
When the time to kill time is passed, the Kubernetes node is marked as unschedulable, drained and the instance
deleted on GCloud.


## Usage

You can either use environment variables or flags to configure the following settings:

| Environment variable   | Flag                     | Default  | Description
| ---------------------- | ------------------------ | -------- | -----------------------------------------------------------------
| DRAIN_TIMEOUT          | --drain-timeout          | 300      | Max time in second to wait before deleting a node
| INTERVAL               | --interval (-i)          | 120      | Time in second to wait between each node check
| KUBECONFIG             | --kubeconfig             |          | Provide the path to the kube config path, usually located in ~/.kube/config. For out of cluster execution
| METRICS_LISTEN_ADDRESS | --metrics-listen-address | :9001    | The address to listen on for Prometheus metrics requests
| METRICS_PATH           | --metrics-path           | /metrics | The path to listen for Prometheus metrics requests


### In cluster

As a Kubernetes administrator, you first need to deploy the rbac.yaml file which set role and permissions.
Then deploy the application to Kubernetes cluster using the manifest below.


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
            port: 9001
          initialDelaySeconds: 30
          timeoutSeconds: 1
```

### Local development

```
# proxy master
kubectl proxy

# in another shell
go build && ./estafette-gke-preemptible-killer
```

Note: `KUBECONFIG=~/.kube/config` as environment variable can also be used if you don't want to use the `kubectl proxy`
command.
