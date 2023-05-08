FROM golang:1.20 as builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY *.go ./
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags '-extldflags "-static"' -tags timetzdata -o /ovo-energy-prometheus-gauge

FROM scratch
USER 1001
COPY --from=builder /ovo-energy-prometheus-gauge /
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
ENTRYPOINT ["/ovo-energy-prometheus-gauge"]
