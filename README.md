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
| `GET /api/v1/operators/{name}` | `mmes[].status.state` | `disconnected`, `connected` (SCTP up, no S1 Setup), `up`, `setup_failed` |
| | `mmes[].status.since` | Time the MME entered its state |
| | `mmes[].status.primary_tnl_address` | MME address in use |
| | `mmes[].status.mme_name`, `served_gummeis`, `relative_mme_capacity` | Learned from S1 SETUP RESPONSE and MME CONFIGURATION UPDATE |
| `GET /api/v1/radios`, `GET /api/v1/radios/{ref}` | `ref` | `<mcc>-<mnc>:<enb_id_type>:<enb_id>`, set once the radio has a Global eNB ID |
| | `global_enb_id`, `enb_name`, `supported_tas`, `default_paging_drx` | From the radio's S1 SETUP REQUEST and ENB CONFIGURATION UPDATE |
| | `state` | `connected` (SCTP up, no S1 Setup), `up` |
| | `connected_since`, `tnl_addresses` | SCTP association |

### Metrics

| Metric | Labels |
|---|---|
| `ellamocn_s1_associations` | `side` (`ran`, `mme`), `state` |
| `ellamocn_radios_connected` | |
| `ellamocn_s1_setup_total` | `side` (`ran`, `mme`), `outcome` (`success`, `failure`) |

## Explanation

### The concentrator model

Ella MOCN works like a HeNB GW (TS 36.300 clause 4.6.1). Toward radios it is one MME; toward each operator it is one eNB with the Global eNB ID that operator assigned, with one SCTP association per MME (TS 36.412 clause 7). Non-UE-associated procedures end at Ella MOCN on each side (TS 36.300 clause 4.6.2), so each side sees a consistent peer:

- A radio's S1 Setup succeeds only if an operator serves one of its broadcast PLMNs ("Unknown PLMN" otherwise) and one of that operator's MMEs is up. The response lists the served GUMMEIs of every MME that is up, merged into one LTE pool and limited to each operator's PLMNs, so the list stays within 32 PLMNs. The Relative MME Capacity is 255: a radio behind a gateway connects to no other MME, so the value is never weighed against another, and 0 would mean "send nothing".
- S1 Setup toward an MME waits for a radio that broadcasts one of the operator's PLMNs, since Supported TAs cannot be empty. It carries the union of the connected radios' TAs, filtered to the operator's PLMNs, and the longest Default Paging DRX among those radios. TS 36.413 sets no rule for a gateway; the longest value is the one no radio's paging cycle exceeds. Changes are sent as ENB CONFIGURATION UPDATE.
- UE-associated messages are not relayed yet, so no UE context exists. A RESET from either side is acknowledged on that side alone, since there is nothing to release (TS 36.413 clause 8.7.1). ERROR INDICATION and the other non-UE-associated procedures are logged and dropped.
- Learned data lasts as long as its association, so it is kept in memory only.
