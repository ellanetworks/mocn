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

Ella MOCN exposes Prometheus metrics at [`GET /api/v1/metrics`](api/metrics.md). The `interface` label is `s1` (TS 36.413) or `ng` (TS 38.413).

| Metric | Type | Labels | Description |
|---|---|---|---|
| `ellamocn_radio_associations` | Gauge | `interface`, `state` (`connected`, `up`) | Radio associations. `connected`: SCTP association up, no S1 or NG Setup. `up`: S1 or NG Setup succeeded. |
| `ellamocn_core_associations` | Gauge | `interface`, `operator`, `state` (`disconnected`, `connected`, `up`, `setup_failed`) | Associations to the configured MMEs (`s1`) and AMFs (`ng`), by operator name. `disconnected`: no SCTP association. `connected`: SCTP association up, no S1 or NG Setup. `up`: S1 or NG Setup succeeded. `setup_failed`: the MME or AMF rejected the setup. Every state of every operator with an MME or AMF is exported, at 0 when none. |
| `ellamocn_core_overloaded` | Gauge | `interface`, `operator` | Configured MMEs and AMFs with an ongoing overload action, by operator name. Exported at 0 for every operator with an MME or AMF. |
| `ellamocn_setup_attempts_total` | Counter | `interface`, `side` (`radio`, `core`), `result` | S1 Setup and NG Setup procedures, one per attempt. `radio`: received from radios, with result `success`, `invalid` (undecodable or invalid request), `unknown_plmn` (no operator serves a broadcast PLMN), `no_mme` or `no_amf` (no MME or AMF of the serving operators is up), `mme_failure` or `amf_failure` (the serving operators' MMEs or AMFs rejected the setup) or `error` (the response could not be built or sent, or the radio left before the answer). `core`: sent to MMEs and AMFs, with result `success`, `failure` (S1 Setup Failure, NG Setup Failure or an invalid response), `timeout` (no answer) or `error` (the request could not be built or sent, or the association dropped before the answer). |
| `ellamocn_initial_ue_messages_total` | Counter | `interface`, `result` (`relayed`, `unknown_plmn`, `no_mme`, `no_amf`, `rejected`, `invalid`, `error`) | INITIAL UE MESSAGEs received from radios. `relayed`: sent to an MME or AMF. `unknown_plmn`: no operator serves the PLMN. `no_mme`, `no_amf`: no selectable MME or AMF of the operator. `rejected`: reuses an allocated eNB UE S1AP ID or RAN UE NGAP ID; an ERROR INDICATION is sent. `invalid`: undecodable or missing a mandatory IE. `error`: internal failure, or the send to the MME or AMF failed. |
| `ellamocn_paging_messages_total` | Counter | `interface`, `result` (`relayed`, `no_radio`, `invalid`) | PAGING messages received from MMEs and AMFs. `relayed`: sent to the radios that serve a TAI of the message in one of the operator's PLMNs. `no_radio`: no radio that is up serves such a TAI. `invalid`: undecodable. |
| `ellamocn_overload_messages_total` | Counter | `interface`, `procedure` (`overload_start`, `overload_stop`), `result` (`relayed`, `no_radio`, `ignored`, `invalid`) | OVERLOAD START and OVERLOAD STOP messages received from MMEs. `interface` is `s1` only. `relayed`: sent to the radios that broadcast one of the operator's PLMNs, including the replay of an ongoing OVERLOAD START to a radio after its S1 Setup. `no_radio`: no such radio is up. `ignored` (`overload_stop` only): no ongoing overload action for the GUMMEIs. `invalid`: undecodable or with an invalid GUMMEI. |
| `ellamocn_ue_connections` | Gauge | `interface`, `operator` | UE-associated logical S1-connections (TS 36.413 clause 3.1) and NG-connections relayed, by operator name. |
| `ellamocn_api_request_duration_seconds` | Histogram | `method`, `route`, `code` | Duration of API requests, by route (`/api/v1/operators/{name}`). Requests to `/api/v1/pprof/` and to no route are left out. |
| `ellamocn_database_query_duration_seconds` | Histogram | `pool` (`write`, `read`) | Duration of database calls, statements, transaction and row scanning included. Exemplars: `trace_id`, `span_id`. |
| `ellamocn_database_query_errors_total` | Counter | `pool` (`write`, `read`) | Failed database calls. A record not found or in conflict is a result, not a failure. |
| `ellamocn_database_storage_bytes` | Gauge | `file` (`main`, `wal`) | Size of the database on disk. |
| `ellamocn_build_info` | Gauge | `version`, `revision`, `branch`, `goversion`, `goos`, `goarch`, `tags` | Always 1. |
| `go_*` | Gauge, Counter, Summary | | Go runtime: goroutines, heap, garbage-collection pauses. |
| `process_*` | Gauge, Counter | | Process: memory, CPU, open file descriptors, start time. |

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
