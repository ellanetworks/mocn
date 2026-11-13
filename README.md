# mocn
Integrate your neutral host radio network to mobile operators

## How-to guides

### Connect an operator's MME

1. Create or edit the operator with a Global eNB ID assigned by that operator and its MMEs' TNL addresses:

   ```shell
   curl -X PUT http://<mocn>:5030/api/v1/operators/<name> -H "Authorization: Bearer <token>" -d '{
     "name": "<name>", "plmns": [{"mcc": "001", "mnc": "01"}],
     "global_enb_id": {"plmn": {"mcc": "001", "mnc": "01"}, "enb_id_type": "macro", "enb_id": 1},
     "mmes": [{"tnl_addresses": ["10.0.0.1", "10.0.1.1"]}]}'
   ```

2. Ask the operator to accept that Global eNB ID and the TAIs of your radios on its MMEs.
3. Connect a radio (below). The MME's status in the operator page turns `up`.

### Connect a radio

1. Set the radio's MME address to Ella MOCN's `s1.address`, port 36412.
2. Broadcast at least one PLMN of an operator that has MMEs.
3. Check the Radios page, or `GET /api/v1/radios`: the radio's state is `up`.

### Find the MME that serves a UE

1. Call `GET /api/v1/ue-connections?radio=<ref>`, with the radio's ref from its page or from `GET /api/v1/radios`.
2. Find the UE connection by its eNB UE S1AP ID on the radio side (`radio.enb_ue_s1ap_id`).
3. Read the MME's addresses (`mme.tnl_addresses`) and the UE's IDs on the MME side (`mme.enb_ue_s1ap_id`, `mme.mme_ue_s1ap_id`). Give those IDs to the operator to find the UE in its MME.

## Reference

### Configuration file

| Key | Default | Description |
|---|---|---|
| `s1.address` | `0.0.0.0` | Address of the S1 SCTP listener that radios connect to. |
| `s1.port` | `36412` | Port of the S1 SCTP listener. |

MMEs are reached on port 36412. SCTP PPID is 18.

### API

| Endpoint | Field | Description |
|---|---|---|
| `GET /api/v1/operators`, `GET /api/v1/operators/{name}` | `ue_connections` | UE-associated logical S1-connections across the operator's MMEs; read-only, ignored in requests |
| | `mmes[].status.state` | `disconnected`, `connected` (SCTP up, no S1 Setup), `up`, `setup_failed` |
| | `mmes[].status.since` | Time the MME entered its state |
| | `mmes[].status.primary_tnl_address` | MME address in use |
| | `mmes[].status.mme_name`, `served_gummeis`, `relative_mme_capacity` | Learned from S1 SETUP RESPONSE and MME CONFIGURATION UPDATE |
| | `mmes[].status.ue_connections` | UE-associated logical S1-connections relayed to the MME |
| `GET /api/v1/operators/{name}` | `ue_connections_by_radio` | `radio` (ref), `name` (eNB name, if any) and `ue_connections` for each radio that broadcasts one of the operator's PLMNs or has UE connections of the operator; sorted by name, or ref without a name; read-only, ignored in requests |
| `GET /api/v1/radios`, `GET /api/v1/radios/{ref}` | `ref` | `<mcc>-<mnc>:<enb_id_type>:<enb_id>`, set once the radio has a Global eNB ID |
| | `global_enb_id`, `enb_name`, `supported_tas`, `default_paging_drx` | From the radio's S1 SETUP REQUEST and ENB CONFIGURATION UPDATE |
| | `state` | `connected` (SCTP up, no S1 Setup), `up` |
| | `connected_since`, `tnl_addresses` | SCTP association |
| | `ue_connections` | UE-associated logical S1-connections from the radio |
| `GET /api/v1/radios/{ref}` | `ue_connections_by_operator` | `operator` and `ue_connections` for each operator that owns a PLMN the radio broadcasts or has UE connections from the radio; sorted by operator name |
| `GET /api/v1/ue-connections` | `?radio=<ref>`, `?operator=<name>` | Optional filters on the radio's ref and the operator's name |
| | `radio.ref` | Ref of the radio that serves the UE |
| | `radio.enb_ue_s1ap_id` | Allocated by the radio |
| | `radio.mme_ue_s1ap_id` | Allocated by Ella MOCN |
| | `mme.tnl_addresses` | TNL addresses of the MME that serves the UE |
| | `mme.enb_ue_s1ap_id` | Allocated by Ella MOCN |
| | `mme.mme_ue_s1ap_id` | Allocated by the MME; `null` until the MME's first message for the UE |
| | `operator`, `plmn` | Operator that serves the selected PLMN, and that PLMN |
| | `since` | Time of the INITIAL UE MESSAGE |
| | `tai`, `ecgi` | Last TAI and E-UTRAN CGI the radio sent for the UE; `null` if none |

### Metrics

Ella MOCN exposes Prometheus metrics at `GET /api/v1/metrics`. The `interface` label is `s1` (TS 36.413).

| Metric | Type | Labels | Description |
|---|---|---|---|
| `ellamocn_radio_associations` | Gauge | `interface`, `state` (`connected`, `up`) | Radio associations. `connected`: SCTP association up, no S1 Setup. `up`: S1 Setup succeeded. |
| `ellamocn_core_associations` | Gauge | `interface`, `operator`, `state` (`disconnected`, `connected`, `up`, `setup_failed`) | Associations to the configured MMEs, by operator name. `disconnected`: no SCTP association. `connected`: SCTP association up, no S1 Setup. `up`: S1 Setup succeeded. `setup_failed`: the MME rejected S1 Setup. Every state of every operator with an MME is exported, at 0 when none. |
| `ellamocn_setup_attempts_total` | Counter | `interface`, `side` (`radio`, `core`), `result` | S1 Setup procedures, one per attempt. `radio`: received from radios, with result `success`, `invalid` (undecodable or invalid request), `unknown_plmn` (no operator serves a broadcast PLMN), `no_mme` (no MME of the serving operators is up), `mme_failure` (the serving operators' MMEs rejected S1 Setup) or `error` (the response could not be built or sent, or the radio left before the answer). `core`: sent to MMEs, with result `success`, `failure` (S1 Setup Failure or an invalid response), `timeout` (no answer) or `error` (the request could not be built or sent, or the association dropped before the answer). |
| `ellamocn_initial_ue_messages_total` | Counter | `interface`, `result` (`relayed`, `unknown_plmn`, `no_mme`, `rejected`, `invalid`, `error`) | INITIAL UE MESSAGEs received from radios. `relayed`: sent to an MME. `unknown_plmn`: no operator serves the PLMN. `no_mme`: no selectable MME of the operator. `rejected`: reuses an allocated eNB UE S1AP ID; an ERROR INDICATION is sent. `invalid`: undecodable or missing a mandatory IE. `error`: internal failure, or the send to the MME failed. |
| `ellamocn_ue_connections` | Gauge | `interface`, `operator` | UE-associated logical S1-connections (TS 36.413 clause 3.1) relayed, by operator name. |
| `ellamocn_api_request_duration_seconds` | Histogram | `method`, `route`, `code` | Duration of API requests. |
| `ellamocn_database_query_duration_seconds` | Histogram | `pool` (`write`, `read`) | Duration of database calls, statements, transaction and row scanning included. |
| `ellamocn_database_query_errors_total` | Counter | `pool` (`write`, `read`) | Failed database calls. A record not found or in conflict is a result, not a failure. |
| `ellamocn_database_storage_bytes` | Gauge | `file` (`main`, `wal`) | Size of the database on disk. |
| `ellamocn_build_info` | Gauge | `version`, `revision`, `branch`, `goversion`, `goos`, `goarch`, `tags` | Always 1. |
| `go_*` | Gauge, Counter, Summary | | Go runtime: goroutines, heap, garbage-collection pauses. |
| `process_*` | Gauge, Counter | | Process: memory, CPU, open file descriptors, start time. |

## Explanation

### The concentrator model

Ella MOCN works like a HeNB GW (TS 36.300 clause 4.6.1). Toward radios it is one MME; toward each operator it is one eNB with the Global eNB ID that operator assigned, with one SCTP association per MME (TS 36.412 clause 7). Non-UE-associated procedures end at Ella MOCN on each side (TS 36.300 clause 4.6.2), so each side sees a consistent peer:

- A radio's S1 Setup succeeds only if an operator serves one of its broadcast PLMNs ("Unknown PLMN" otherwise) and one of that operator's MMEs is up. The response lists the served GUMMEIs of every MME that is up, merged into one LTE pool and limited to each operator's PLMNs, so the list stays within 32 PLMNs. The Relative MME Capacity is 255: a radio behind a gateway connects to no other MME, so the value is never weighed against another, and 0 would mean "send nothing".
- S1 Setup toward an MME waits for a radio that broadcasts one of the operator's PLMNs, since Supported TAs cannot be empty. It carries the union of the connected radios' TAs, filtered to the operator's PLMNs, and the longest Default Paging DRX among those radios. TS 36.413 sets no rule for a gateway; the longest value is the one no radio's paging cycle exceeds. Changes are sent as ENB CONFIGURATION UPDATE.
- Paging, Overload, PWS and the other non-UE-associated procedures are logged and dropped for now.
- Learned data lasts as long as its association, so it is kept in memory only.

### UE relay

Ella MOCN relays UE-associated messages between the radio and the MME that serves the UE (TS 36.300 clause 4.6.2). Each side has its own UE-associated logical S1-connection: Ella MOCN allocates the MME UE S1AP ID toward the radio and the eNB UE S1AP ID toward the MME, and routes every message by the ID pair. Only the top-level UE S1AP ID IEs are rewritten; every other IE passes byte for byte. NAS, the Security Key and NH/NCC stay opaque: they are never decoded, logged or stored, because they belong to the UE and its operator.

A UE CONTEXT RELEASE REQUEST with a GW Context Release Indication is not relayed: a gateway releases on it only when the UE is no longer served by one of its radios (TS 36.300 clause 4.6.2), and every UE it knows is.

### MME selection

The operator is the one that serves the PLMN in the TAI of the INITIAL UE MESSAGE, the PLMN the UE selected (TS 36.413 clause 8.6.2.1). Within that operator, Ella MOCN runs the NAS Node Selection Function a radio would run (TS 36.300 clause 19.2.1.7):

1. The GUMMEI, if the radio included it. Radios behind a gateway include it, since the gateway selects the MME (TS 36.300 clause 4.6.2). The GUMMEI Type is not used: TS 23.401 clause 4.3.8.3 lets the selection rely on the GUMMEI alone.
2. The S-TMSI's MME Code, matched against the GUMMEIs that operator's MMEs serve for the selected PLMN. The merged list sent to radios is not used, since two operators may reuse an MME Code.
3. Otherwise an MME at random, weighted by its Relative MME Capacity: the probability of selecting an MME is proportional to its Weight Factor (TS 23.401 clause 4.3.7.2). An MME with capacity 0 gets no new UE, since a Weight Factor of 0 routes new entrants to the other MMEs (TS 23.401 clause 4.3.7.3). Steps 1 and 2 still select it, since they find a UE that MME already serves.

Only MMEs that are up are candidates. If no operator serves the PLMN, or the operator has no MME to select (none up, or every one with capacity 0), the INITIAL UE MESSAGE is counted and the UE is released toward the radio. INITIAL UE MESSAGE has no failure response (TS 36.413 clause 8.6.2.1), so Ella MOCN acts as the MME would and sends UE CONTEXT RELEASE COMMAND (TS 36.413 clause 8.3.3), with the UE S1AP ID pair (TS 36.413 clause 8.3.3.2). The cause is "Unknown PLMN" when no operator serves the PLMN, and "Transport Resource Unavailable" when no MME can be selected (TS 36.413 clause 9.2.1.3).

### Errors and cleanup

- **Unknown IDs.** A message with UE S1AP IDs Ella MOCN does not know, or that do not match, gets an ERROR INDICATION with the received IDs, and any connection holding one of those IDs is released, except for UE CONTEXT RELEASE COMPLETE, the last message, which is dropped (TS 36.413 clause 10.6). An INITIAL UE MESSAGE that reuses an allocated eNB UE S1AP ID is answered the same way. An ERROR INDICATION for a known pair is relayed, and one whose cause says the IDs are unknown releases the connection on both sides.
- **Release.** UE CONTEXT RELEASE COMPLETE frees the UE. For a UE that Ella MOCN releases itself, the IDs stay allocated until the radio's UE CONTEXT RELEASE COMPLETE, so the radio cannot reuse them early. If the Complete does not come within the response timeout that Ella MOCN uses for every procedure it starts, Ella MOCN sends the radio a partial RESET listing that one UE-associated logical S1-connection, with cause "Unspecified" (radio network), then frees the IDs. TS 36.413 defines no behaviour for a missing Complete (TS 36.413 clause 8.3.3.3), while a partial RESET releases the indicated connections on both sides (TS 36.413 clause 8.7.1), so the radio cannot keep a connection that Ella MOCN has forgotten. Ella MOCN does not wait for the RESET ACKNOWLEDGE.
- **Failed send to the MME.** If the INITIAL UE MESSAGE cannot be sent to the selected MME, it is counted as `error`, the MME-side connection is freed, and the UE is released toward the radio as when no MME can be selected: UE CONTEXT RELEASE COMMAND with "Transport Resource Unavailable".
- **RESET.** Reset is a non-UE-associated procedure, so it ends at Ella MOCN: it is acknowledged on the side it came from, and the UEs it covers are reset on the other side with a partial RESET per association, listing their IDs on that side (TS 36.413 clause 8.7.1). A full RESET from an MME becomes a partial RESET to each radio for that MME's UEs; a full RESET from a radio, a partial RESET to each MME for that radio's UEs.
- **Lost association.** When a radio's association drops, its UEs are reset toward their MMEs; when an MME's association drops, its UEs are reset toward their radios. The cause is "Transport Resource Unavailable". When a radio repeats S1 Setup, which re-initialises its UE contexts (TS 36.413 clause 8.7.3.1), its UEs are reset toward their MMEs with "Release due to E-UTRAN generated reason": the association is up, and the release comes from the radio.
