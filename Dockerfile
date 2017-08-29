FROM scratch

LABEL maintainer="estafette.io" \
      description="The estafette-gke-preemptible-killer component is a Kubernetes controller that ensures preemptible nodes in a Container Engine cluster don't expire at the same time"

COPY ca-certificates.crt /etc/ssl/certs/
COPY estafette-gke-preemptible-killer /

CMD ["./estafette-gke-preemptible-killer"]
