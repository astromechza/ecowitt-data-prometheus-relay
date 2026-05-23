FROM gcr.io/distroless/static-debian12:nonroot
COPY ecowitt-data-prometheus-relay /usr/local/bin/ecowitt-data-prometheus-relay
COPY config-example.json /config.json
ENTRYPOINT ["/usr/local/bin/ecowitt-data-prometheus-relay"]
