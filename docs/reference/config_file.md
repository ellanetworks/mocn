---
description: Ella MOCN configuration file keys and defaults.
---

# Configuration File

```yaml
logging:
  level: info
db:
  path: /var/lib/mocn/mocn.db
api:
  address: 0.0.0.0
  port: 5030
  tls:
    cert: /etc/mocn/api.pem
    key: /etc/mocn/api.key
s1:
  address: 0.0.0.0
  port: 36412
ng:
  address: 0.0.0.0
  port: 38412
telemetry:
  enabled: true
  otlp_endpoint: localhost:4317
```

| Key | Default | Description |
|---|---|---|
| `logging.level` | `info` | `debug`, `info`, `warn` or `error`. |
| `db.path` | | Path of the database file. Required. |
| `api.address` | `0.0.0.0` | Address of the API and UI listener. |
| `api.port` | `5030` | Port of the API and UI listener. |
| `api.tls.cert` | | Path of the PEM certificate chain, the server's certificate first. With `api.tls.key`, the API and UI are served over HTTPS (TLS 1.2 or later). Without `api.tls`, over plain HTTP. |
| `api.tls.key` | | Path of the PEM private key. Required with `api.tls.cert`. |
| `s1.address` | `0.0.0.0` | Address of the S1 SCTP listener that radios connect to. |
| `s1.port` | `36412` | Port of the S1 SCTP listener. |
| `ng.address` | `0.0.0.0` | Address of the NG SCTP listener that radios connect to. |
| `ng.port` | `38412` | Port of the NG SCTP listener. |
| `telemetry.enabled` | `false` | Export [traces](observability.md#traces). |
| `telemetry.otlp_endpoint` | | The `host:port` of an OTLP/gRPC collector, over plain gRPC. Required when `telemetry.enabled` is `true`. |

MMEs are reached on port 36412, with SCTP PPID 18. AMFs are reached on port 38412, with SCTP PPID 60.
