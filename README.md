# estafette-gke-preemptible-killer

This small Kubernetes application loop through a given preemptibles node pool and kill a node before the regular [24h
life time of a preemptible VM](https://cloud.google.com/compute/docs/instances/preemptible#limitations).

[![License](https://img.shields.io/github/license/estafette/estafette-gke-preemptible-killer.svg)](https://github.com/estafette/estafette-gke-preemptible-killer/blob/master/LICENSE)


## Why?

When creating a cluster, all the node are created at the same time and should be deleted after 24h of activity. To
prevent large disruption, the estafette-gke-preemptible-killer can be used to kill instances during a random period
of time between 12 and 24h. It make use of the node annotation to store the time to kill value.


## Usage

### In cluster

First deploy the application to Kubernetes cluster using the manifest below. Make sure to pass the following
mandatory environment variable:

- GOOGLE_PROJECT_ID: id of the Google project
- GOOGLE_INSTANCE_ZONE: [zone](https://cloud.google.com/compute/docs/regions-zones/regions-zones#available) where the node pool is hosted
- NODE_POOL: name of the node pool

Optional variables for out of cluster usage:

- KUBERNETES_SERVICE_HOST: Kubernetes service host (out of cluster use)
- KUBERNETES_SERVICE_PORT: Kubernetes service port (out of cluster use)
- KUBECONFIG: Kubernetes KubeConfig path (out of cluster use)


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
        env:
        - name: "GOOGLE_PROJECT_ID"
          value: "my-project-id"
        - name: "GOOGLE_INSTANCE_ZONE"
          value: "europe-west1-c"
        - name: "NODE_POOL"
          value: "node-pool-name"
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
```


### Local development

```
# proxy master
kubectl proxy

# in another shell
export GOOGLE_INSTANCE_ZONE=europe-west1-c
export GOOGLE_PROJECT_ID=my-project-id
export NODE_POOL=node-pool-name

go run main.go
```
