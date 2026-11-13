---
description: RESTful API reference for viewing the UE connections that Ella MOCN relays.
---

# UE Connections

A UE connection is a UE-associated logical S1-connection or NG-connection that Ella MOCN relays between a radio and an operator's MME or AMF. UE connections are learned at runtime, not stored.

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
| `interface`            | string | `s1` or `ng`. |
| `radio`                | object | The connection between the radio and Ella MOCN. |
| `radio.ref`            | string | The radio's [`ref`](radios.md#list-radios). |
| `radio.enb_ue_s1ap_id` | int    | S1 only. eNB UE S1AP ID, allocated by the radio. `0` to `16777215`. |
| `radio.mme_ue_s1ap_id` | int    | S1 only. MME UE S1AP ID, allocated by Ella MOCN. `0` to `4294967295`. |
| `radio.ran_ue_ngap_id` | int    | NG only. RAN UE NGAP ID, allocated by the radio. `0` to `4294967295`. |
| `radio.amf_ue_ngap_id` | int    | NG only. AMF UE NGAP ID, allocated by Ella MOCN. `0` to `1099511627775`. |
| `mme`                  | object | S1 only. The S1 connection between Ella MOCN and the operator's MME. |
| `mme.tnl_addresses`    | array  | The MME's TNL addresses, as configured on the operator. |
| `mme.enb_ue_s1ap_id`   | int    | eNB UE S1AP ID, allocated by Ella MOCN. `0` to `16777215`. |
| `mme.mme_ue_s1ap_id`   | int    | MME UE S1AP ID, allocated by the MME. `null` until the MME's first message for the UE. |
| `amf`                  | object | NG only. The NG connection between Ella MOCN and the operator's AMF. |
| `amf.tnl_addresses`    | array  | The AMF's TNL addresses, as configured on the operator. |
| `amf.ran_ue_ngap_id`   | int    | RAN UE NGAP ID, allocated by Ella MOCN. `0` to `4294967295`. |
| `amf.amf_ue_ngap_id`   | int    | AMF UE NGAP ID, allocated by the AMF. `null` until the AMF's first message for the UE. |
| `operator`             | string | Name of the operator that serves the selected PLMN. |
| `plmn`                 | object | The selected PLMN, with `mcc` and `mnc`. |
| `since`                | string | When the radio sent the INITIAL UE MESSAGE. |
| `tai`                  | object | The last TAI the radio reported for the UE: `plmn` and `tac`. `null` when none. |
| `ecgi`                 | object | S1 only. The last E-UTRAN CGI the radio reported for the UE: `plmn` and `cell_id`, the 28-bit E-UTRAN Cell Identity. Absent when none. |
| `nr_cgi`               | object | NG only. The last NR CGI the radio reported for the UE: `plmn` and `cell_id`, the 36-bit NR Cell Identity. Absent when none. |

### Sample Response

```json
{
    "result": {
        "items": [
            {
                "interface": "s1",
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
                "interface": "ng",
                "radio": {
                    "ref": "001-01:gnb22:1",
                    "ran_ue_ngap_id": 0,
                    "amf_ue_ngap_id": 2
                },
                "amf": {
                    "tnl_addresses": ["127.0.0.2"],
                    "ran_ue_ngap_id": 2,
                    "amf_ue_ngap_id": 101
                },
                "operator": "Operator A",
                "plmn": {"mcc": "001", "mnc": "01"},
                "since": "2026-11-13T16:31:56.646102232Z",
                "tai": {
                    "plmn": {"mcc": "001", "mnc": "01"},
                    "tac": 1
                },
                "nr_cgi": {
                    "plmn": {"mcc": "001", "mnc": "01"},
                    "cell_id": 16384
                }
            }
        ]
    }
}
```
