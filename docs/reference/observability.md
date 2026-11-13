---
description: Prometheus metrics exposed by Ella MOCN.
---

# Observability

Ella MOCN exposes Prometheus metrics at `GET /api/v1/metrics`. The `interface` label is `s1` (TS 36.413).

| Metric | Type | Labels | Description |
|---|---|---|---|
| `ellamocn_radio_associations` | Gauge | `interface`, `state` (`connected`, `up`) | Radio associations. `connected`: SCTP association up, no S1 Setup. `up`: S1 Setup succeeded. |
| `ellamocn_core_associations` | Gauge | `interface`, `operator`, `state` (`disconnected`, `connected`, `up`, `setup_failed`) | Associations to the configured MMEs, by operator name. `disconnected`: no SCTP association. `connected`: SCTP association up, no S1 Setup. `up`: S1 Setup succeeded. `setup_failed`: the MME rejected S1 Setup. Every state of every operator with an MME is exported, at 0 when none. |
| `ellamocn_core_overloaded` | Gauge | `interface`, `operator` | Configured MMEs with an ongoing overload action, by operator name. Exported at 0 for every operator with an MME. |
| `ellamocn_setup_attempts_total` | Counter | `interface`, `side` (`radio`, `core`), `result` | S1 Setup procedures, one per attempt. `radio`: received from radios, with result `success`, `invalid` (undecodable or invalid request), `unknown_plmn` (no operator serves a broadcast PLMN), `no_mme` (no MME of the serving operators is up), `mme_failure` (the serving operators' MMEs rejected S1 Setup) or `error` (the response could not be built or sent, or the radio left before the answer). `core`: sent to MMEs, with result `success`, `failure` (S1 Setup Failure or an invalid response), `timeout` (no answer) or `error` (the request could not be built or sent, or the association dropped before the answer). |
| `ellamocn_initial_ue_messages_total` | Counter | `interface`, `result` (`relayed`, `unknown_plmn`, `no_mme`, `rejected`, `invalid`, `error`) | INITIAL UE MESSAGEs received from radios. `relayed`: sent to an MME. `unknown_plmn`: no operator serves the PLMN. `no_mme`: no selectable MME of the operator. `rejected`: reuses an allocated eNB UE S1AP ID; an ERROR INDICATION is sent. `invalid`: undecodable or missing a mandatory IE. `error`: internal failure, or the send to the MME failed. |
| `ellamocn_paging_messages_total` | Counter | `interface`, `result` (`relayed`, `no_radio`, `invalid`) | PAGING messages received from MMEs. `relayed`: sent to the radios that serve a TAI of the message in one of the operator's PLMNs. `no_radio`: no radio that is up serves such a TAI. `invalid`: undecodable. |
| `ellamocn_overload_messages_total` | Counter | `interface`, `procedure` (`overload_start`, `overload_stop`), `result` (`relayed`, `no_radio`, `ignored`, `invalid`) | OVERLOAD START and OVERLOAD STOP messages received from MMEs. `relayed`: sent to the radios that broadcast one of the operator's PLMNs, including the replay of an ongoing OVERLOAD START to a radio after its S1 Setup. `no_radio`: no such radio is up. `ignored` (`overload_stop` only): no ongoing overload action for the GUMMEIs. `invalid`: undecodable or with an invalid GUMMEI. |
| `ellamocn_ue_connections` | Gauge | `interface`, `operator` | UE-associated logical S1-connections (TS 36.413 clause 3.1) relayed, by operator name. |
| `ellamocn_api_request_duration_seconds` | Histogram | `method`, `route`, `code` | Duration of API requests. |
| `ellamocn_database_query_duration_seconds` | Histogram | `pool` (`write`, `read`) | Duration of database calls, statements, transaction and row scanning included. |
| `ellamocn_database_query_errors_total` | Counter | `pool` (`write`, `read`) | Failed database calls. A record not found or in conflict is a result, not a failure. |
| `ellamocn_database_storage_bytes` | Gauge | `file` (`main`, `wal`) | Size of the database on disk. |
| `ellamocn_build_info` | Gauge | `version`, `revision`, `branch`, `goversion`, `goos`, `goarch`, `tags` | Always 1. |
| `go_*` | Gauge, Counter, Summary | | Go runtime: goroutines, heap, garbage-collection pauses. |
| `process_*` | Gauge, Counter | | Process: memory, CPU, open file descriptors, start time. |
