# Connectivity

The interfaces Ella MOCN exposes and the peers it connects to. Defaults are in parentheses.

| Interface | Peer | Transport | Local address | Remote address | Port | Used |
| --- | --- | --- | --- | --- | --- | --- |
| API and UI | Users, applications, Prometheus | TCP, or TLS with `api.tls` | `api.address` (`0.0.0.0`) |  | `api.port` (`5030`) | Always |
| S1, from radios | eNBs | SCTP, PPID `18` | `ran.signalling.address` (`0.0.0.0`) |  | `ran.signalling.s1_port` (`36412`) | Always |
| NG, from radios | gNBs | SCTP, PPID `60` | `ran.signalling.address` (`0.0.0.0`) |  | `ran.signalling.ng_port` (`38412`) | Always |
| S1, to MMEs | The operators' MMEs | SCTP, PPID `18` | Source address of the host's route to the MME (`src`) | The MME's `tnl_addresses` | `36412` | Always |
| NG, to AMFs | The operators' AMFs | SCTP, PPID `60` | Source address of the host's route to the AMF (`src`) | The AMF's `tnl_addresses` | `38412` | Always |
| GTP-U, with radios | eNBs, gNBs | GTP-U over UDP | `ran.user_plane.address`, or the address of `ran.user_plane.name` |  | `2152` | Only with `ran.user_plane.enabled`, for operators with user-plane aggregation |
| GTP-U, with operators | The operators' S-GWs and UPFs | GTP-U over UDP | Same as S1 or NG to the operator; for the other IP version, source address of the host's route to the S-GW or UPF |  | `2152` | Only with `ran.user_plane.enabled`, for operators with user-plane aggregation |
| OTLP | OpenTelemetry collector | gRPC over TCP, without TLS |  | `telemetry.otlp_endpoint` | `telemetry.otlp_endpoint` | Only with `telemetry.enabled` |
