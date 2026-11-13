---
description: RESTful API reference for viewing the radios connected to Ella MOCN.
---

# Radios

Radios are the eNodeBs with an S1 association to Ella MOCN. They are learned at runtime, not stored.

## List Radios

This path returns the radios, oldest association first.

| Method | Path             |
| ------ | ---------------- |
| GET    | `/api/v1/radios` |

### Response Fields

| Field                              | Type   | Description |
| ---------------------------------- | ------ | ----------- |
| `ref`                              | string | Radio identity, and the path segment that addresses it: `<mcc>-<mnc>:<enb_id_type>:<enb_id>`. Absent while `global_enb_id` is `null`. |
| `global_enb_id`                    | object | Global eNB ID from the S1 SETUP REQUEST. `null` before the radio sends one, or after it is rejected. |
| `global_enb_id.plmn`               | object | PLMN, with `mcc` and `mnc`. |
| `global_enb_id.enb_id_type`        | string | `macro`, `home`, `short_macro` or `long_macro`. |
| `global_enb_id.enb_id`             | int    | eNB ID. |
| `enb_name`                         | string | eNB Name. Absent when the radio sent none. |
| `supported_tas`                    | array  | Supported TAs. |
| `supported_tas[].tac`              | int    | Tracking Area Code, `0` to `65535`. |
| `supported_tas[].broadcast_plmns`  | array  | Broadcast PLMNs, each with `mcc` and `mnc`. |
| `default_paging_drx`               | int    | Default paging DRX, in radio frames: `32`, `64`, `128` or `256`. Absent while `global_enb_id` is `null`. |
| `state`                            | string | `connected`: SCTP association up, no successful S1 Setup. `up`: S1 Setup succeeded. |
| `connected_since`                  | string | When the SCTP association was established. |
| `tnl_addresses`                    | array  | Addresses of the radio. |
| `ue_connections`                   | int    | UE-associated logical S1-connections from the radio. |

### Sample Response

```json
{
    "result": {
        "items": [
            {
                "ref": "001-01:macro:412",
                "global_enb_id": {
                    "plmn": {"mcc": "001", "mnc": "01"},
                    "enb_id_type": "macro",
                    "enb_id": 412
                },
                "enb_name": "enb-south",
                "supported_tas": [
                    {
                        "tac": 1,
                        "broadcast_plmns": [
                            {"mcc": "001", "mnc": "01"},
                            {"mcc": "001", "mnc": "02"}
                        ]
                    }
                ],
                "default_paging_drx": 128,
                "state": "up",
                "connected_since": "2026-11-13T16:31:55.645045565Z",
                "tnl_addresses": ["127.0.0.1"],
                "ue_connections": 1
            },
            {
                "ref": "001-01:macro:411",
                "global_enb_id": {
                    "plmn": {"mcc": "001", "mnc": "01"},
                    "enb_id_type": "macro",
                    "enb_id": 411
                },
                "enb_name": "enb-north",
                "supported_tas": [
                    {
                        "tac": 1,
                        "broadcast_plmns": [
                            {"mcc": "001", "mnc": "01"},
                            {"mcc": "001", "mnc": "02"}
                        ]
                    }
                ],
                "default_paging_drx": 128,
                "state": "up",
                "connected_since": "2026-11-13T16:31:55.645073005Z",
                "tnl_addresses": ["127.0.0.1"],
                "ue_connections": 3
            }
        ]
    }
}
```

## Get a Radio

This path returns a radio, with its UE connections by operator. To list its UE connections, use [`GET /api/v1/ue-connections?radio={ref}`](ue_connections.md#list-ue-connections).

| Method | Path                   |
| ------ | ---------------------- |
| GET    | `/api/v1/radios/{ref}` |

### Path Parameters

| Name  | Type   | Description |
| ----- | ------ | ----------- |
| `ref` | string | Radio identity, as returned in a radio's `ref` field: `<mcc>-<mnc>:<enb_id_type>:<enb_id>`, for example `001-01:macro:411`. |

### Response Fields

The fields of [List Radios](#list-radios), plus:

| Field                                         | Type   | Description |
| --------------------------------------------- | ------ | ----------- |
| `ue_connections_by_operator`                  | array  | UE-associated logical S1-connections from the radio, by operator. Lists every operator that owns a PLMN the radio broadcasts, and every other operator with UE connections from the radio, sorted by operator name. Absent when empty. |
| `ue_connections_by_operator[].operator`       | string | Operator name. |
| `ue_connections_by_operator[].ue_connections` | int    | UE-associated logical S1-connections. |

### Response Codes

| Code  | Description |
| ----- | ----------- |
| `404` | No radio has that `ref`. |

### Sample Response

```json
{
    "result": {
        "ref": "001-01:macro:412",
        "global_enb_id": {
            "plmn": {"mcc": "001", "mnc": "01"},
            "enb_id_type": "macro",
            "enb_id": 412
        },
        "enb_name": "enb-south",
        "supported_tas": [
            {
                "tac": 1,
                "broadcast_plmns": [
                    {"mcc": "001", "mnc": "01"},
                    {"mcc": "001", "mnc": "02"}
                ]
            }
        ],
        "default_paging_drx": 128,
        "state": "up",
        "connected_since": "2026-11-13T16:31:55.645045565Z",
        "tnl_addresses": ["127.0.0.1"],
        "ue_connections": 1,
        "ue_connections_by_operator": [
            {"operator": "Operator A", "ue_connections": 0},
            {"operator": "Operator B", "ue_connections": 1}
        ]
    }
}
```
