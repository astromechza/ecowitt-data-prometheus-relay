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

## Hardware inventory (this deployment)

| Device | Model | Channel | Role | Status (2026-05-23) |
|---|---|---|---|---|
| GW1100 | Ecowitt GW1100 | — | Wi-Fi gateway + indoor sensors | ✅ Online, IP `<gateway-ip>` |
| Indoor console | Ecowitt WS2910 | — | LCD display console; relays WS69 RF data to GW1100 | ✅ Online |
| Outdoor array | Ecowitt WS69 | — | 7-in-1: wind/rain/temp/humidity/UV/solar | ⚠️ Offline since 2026-05-19 (hardware failure) |
| Temp/humidity | Ecowitt WH31 | CH2 | Extra indoor/sheltered temp+humidity | ✅ Connected, battery normal |
| Air quality | Ecowitt WH45 | — | PM2.5, PM10, CO₂, temp/humidity | ❌ Not connected (unplugged) |
| Soil moisture | Ecowitt WH51 × 3 | Soil CH1/2/3 | Soil moisture % | ❌ All disconnected (dead batteries) |

Note: `stationtype` in the payload reports as `GW1100A_V2.4.x` — the `A` suffix is a firmware/regional variant designation, not a different hardware model.

---

## Sample Report

Captured from a live GW1100 device on 2026-05-18. WS69 and WH31 CH2 both online; WH45 and WH51s offline.

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

### Outdoor sensors (WS69 array)

The WS69 is a 7-in-1 outdoor sensor array derived from the WH65 sensor family. The field name prefix `wh65` in `wh65batt` is inherited from that lineage — the actual device in this deployment is a WS69.

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

### Atmospheric derived

| Field | Unit | Notes |
|---|---|---|
| `vpd` | kPa | Vapor Pressure Deficit (used in agriculture/growing) |

### Numbered channel sensors (WH31, WH51, etc.)

Channel sensors append a digit to field names. Channel numbering for temp/humidity sensors (WH31) is independent from soil sensor channel numbering (WH51).

#### WH31 temp/humidity (CH2 in this deployment)

| Field | Unit | Notes |
|---|---|---|
| `temp2f` | °F | CH2 temperature — WH31 sensor, location indoors/sheltered |
| `humidity2` | % RH | CH2 humidity |

In this deployment CH2 reads ~72°F / 52% RH (indoor/sheltered) vs outdoor 51°F / 93%.

#### WH51 soil moisture (Soil CH1/2/3 in this deployment — currently offline)

| Field | Unit | Notes |
|---|---|---|
| `soilmoisture1` / `2` / `3` | % | Volumetric soil moisture percentage |
| `soilad1` / `2` / `3` | raw | Raw ADC value from soil sensor |
| `soilbatt1` / `2` / `3` | V | Soil sensor battery voltage (not a boolean flag — actual voltage) |

#### WH45 air quality (currently offline/unplugged)

| Field | Unit | Notes |
|---|---|---|
| `pm25` | µg/m³ | PM2.5 particulate concentration |
| `pm25_24h` | µg/m³ | 24-hour average PM2.5 |
| `pm10` | µg/m³ | PM10 particulate concentration |
| `pm10_24h` | µg/m³ | 24-hour average PM10 |
| `co2` | ppm | CO₂ concentration (needs ~10 min warm-up for stable reading) |
| `co2_24h` | ppm | 24-hour average CO₂ |

WH45 also reports its own temp, humidity, and battery via fields that may overlap with channel numbering depending on gateway firmware version.

### Battery status

Battery fields are **boolean flags**, not voltages (exception: WH51 `soilbattN` is an actual voltage).

| Field | Sensor | 0 = OK | 1 = Low |
|---|---|---|---|
| `wh65batt` | WS69 outdoor array (inherits WH65 family naming) | ✓ | low battery |
| `batt2` | CH2 sensor (WH31 in this deployment) | ✓ | low battery |

---

## Device models

| Model | Type | Config method | Notes |
|---|---|---|---|
| GW1100 | Wi-Fi gateway + built-in indoor sensors | Web browser UI at `http://<ip>/` | Reports as `GW1100A` in stationtype |
| GW1000 | Wi-Fi gateway + built-in sensors (older) | WSView mobile app | |
| WS2910 | Indoor LCD console/display | Pairs to gateway via RF relay | Receives WS69 RF; relays to GW1100 |
| WS69 | 7-in-1 outdoor sensor array | Pairs via RF to WS2910 console | Derived from WH65 family; solar + AA battery backup |
| WH31 / WN31 | Temp + humidity sensor | Pairs to gateway via RF | Multi-channel, LCD display |
| WH51 | Soil moisture sensor | Pairs to gateway via RF | 2× AA battery; resets on battery swap, needs re-pairing |
| WH45 | Air quality (PM2.5/PM10/CO₂) | Pairs to gateway via RF | AC-powered; 868 MHz (EU); CO₂ needs ~10 min warm-up |

---

## External references

- [Community protocol gist](https://gist.github.com/tsmx/70d06c6e4cfc65b6d7d23c7f9c75b17c) — comprehensive field list
- [homebridge-ecowitt-weather-sensors wiki](https://github.com/rhockenbury/homebridge-ecowitt-weather-sensors/wiki/Submitting-Data-Report) — field-by-field docs
- [ecowitt2mqtt](https://github.com/bachya/ecowitt2mqtt) — Python implementation with unit conversion and battery interpretation
- [Official GW1100 manual](https://oss.ecowitt.net/uploads/20241111/GW1100%20Manual.pdf)
- [Local IoT API spec](https://oss.ecowitt.net/uploads/20240827/Local%20IOT%20API%2020240828.pdf)
