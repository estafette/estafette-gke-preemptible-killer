FROM golang:1.15.2-alpine3.12 AS build

ENV GOBIN=$GOPATH/bin

ENV CGO_ENABLED="0" \
    GOOS="linux"

ADD . /src/estafette-gke-preemptible-killer

WORKDIR /src/estafette-gke-preemptible-killer

RUN apk update && apk add ca-certificates && rm -rf /var/cache/apk/*
RUN update-ca-certificates

RUN go test ./... \
    && go build -a -installsuffix cgo -ldflags "-X main.version=${ESTAFETTE_BUILD_VERSION} -X main.revision=${ESTAFETTE_GIT_REVISION} -X main.branch=${ESTAFETTE_GIT_BRANCH} -X main.buildDate=${ESTAFETTE_BUILD_DATETIME}" .


FROM debian:buster-slim

LABEL maintainer="estafette.io" \
      description="The estafette-gke-preemptible-killer component is a Kubernetes controller that ensures preemptible nodes in a Container Engine cluster don't expire at the same time"

COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=build /src/estafette-gke-preemptible-killer/estafette-gke-preemptible-killer /estafette-gke-preemptible-killer

ENTRYPOINT ["/estafette-gke-preemptible-killer"]

