FROM golang:1.20 as builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY *.go ./
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags '-extldflags "-static"' -tags timetzdata -o /ecowitt-data-prometheus-relay

FROM scratch
USER 1001
COPY --from=builder /ecowitt-data-prometheus-relay /
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY config-example.json /config.json
ENTRYPOINT ["/ecowitt-data-prometheus-relay"]
