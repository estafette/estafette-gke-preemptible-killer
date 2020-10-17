FROM golang:1.14-buster AS build

ENV GOBIN=$GOPATH/bin

ADD . /src/estafette-gke-preemptible-killer

WORKDIR /src/estafette-gke-preemptible-killer

RUN apt-get -qqq update \
    && apt-get -qqq -y install ca-certificates\
    && update-ca-certificates

RUN go mod download \
    && go build ./...

FROM debian:buster-slim

LABEL maintainer="estafette.io" \
      description="The estafette-gke-preemptible-killer component is a Kubernetes controller that ensures preemptible nodes in a Container Engine cluster don't expire at the same time"

COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=build /src/estafette-gke-preemptible-killer/estafette-gke-preemptible-killer /estafette-gke-preemptible-killer

ENTRYPOINT ["/estafette-gke-preemptible-killer"]

