FROM scratch

MAINTAINER estafette.io

COPY ca-certificates.crt /etc/ssl/certs/
COPY estafette-gke-preemptible-killer

CMD ["./estafette-gke-preemptible-killer"]
