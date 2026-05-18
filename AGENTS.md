# Agent Notes

## Project overview

This is a Go HTTP service that receives Ecowitt weather station data POSTs and exposes them as Prometheus metrics. Single `main.go`, tested in `main_test.go`.

## Key design decisions

- **Opt-out field filtering**: all URL-encoded fields become gauges except a known drop-list (`PASSKEY`, `dateutc`, `model`, `stationtype`, `freq`).
- **Dynamic gauge registration**: gauges are registered on first receipt using `prometheus.AlreadyRegisteredError` to retrieve existing collectors on subsequent reports.
- **Labels**: `model`, `stationType`, `source_ip` on every metric.
- **`/last` endpoint**: returns the verbatim body of the most recent station POST for debugging. `Authorization` header is stripped before replay. Intended for trusted-network use only.

## Protocol documentation

See [`docs/internal/ecowitt-protocol.md`](docs/internal/ecowitt-protocol.md) for:
- Full HTTP protocol spec and known quirks
- Complete field reference with units and semantics
- Real sample payload from a live GW1100A device
- Battery field semantics (`wh65batt`, `batt2` — boolean flags, not voltages)
- Device model reference

## Test data

`testdata/live-metrics-sample.txt` — snapshot of `/metrics` output from the live GW1100A deployment (v0.0.9, 2026-05-18).

## Open questions / future work

- Whether to drop device diagnostic fields (`runtime`, `heap`, `interval`) from metrics
- Better Prometheus naming (units in suffix, remove `_raw`)
- Battery fields are boolean — consider emitting as separate named gauge rather than generic `_raw`
- `baromrelin` vs `baromabsin` — only one is meaningful when altitude correction is zero

## Lint / build

```bash
golangci-lint run   # config in .golangci.yml
go test ./...
go build ./...
```
