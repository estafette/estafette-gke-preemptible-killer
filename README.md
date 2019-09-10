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

## Known limitations

- Pods in selected nodes are deleted, not evicted.
- Currently deletion time is based on node creation time, so if you deploy
  this tool when your instances have over 12h then you may experience a lot
  of nodes getting deleted at once.
- Selecting node pool is not supported yet, the code is processing ALL
  preemptible nodes attached to the cluster, and there is no way to limit it
  even via taints nor annotations
- This tool increases the chances to have many small disruptions instead of
  one major disruption.
- This tool does not guarantee that major disruption is avoided - GCP can
  trigger large disruption because the way preemptible instances are managed.
  Ensure your have PDB and enough of replicas, so for better safety just use
  non-preemptible nodes in different zones. You may also be interested in [estafette-gke-node-pool-shifter](https://github.com/estafette/estafette-gke-node-pool-shifter)

## Usage

You can either use environment variables or flags to configure the following settings:

| Environment variable   | Flag                     | Default  | Description
| ---------------------- | ------------------------ | -------- | -----------------------------------------------------------------
| BLACKLIST_HOURS        | --blacklist-hours        |          | List of UTC time intervals in the form of `09:00 - 12:00, 13:00 - 18:00` in which deletion is NOT allowed
| DRAIN_TIMEOUT          | --drain-timeout          | 300      | Max time in second to wait before deleting a node
| INTERVAL               | --interval (-i)          | 600      | Time in second to wait between each node check
| KUBECONFIG             | --kubeconfig             |          | Provide the path to the kube config path, usually located in ~/.kube/config. This argument is only needed if you're running the killer outside of your k8s cluster
| METRICS_LISTEN_ADDRESS | --metrics-listen-address | :9001    | The address to listen on for Prometheus metrics requests
| METRICS_PATH           | --metrics-path           | /metrics | The path to listen for Prometheus metrics requests
| WHITELIST_HOURS        | --whitelist-hours        |          | List of UTC time intervals in the form of `09:00 - 12:00, 13:00 - 18:00` in which deletion is allowed and preferred

### Create a Google Service Account

In order to have the estafette-gke-preemptible-killer instance delete nodes,
create a service account and give the _compute.instances.delete_ permissions.

You can either create the service account and associate the role using the
GCloud web console or the cli:

```bash
$ export project_id=<PROJECT>
$ gcloud iam --project=$project_id service-accounts create preemptible-killer \
    --display-name preemptible-killer
$ gcloud iam --project=$project_id roles create preemptible_killer \
    --project $project_id \
    --title preemptible-killer \
    --description "Delete compute instances" \
    --permissions compute.instances.delete
$ export service_account_email=$(gcloud iam --project=$project_id service-accounts list --filter preemptible-killer --format 'value([email])')
$ gcloud projects add-iam-policy-binding $project_id \
    --member=serviceAccount:${service_account_email} \
    --role=projects/${project_id}/roles/preemptible_killer
$ gcloud iam --project=$project_id service-accounts keys create \
    --iam-account $service_account_email \
    google_service_account.json
```

### Deploy with Helm

```bash
# Prepare Helm/Tiller
$ kubectl create sa tiller -n kube-system
$ helm init --service-account tiller
$ kubectl create clusterrolebinding tiller \
    --clusterrole=cluster-admin \
    --serviceaccount=kube-system:tiller

# Install
$ helm upgrade estafette-gke-preemptible-killer \
    --namespace estafette \
    --install \
    --set rbac.create=true \
    --set-file googleServiceAccount=./google_service_account.json \
    ./chart/estafette-gke-preemptible-killer
```

### Deploy without Helm

```bash
export NAMESPACE=estafette
export APP_NAME=estafette-gke-preemptible-killer
export TEAM_NAME=tooling
export VERSION=1.1.21
export GO_PIPELINE_LABEL=1.1.21
export GOOGLE_SERVICE_ACCOUNT=$(cat google_service_account.json | base64)
export DRAIN_TIMEOUT=300
export INTERVAL=600
export CPU_REQUEST=10m
export MEMORY_REQUEST=16Mi
export CPU_LIMIT=50m
export MEMORY_LIMIT=128Mi

# Setup RBAC
curl https://raw.githubusercontent.com/estafette/estafette-gke-preemptible-killer/master/rbac.yaml | envsubst | kubectl apply -n ${NAMESPACE} -f -

# Run application
curl https://raw.githubusercontent.com/estafette/estafette-gke-preemptible-killer/master/kubernetes.yaml | envsubst | kubectl apply -n ${NAMESPACE} -f -
```

### Deploy with Kustomize

Create a `kustomization.yaml` file:

```yaml
apiVersion: kustomize.config.k8s.io/v1beta1
kind: Kustomization
namespace: default
commonLabels:
  app: preemptible-killer
bases:
- github.com/coryodaniel/estafette-gke-preemptible-killer//manifests
images:
- name: estafette/estafette-gke-preemptible-killer
  newTag: 1.1.21
secretGenerator:
- name: preemptible-killer-secrets
  files:
  - google-service-account.json=google_service_account.json
  type: "Opaque"
```

Apply manifests:

```bash
kubectl apply -k .
```

## Development

To start development run

```bash
git clone git@github.com:estafette/estafette-ci-api.git
cd estafette-ci-api
```

Before committing your changes run

```bash
go test ./...
go mod tidy
```

### Testing

In order to test your local changes against an external Kubernetes cluster use the following commands:

```bash
# proxy master
kubectl proxy

# in another shell
go build && ./estafette-gke-preemptible-killer -i 10
```

Note: `KUBECONFIG=~/.kube/config` as environment variable can also be used if you don't want to use the `kubectl proxy`
command.

For an all-in-one script that launches a kind cluster with 3 nodes, runs
`estafette-gke-preemptible-killer` and then reports on the kill time, run:
```
go build && ./scripts/all-in-one-test -i 10
```
where `-i 10` are the arguments to be passed to
`estafette-gke-preemptible-killer`, replace with your own test arguments.
For safety, it does not remove the kind cluster it leaves behind.
