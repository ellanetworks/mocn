---
description: Reference for observability features in Ella MOCN.
---

# Observability

Ella MOCN supports four observability pillars: Logs, Metrics, Traces and Profiles.

## Logs

Ella MOCN writes its logs to standard output, one JSON object per line, at the level of `logging.level` in the [configuration file](config_file.md): `debug`, `info`, `warn` or `error`.

| Field | Description |
| --- | --- |
| `ts` | Time of the event. |
| `level` | `debug`, `info`, `warn` or `error`. |
| `msg` | What happened. |
| `trace_id` | Trace ID of the span the event happened in, as 32 hex digits. Absent outside a span. |
| `span_id` | Span ID of the span the event happened in, as 16 hex digits. Absent outside a span. |
| `trace_flags` | W3C trace flags of that span, as 2 hex digits: `01` when sampled. Absent outside a span. |

Other fields depend on the event.

### Audit events

Audit events are logs at level `info` with `msg` set to `audit event`.

| Field | Description |
| --- | --- |
| `action` | What was done, such as `auth_login` or `create_operator`. |
| `actor` | Email of the user who did it. Empty when unknown. |
| `ip` | Address the request came from. |
| `details` | What was done, in words. |

## Metrics

Ella MOCN exposes Prometheus metrics at [`GET /api/v1/metrics`](api/metrics.md).

| Metric | Type | Labels | Description |
|---|---|---|---|
| `ellamocn_radio_associations` | Gauge | `interface`, `state` (`connected`, `up`, `setup_failed`) | Radios connected, by state: `connected` before the S1 or NG Setup, `up` after, `setup_failed` after Ella MOCN rejected the last S1 or NG Setup. |
| `ellamocn_core_associations` | Gauge | `interface`, `operator`, `state` (`disconnected`, `connected`, `up`, `setup_failed`) | Associations to MMEs and AMFs, by operator and state. |
| `ellamocn_core_overloaded` | Gauge | `interface`, `operator` | MMEs and AMFs reporting overload, by operator. |
| `ellamocn_setup_attempts_total` | Counter | `interface`, `side` (`radio`, `core`), `result` (`success`, `failure`, `timeout`, `unknown_plmn`, …) | S1 and NG Setup attempts from radios (`radio`) and to MMEs and AMFs (`core`), by result. |
| `ellamocn_initial_ue_messages_total` | Counter | `interface`, `result` (`relayed`, `unknown_plmn`, `no_mme`, `no_amf`, `rejected`, `invalid`, `error`) | New UE connections from radios, by result. |
| `ellamocn_paging_messages_total` | Counter | `interface`, `result` (`relayed`, `no_radio`, `invalid`) | Paging messages from MMEs and AMFs, by result. |
| `ellamocn_overload_messages_total` | Counter | `interface`, `procedure` (`overload_start`, `overload_stop`), `result` (`relayed`, `no_radio`, `ignored`, `invalid`) | Overload start and stop messages from MMEs and AMFs, by result. |
| `ellamocn_handover_messages_total` | Counter | `interface`, `procedure` (`handover_request`, `path_switch_request`), `result` (`relayed`, `no_radio`, `no_mme`, `no_amf`, `invalid`, `error`) | Handover and path switch requests, by result. |
| `ellamocn_pws_messages_total` | Counter | `interface`, `procedure` (`write_replace_warning`, `kill`, `pws_cancel`, `pws_restart_indication`, `pws_failure_indication`), `result` (`relayed`, `no_radio`, `timeout`, …) | Public warning requests from MMEs and AMFs and indications from radios, by result. |
| `ellamocn_ue_connections` | Gauge | `interface`, `operator` | UE connections relayed, by operator. |
| `ellamocn_api_request_duration_seconds` | Histogram | `method`, `route`, `code` | Duration of API requests. |
| `ellamocn_database_query_duration_seconds` | Histogram | `pool` (`write`, `read`) | Duration of database queries. |
| `ellamocn_database_query_errors_total` | Counter | `pool` (`write`, `read`) | Failed database queries. |
| `ellamocn_database_storage_bytes` | Gauge | `file` (`main`, `wal`) | Size of the database on disk. |
| `ellamocn_build_info` | Gauge | `version`, `revision`, `branch`, `goversion`, `goos`, `goarch`, `tags` | Build information. Always 1. |
| `go_*` | Gauge, Counter, Summary | | Go runtime. |
| `process_*` | Gauge, Counter | | Process memory, CPU and file descriptors. |

### User-plane metrics

With [user-plane aggregation](../explanation/user_plane_aggregation_with_ebpf.md):

| Metric | Type | Labels | Description |
|---|---|---|---|
| `ellamocn_user_plane_enabled` | Gauge | | 1 when `ran.user_plane.enabled` is `true` in the [configuration file](config_file.md), 0 otherwise. |
| `ellamocn_user_plane_datapath_loaded` | Gauge | | 1 while the user-plane eBPF program is loaded, 0 otherwise. |
| `ellamocn_user_plane_state` | Gauge | `operator`, `state` (`ready`, `unavailable`, `off`) | 1 for the operator's current [user-plane state](api/operators.md), 0 for the other states. |
| `ellamocn_user_plane_unavailable` | Gauge | `operator`, `reason` (`not_connected`, `no_route`, `route_mismatch`, `no_address`, `interface_error`, `too_many_operators`) | 1 for the [reason](api/operators.md) while the state is `unavailable`, 0 otherwise. |
| `ellamocn_user_plane_tunnels` | Gauge | `operator`, `direction` | GTP-U tunnels relayed. |
| `ellamocn_user_plane_tunnel_setups_total` | Counter | `operator`, `direction` | GTP-U tunnels set up. |
| `ellamocn_user_plane_tunnel_setup_failures_total` | Counter | `operator`, `interface` (`s1`, `ng`), `procedure`, `reason` (`user_plane_unavailable`, `no_route`, `route_mismatch`, `no_address`, `no_teid`, `too_many_paths`, `invalid_address`, `unknown_endpoint`, `other`) | Procedures whose user-plane transport could not be relayed. The UE is released, or the handover or path switch is rejected. |
| `ellamocn_user_plane_packets_total` | Counter | `operator`, `radio`, `direction` | G-PDUs relayed. End Marker and Tunnel Status messages are not counted. |
| `ellamocn_user_plane_bytes_total` | Counter | `operator`, `radio`, `direction` | Octets of the T-PDUs of the G-PDUs relayed: the inner IP packet, without the GTP-U header and extension headers. |
| `ellamocn_user_plane_dropped_packets_total` | Counter | `operator`, `path` (`fast`, `slow`), `reason` | GTP-U messages dropped. |
| `ellamocn_user_plane_slow_path_packets_total` | Counter | `reason` | Packets on user-plane interfaces that the fast path left to the kernel. |
| `ellamocn_user_plane_slow_path_relayed_total` | Counter | `operator`, `direction` | G-PDUs, End Markers and Tunnel Status messages relayed by the slow path. |
| `ellamocn_user_plane_error_indications_total` | Counter | `origin`, `result` | GTP-U Error Indications. |
| `ellamocn_user_plane_peer_up` | Gauge | `operator`, `side` (`radio`, `core`), `peer` | 0 once 3 Echo Requests in a row, sent every 60 s, go unanswered, 3 to 4 minutes after the peer stops answering. 1 otherwise. Present while a tunnel of the operator relays to the peer. |
| `ellamocn_user_plane_bpf_map_entries` | Gauge | `map` (`tunnels`, `tunnel_stats`, `ifaces`, `path_stats`) | Entries in a map of the user-plane datapath. |
| `ellamocn_user_plane_bpf_map_max_entries` | Gauge | `map` (`tunnels`, `tunnel_stats`, `ifaces`, `path_stats`) | Capacity of a map of the user-plane datapath. |
| `ellamocn_user_plane_bpf_map_memory_bytes` | Gauge | `map` (`tunnels`, `tunnel_stats`, `ifaces`, `config`, `path_stats`, `drops`, `passes`) | Kernel memory locked by a map of the user-plane datapath. |

| Label | Values |
|---|---|
| `operator` | Operator name. Series follow a rename and are removed with the operator. |
| `direction` | `uplink`: received from a radio. `downlink`: received from the operator. |
| `radio` | `ref`, in the [Radios API](api/radios.md), of the radio that carries the traffic. After a handover or path switch, later traffic counts under the target radio. Series are removed when the radio disconnects. |
| `procedure` | `initial_context_setup`, `erab_setup`, `erab_modify`, `erab_modification_indication`, `handover_preparation`, `handover_resource_allocation`, `path_switch_request` (`s1`); `initial_context_setup`, `pdu_session_resource_setup`, `pdu_session_resource_modify`, `pdu_session_resource_modify_indication`, `handover_preparation`, `handover_resource_allocation`, `path_switch_request` (`ng`). |
| `peer` | Address of the eNB, gNB, S-GW or UPF. |

`ellamocn_user_plane_dropped_packets_total` reasons:

| `path` | `operator` | `reason` |
|---|---|---|
| `fast` | Set | `wrong_interface`: received on an interface of another operator. `wrong_egress_interface`: routed out of another interface than the tunnel's. `fib_blackhole`, `fib_unreachable`, `fib_prohibit`: route type toward the peer. `rewrite_failed`: the packet could not be rewritten. |
| `slow` | Set | `wrong_interface`: received on an interface of another operator. `malformed`: invalid extension headers. `unsupported_extension_header`: an extension header that must be comprehended. `no_egress_interface`: the tunnel has no egress interface. `wrong_egress_interface`: the route toward the peer leaves by another interface than the tunnel's. `send_failed`: the kernel refused the packet. |
| `slow` | Empty | `no_tunnel`: unknown TEID. `quarantined`: TEID of a tunnel closed less than 10 s ago. `malformed`: invalid GTP-U header. `wrong_interface`: Echo Request on an interface Ella MOCN does not relay. `unknown_message`: unsupported message type. `supported_extension_headers_notification`: received from a peer. `receive_buffer_full`: dropped by the kernel because the receive buffer of the slow path's socket was full. |

`ellamocn_user_plane_slow_path_packets_total` reasons:

| `reason` | Packet |
|---|---|
| `ip_fragment` | An IP fragment, of any protocol. Counted before UDP and GTP-U parsing. |
| `no_tunnel` | Unknown TEID. |
| `not_local` | Not addressed to the tunnel's address. |
| `extension_header` | An extension header the fast path does not relay. |
| `zero_checksum` | IPv6 with a zero UDP checksum. |
| `merged` | A GRO aggregate. |
| `frag_needed` | Larger than the egress MTU. |
| `forwarding_disabled` | IP forwarding is off on the ingress interface. |
| `ipsec_policy` | An IPsec policy with a template and no XFRM interface ID, or a blocking default policy, is installed, and the ingress or egress interface is not an XFRM interface. |
| `fib_other` | Another route lookup result. |

`ellamocn_user_plane_error_indications_total` labels:

| `origin` | `result` |
|---|---|
| `received`: from a peer | `translated`: sent to the other side of the matching tunnels. `no_tunnel`: no matching tunnel. `no_return_address`: no address known for the other side. `malformed`. |
| `generated`: by Ella MOCN, for a G-PDU to an unknown TEID other than 0 | `sent`. |

## Traces

Ella MOCN exports [OpenTelemetry](https://opentelemetry.io/) traces when `telemetry.enabled` is set in the [configuration file](config_file.md).

| Property | Value |
| --- | --- |
| Export | OTLP over gRPC, without TLS |
| Service | `ella-mocn` |
| Sampling | Parent-based, always on for a trace the MOCN starts |
| Propagation | W3C Trace Context |
| S1AP, NGAP and database spans | No NAS, security keys, subscriber identity, SQL text or parameters |

### Spans

| Span | Kind | Description |
| --- | --- | --- |
| `{method} {route}` | Server | A request to the API, except `/api/v1/livez`, `/api/v1/readyz`, `/api/v1/metrics` and `/api/v1/pprof/`. Links to the caller's `traceparent`. |
| `s1ap/receive` | Server, Consumer | An S1AP message from a radio or an MME. |
| `s1ap/send` | Client, Producer | An S1AP message to a radio or an MME, a child of the `s1ap/receive` that caused it. |
| `ngap/receive` | Server, Consumer | An NGAP message from a radio or an AMF. |
| `ngap/send` | Client, Producer | An NGAP message to a radio or an AMF, a child of the `ngap/receive` that caused it. |
| `db.{call}`, such as `db.CreateUser` | Internal | A database call within a trace. |

### RAN trace correlation

When an MME or AMF activates a trace for a UE, that UE's S1AP or NGAP spans join the core's trace.

| Item | Value |
| --- | --- |
| OpenTelemetry trace ID | 8 octets `0x00` ‖ E-UTRAN Trace ID or NG-RAN Trace ID (8 octets) |

## Profiles

Ella MOCN exposes the [net/http/pprof](https://pkg.go.dev/net/http/pprof) API at [`/api/v1/pprof/`](api/pprof.md).
