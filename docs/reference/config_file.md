---
description: Reference of the configuration file of Ella MOCN.
---

# Configuration File

Ella MOCN is configured using a YAML file. Start Ella MOCN with the `--config` flag to specify its path. Unknown keys are rejected. Changes take effect on restart.

The operators and radios are not in this file: they are in the database, and set with the UI or the [API](api/index.md).

## Parameters

- `logging` (object, optional): The logging configuration.
    - `level` (string, optional): The log level: `debug`, `info`, `warn`, or `error`. Default `info`.
- `db` (object): The database configuration.
    - `path` (string): The path to the SQLite database file.
- `api` (object, optional): The HTTP API and UI.
    - `address` (string, optional): The IP address to listen on. Default `0.0.0.0`.
    - `port` (int, optional): The TCP port to listen on. Default `5030`.
    - `tls` (object, optional): Serve over HTTPS, TLS 1.2 or later. Default: plain HTTP.
        - `cert` (string): The path to the PEM certificate chain, the server's certificate first.
        - `key` (string): The path to the PEM private key.
- `ran` (object, optional): The radios.
    - `signalling` (object, optional): The S1 and NG listeners that radios connect to.
        - `address` (string, optional): The IP address to listen on. Default `0.0.0.0`.
        - `s1_port` (int, optional): The SCTP port of S1. Default `36412`.
        - `ng_port` (int, optional): The SCTP port of NG. Default `38412`.
    - `user_plane` (object, optional): The user plane towards the radios.
        - `enabled` (boolean, optional): Allow [user-plane aggregation](../explanation/user_plane_aggregation_with_ebpf.md). When `true`, Ella MOCN sets `net.ipv4.ip_forward` and `net.ipv6.conf.all.forwarding` to `1` at startup and needs the capabilities in the [system requirements](system_reqs.md). When `false`, every operator uses the direct user plane. Default `false`.
        - `name` (string): The network interface that radios' user traffic arrives on. Ella MOCN's address is its first global IPv4 address, or else its first global IPv6 address. Either `name` or `address` must be set when `enabled` is `true`, not both.
        - `address` (string): Ella MOCN's user-plane address towards the radios. A unicast address of an interface of the host. Either `name` or `address` must be set when `enabled` is `true`, not both.
- `telemetry` (object, optional): The [traces](observability.md#traces).
    - `enabled` (boolean, optional): Export traces. Default `false`.
    - `otlp_endpoint` (string): The `host:port` of an OTLP/gRPC collector, over plain gRPC. Required when `enabled` is `true`.

With the snap, the configuration file is `/var/snap/ella-mocn/common/mocn.yaml`.

## Example

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
