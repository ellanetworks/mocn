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
ran:
  signalling:
    address: 0.0.0.0
    s1_port: 36412
    ng_port: 38412
  user_plane:
    enabled: true
    name: eth1
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
| `ran.signalling.address` | `0.0.0.0` | Address of the S1 and NG SCTP listeners that radios connect to. |
| `ran.signalling.s1_port` | `36412` | Port of the S1 SCTP listener. |
| `ran.signalling.ng_port` | `38412` | Port of the NG SCTP listener. |
| `ran.user_plane.enabled` | `false` | Allow [user-plane aggregation](../explanation/user_plane_aggregation_with_ebpf.md). When `true`, Ella MOCN sets `net.ipv4.ip_forward` and `net.ipv6.conf.all.forwarding` to `1` at startup and needs the capabilities in the [system requirements](system_reqs.md). When `false`, every operator uses the direct user plane. |
| `ran.user_plane.name` | | Interface radios' user traffic arrives on. Set either `name` or `address`, not both. Required when `ran.user_plane.enabled` is `true`. |
| `ran.user_plane.address` | | Ella MOCN's user-plane address toward the radios. Default: the first IPv4 address of `name`, or else its first global IPv6 address. Without `name`, the interface is the one that holds this address. |
| `telemetry.enabled` | `false` | Export [traces](observability.md#traces). |
| `telemetry.otlp_endpoint` | | The `host:port` of an OTLP/gRPC collector, over plain gRPC. Required when `telemetry.enabled` is `true`. |

See [Connectivity](connectivity.md) for the ports Ella MOCN uses.
