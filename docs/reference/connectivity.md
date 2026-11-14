---
description: Reference of the network interfaces and ports of Ella MOCN.
---

# Connectivity

| Interface | Peer | Transport | Address | Port |
| --------- | ---- | --------- | ------- | ---- |
| API and UI | Users, applications, Prometheus | TCP, or TLS with `api.tls` | `api.address` | `api.port` (`5030`) |
| S1, from radios | eNBs | SCTP, PPID `18` | `s1.address` | `s1.port` (`36412`) |
| NG, from radios | gNBs | SCTP, PPID `60` | `ng.address` | `ng.port` (`38412`) |
| S1, to MMEs | The operators' MMEs | SCTP, PPID `18` | The MME's `tnl_addresses` | `36412` |
| NG, to AMFs | The operators' AMFs | SCTP, PPID `60` | The AMF's `tnl_addresses` | `38412` |
| OTLP | OpenTelemetry collector | gRPC over TCP, without TLS | `telemetry.otlp_endpoint` | `telemetry.otlp_endpoint` |

Defaults are in parentheses. The API and the S1 and NG listeners listen on all IPv4 addresses (`0.0.0.0`) by default. OTLP is used only when `telemetry.enabled` is `true`.

On Kubernetes, S1 is at `ella-mocn-s1.<namespace>.svc.cluster.local:36412` and NG at `ella-mocn-ng.<namespace>.svc.cluster.local:38412`.
