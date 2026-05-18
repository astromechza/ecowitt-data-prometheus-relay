# Ecowitt Upload Protocol Reference

## Protocol

The Ecowitt gateway POSTs sensor data to a configurable HTTP endpoint at a regular interval.

- **Method:** `POST`
- **Path:** `/data/report/` (trailing slash required — gateway sends exactly this path)
- **Content-Type:** `application/x-www-form-urlencoded`
- **Encoding:** Standard URL-encoded form data
- **Interval:** Configurable; default 60s (reported in the `interval` field)

### Known quirks

- Gateway firmware adds trailing whitespace to the `Content-Length` header value on some versions.
- `dateutc` uses `+` for spaces (`2026-05-18+16:32:04`), not `%20`.

---

## Sample Report

Captured from a live GW1100A device (firmware V2.4.5) on 2026-05-18:

```
PASSKEY=xxxxxxxxxxx&stationtype=GW1100A_V2.4.5&runtime=3728&heap=24412
&dateutc=2026-05-18+16:32:04&tempinf=80.42&humidityin=41
&baromrelin=29.394&baromabsin=29.394&tempf=51.08&humidity=93&vpd=0.026
&winddir=125&winddir_avg10m=171&windspeedmph=0.67&windgustmph=3.36
&maxdailygust=8.05&solarradiation=127.25&uv=1&rainratein=0.071
&eventrainin=0.051&hourlyrainin=0.039&last24hrainin=0.051
&dailyrainin=0.079&weeklyrainin=0.169&monthlyrainin=1.480
&yearlyrainin=15.571&totalrainin=15.571&temp2f=72.32&humidity2=52
&wh65batt=0&batt2=0&freq=868M&model=GW1100A&interval=60
```

---

## Field Reference

### Metadata / dropped fields

| Field | Dropped | Notes |
|---|---|---|
| `PASSKEY` | yes | MAC-derived auth token |
| `stationtype` | no — label | Model + firmware (e.g. `GW1100A_V2.4.5`) |
| `model` | no — label | Base model (e.g. `GW1100A`) |
| `dateutc` | yes | UTC timestamp; use scrape time instead |
| `freq` | yes | RF band (`868M` EU, `915M` US) |
| `interval` | device diagnostic | Reporting cadence in seconds |

### Device diagnostics (not weather data)

| Field | Unit | Notes |
|---|---|---|
| `runtime` | seconds | Gateway uptime since last restart |
| `heap` | bytes | Free heap memory on the gateway MCU |
| `interval` | seconds | Reporting interval |

### Indoor sensors (built into GW1100 gateway)

| Field | Unit | Notes |
|---|---|---|
| `tempinf` | °F | Indoor temperature |
| `humidityin` | % RH | Indoor relative humidity |

### Outdoor sensors (WH65 array — channel 1)

| Field | Unit | Notes |
|---|---|---|
| `tempf` | °F | Outdoor temperature |
| `humidity` | % RH | Outdoor relative humidity |
| `windspeedmph` | mph | Instantaneous wind speed |
| `windgustmph` | mph | Instantaneous wind gust |
| `winddir` | degrees | Instantaneous wind direction (0=N, 90=E) |
| `winddir_avg10m` | degrees | 10-minute average wind direction |
| `maxdailygust` | mph | Max gust since midnight |
| `solarradiation` | W/m² | Solar irradiance |
| `uv` | index | UV index (0–16+) |

### Barometric pressure

| Field | Unit | Notes |
|---|---|---|
| `baromabsin` | inHg | Absolute pressure (true atmospheric) |
| `baromrelin` | inHg | Relative pressure (sea-level adjusted) |

Note: if `baromrelin == baromabsin` the altitude correction is set to zero in the gateway config.

### Rainfall

| Field | Unit | Notes |
|---|---|---|
| `rainratein` | in/hr | Current instantaneous rain rate |
| `eventrainin` | in | Accumulation for current rain event |
| `hourlyrainin` | in | Rolling past-hour accumulation |
| `last24hrainin` | in | Rolling 24-hour accumulation |
| `dailyrainin` | in | Accumulation since midnight |
| `weeklyrainin` | in | Accumulation since Monday |
| `monthlyrainin` | in | Accumulation since month start |
| `yearlyrainin` | in | Accumulation since Jan 1 |
| `totalrainin` | in | Lifetime total (resets with yearlyrainin annually) |

### Additional sensor (channel 2 — WH31/WH51/etc)

| Field | Unit | Notes |
|---|---|---|
| `temp2f` | °F | Secondary sensor temperature |
| `humidity2` | % RH | Secondary sensor humidity |

In the sample setup, channel 2 reads ~72°F / 52% RH while outdoor is 51°F / 93% — likely a greenhouse or garage sensor.

### Atmospheric derived

| Field | Unit | Notes |
|---|---|---|
| `vpd` | kPa | Vapor Pressure Deficit (used in agriculture/growing) |

### Battery status

Battery fields are **boolean flags**, not voltages.

| Field | Sensor | 0 = OK | 1 = Low |
|---|---|---|---|
| `wh65batt` | WH65 outdoor array | ✓ | low battery |
| `batt2` | Channel 2 sensor | ✓ | low battery |

---

## Device Models

| Model | Type | Config method |
|---|---|---|
| GW1100 / GW1100A | Wi-Fi gateway with built-in indoor sensors | Web browser UI |
| GW1000 | Wi-Fi gateway with built-in sensors (older) | WSView mobile app |

The `A` suffix denotes a regional variant (frequency band).

---

## External references

- [Community protocol gist](https://gist.github.com/tsmx/70d06c6e4cfc65b6d7d23c7f9c75b17c) — comprehensive field list
- [homebridge-ecowitt-weather-sensors wiki](https://github.com/rhockenbury/homebridge-ecowitt-weather-sensors/wiki/Submitting-Data-Report) — field-by-field docs
- [ecowitt2mqtt](https://github.com/bachya/ecowitt2mqtt) — Python implementation with unit conversion and battery interpretation
- [Official GW1100 manual](https://oss.ecowitt.net/uploads/20241111/GW1100%20Manual.pdf)
- [Local IoT API spec](https://oss.ecowitt.net/uploads/20240827/Local%20IOT%20API%2020240828.pdf)
