---
description: RESTful API reference for viewing the UE connections that Ella MOCN relays.
---

# UE Connections

A UE connection is a UE-associated logical S1-connection that Ella MOCN relays between a radio and an operator's MME. UE connections are learned at runtime, not stored.

## List UE Connections

This path returns the UE connections, oldest first.

| Method | Path                     |
| ------ | ------------------------ |
| GET    | `/api/v1/ue-connections` |

### Query Parameters

| Name       | In    | Type   | Default | Allowed | Description |
| ---------- | ----- | ------ | ------- | ------- | ----------- |
| `radio`    | query | string |         |         | Only the UE connections of the radio with this [`ref`](radios.md#list-radios). |
| `operator` | query | string |         |         | Only the UE connections of the [operator](operators.md) with this name. |

### Response Fields

| Field                  | Type   | Description |
| ---------------------- | ------ | ----------- |
| `radio`                | object | The S1 connection between the radio and Ella MOCN. |
| `radio.ref`            | string | The radio's [`ref`](radios.md#list-radios). |
| `radio.enb_ue_s1ap_id` | int    | eNB UE S1AP ID, allocated by the radio. `0` to `16777215`. |
| `radio.mme_ue_s1ap_id` | int    | MME UE S1AP ID, allocated by Ella MOCN. `0` to `4294967295`. |
| `mme`                  | object | The S1 connection between Ella MOCN and the operator's MME. |
| `mme.tnl_addresses`    | array  | The MME's TNL addresses, as configured on the operator. |
| `mme.enb_ue_s1ap_id`   | int    | eNB UE S1AP ID, allocated by Ella MOCN. `0` to `16777215`. |
| `mme.mme_ue_s1ap_id`   | int    | MME UE S1AP ID, allocated by the MME. `null` until the MME's first message for the UE. |
| `operator`             | string | Name of the operator that serves the selected PLMN. |
| `plmn`                 | object | The selected PLMN, with `mcc` and `mnc`. |
| `since`                | string | When the radio sent the INITIAL UE MESSAGE. |
| `tai`                  | object | The last TAI the radio reported for the UE: `plmn` and `tac`. `null` when none. |
| `ecgi`                 | object | The last E-UTRAN CGI the radio reported for the UE: `plmn` and `cell_id`, the 28-bit E-UTRAN Cell Identity. `null` when none. |

### Sample Response

```json
{
    "result": {
        "items": [
            {
                "radio": {
                    "ref": "001-01:macro:412",
                    "enb_ue_s1ap_id": 1,
                    "mme_ue_s1ap_id": 1
                },
                "mme": {
                    "tnl_addresses": ["127.0.0.3"],
                    "enb_ue_s1ap_id": 1,
                    "mme_ue_s1ap_id": 101
                },
                "operator": "Operator B",
                "plmn": {"mcc": "001", "mnc": "02"},
                "since": "2026-11-13T16:31:56.646031138Z",
                "tai": {
                    "plmn": {"mcc": "001", "mnc": "02"},
                    "tac": 1
                },
                "ecgi": {
                    "plmn": {"mcc": "001", "mnc": "02"},
                    "cell_id": 105473
                }
            },
            {
                "radio": {
                    "ref": "001-01:macro:411",
                    "enb_ue_s1ap_id": 1,
                    "mme_ue_s1ap_id": 2
                },
                "mme": {
                    "tnl_addresses": ["127.0.0.2"],
                    "enb_ue_s1ap_id": 2,
                    "mme_ue_s1ap_id": 101
                },
                "operator": "Operator A",
                "plmn": {"mcc": "001", "mnc": "01"},
                "since": "2026-11-13T16:31:56.646102232Z",
                "tai": {
                    "plmn": {"mcc": "001", "mnc": "01"},
                    "tac": 1
                },
                "ecgi": {
                    "plmn": {"mcc": "001", "mnc": "01"},
                    "cell_id": 105217
                }
            }
        ]
    }
}
```
