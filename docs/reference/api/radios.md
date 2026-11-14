---
description: RESTful API reference for viewing the radios connected to Ella MOCN.
---

# Radios

Radios are the eNodeBs with an S1 association and the gNodeBs with an NG association to Ella MOCN. They are learned at runtime, not stored.

## List Radios

This path returns the radios, oldest association first.

| Method | Path             |
| ------ | ---------------- |
| GET    | `/api/v1/radios` |

### Query Parameters

| Name       | In    | Type | Default | Allowed | Description |
| ---------- | ----- | ---- | ------- | ------- | ----------- |
| `page`     | query | int  | `1`     | `>= 1`  | 1-based page index. |
| `per_page` | query | int  | `25`    | `1…100` | Number of items per page. |

### Response Fields

`items` holds the radios of the page, `page` and `per_page` echo the query, and `total_count` is the number of radios. Each radio has:

| Field                              | Type   | Description |
| ---------------------------------- | ------ | ----------- |
| `ref`                              | string | Radio identity, and the path segment that addresses it: `<type>:<mcc>-<mnc>:<id>`, as Ella Core names radios. See [Radio Ref](#radio-ref). Absent while the radio has no Global eNB ID or Global gNB ID. |
| `type`                             | string | 3GPP node type: `eNB` over S1 or `gNB` over NG. |
| `global_enb_id`                    | object | Global eNB ID from the S1 SETUP REQUEST. Absent for a gNB, before the radio sends one, or after it is rejected. |
| `global_enb_id.plmn`               | object | PLMN, with `mcc` and `mnc`. |
| `global_enb_id.enb_id_type`        | string | `macro`, `home`, `short_macro` or `long_macro`. |
| `global_enb_id.enb_id`             | int    | eNB ID: 20 bits for `macro`, 28 for `home`, 18 for `short_macro`, 21 for `long_macro`. |
| `global_gnb_id`                    | object | Global gNB ID from the NG SETUP REQUEST. Absent for an eNB, before the radio sends one, or after it is rejected. |
| `global_gnb_id.plmn`               | object | PLMN, with `mcc` and `mnc`. |
| `global_gnb_id.gnb_id`             | int    | gNB ID, less than 2^`bit_length`. |
| `global_gnb_id.bit_length`         | int    | gNB ID length, in bits: `22` to `32`. |
| `name`                             | string | eNB Name of an eNB, or RAN Node Name of a gNB. Absent when the radio sent none. |
| `supported_tas`                    | array  | Supported TAs. |
| `supported_tas[].tac`              | string | Tracking Area Code: 6 lower-case hexadecimal digits, for example `000001`. The 16-bit TAC of an eNB is in the last 4 digits. |
| `supported_tas[].broadcast_plmns`  | array  | Broadcast PLMNs. |
| `supported_tas[].broadcast_plmns[].plmn` | object | PLMN, with `mcc` and `mnc`. |
| `supported_tas[].broadcast_plmns[].snssais` | array | NG only. TAI Slice Support List of the PLMN in the tracking area: S-NSSAIs, each with `sst` (`0` to `255`) and `sd` (6 hex digits, absent when none). Absent for an eNB. |
| `default_paging_drx`               | int    | Default paging DRX, in radio frames: `32`, `64`, `128` or `256`. Absent before the setup. |
| `state`                            | string | `connected`: SCTP association up, no successful S1 or NG Setup. `up`: S1 or NG Setup succeeded. |
| `connected_at`                     | string | When the SCTP association was established. |
| `tnl_addresses`                    | array  | Addresses of the radio. |
| `ue_connections`                   | int    | UE-associated logical S1-connections or NG-connections from the radio. |

### Radio Ref

| Radio | `<type>` | `<id>` | Example |
| ----- | -------- | ------ | ------- |
| eNB, `macro`       | `eNB` | `MacroeNB-` and the eNB ID in lower-case hexadecimal, 5 digits. | `eNB:001-01:MacroeNB-0019b` for eNB ID 411. |
| eNB, `home`        | `eNB` | `HomeeNB-` and the eNB ID in lower-case hexadecimal, 7 digits. | `eNB:001-01:HomeeNB-000019b` for eNB ID 411. |
| eNB, `short_macro` | `eNB` | `SMacroeNB-` and the eNB ID in lower-case hexadecimal, 5 digits. | `eNB:001-01:SMacroeNB-0019b` for eNB ID 411. |
| eNB, `long_macro`  | `eNB` | `LMacroeNB-` and the eNB ID in lower-case hexadecimal, 6 digits. | `eNB:001-01:LMacroeNB-00019b` for eNB ID 411. |
| gNB                | `gNB` | The gNB ID in lower-case hexadecimal, zero-padded to `bit_length` / 4 digits rounded up, then `@` and `bit_length`. | `gNB:001-01:000001@22` for gNB ID 1 of 22 bits, `gNB:001-01:00019c@24` for gNB ID 412 of 24 bits. |

`<mcc>-<mnc>` is the PLMN of the Global eNB ID or Global gNB ID.

### Sample Response

```json
{
    "result": {
        "items": [
            {
                "ref": "eNB:001-01:MacroeNB-0019c",
                "type": "eNB",
                "global_enb_id": {
                    "plmn": {"mcc": "001", "mnc": "01"},
                    "enb_id_type": "macro",
                    "enb_id": 412
                },
                "name": "enb-south",
                "supported_tas": [
                    {
                        "tac": "000001",
                        "broadcast_plmns": [
                            {"plmn": {"mcc": "001", "mnc": "01"}},
                            {"plmn": {"mcc": "001", "mnc": "02"}}
                        ]
                    }
                ],
                "default_paging_drx": 128,
                "state": "up",
                "connected_at": "2026-11-13T16:31:55.645045565Z",
                "tnl_addresses": ["127.0.0.1"],
                "ue_connections": 1
            },
            {
                "ref": "eNB:001-01:MacroeNB-0019b",
                "type": "eNB",
                "global_enb_id": {
                    "plmn": {"mcc": "001", "mnc": "01"},
                    "enb_id_type": "macro",
                    "enb_id": 411
                },
                "name": "enb-north",
                "supported_tas": [
                    {
                        "tac": "000001",
                        "broadcast_plmns": [
                            {"plmn": {"mcc": "001", "mnc": "01"}},
                            {"plmn": {"mcc": "001", "mnc": "02"}}
                        ]
                    }
                ],
                "default_paging_drx": 128,
                "state": "up",
                "connected_at": "2026-11-13T16:31:55.645073005Z",
                "tnl_addresses": ["127.0.0.1"],
                "ue_connections": 3
            },
            {
                "ref": "gNB:001-01:000001@22",
                "type": "gNB",
                "global_gnb_id": {
                    "plmn": {"mcc": "001", "mnc": "01"},
                    "gnb_id": 1,
                    "bit_length": 22
                },
                "name": "gnb-east",
                "supported_tas": [
                    {
                        "tac": "000001",
                        "broadcast_plmns": [
                            {
                                "plmn": {"mcc": "001", "mnc": "01"},
                                "snssais": [{"sst": 1}, {"sst": 2, "sd": "000001"}]
                            }
                        ]
                    }
                ],
                "default_paging_drx": 128,
                "state": "up",
                "connected_at": "2026-11-13T16:31:55.645102117Z",
                "tnl_addresses": ["127.0.0.1"],
                "ue_connections": 1
            }
        ],
        "page": 1,
        "per_page": 25,
        "total_count": 3
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
| `ref` | string | Radio identity, as returned in a radio's `ref` field, for example `eNB:001-01:MacroeNB-0019b` or `gNB:001-01:000001@22`. See [Radio Ref](#radio-ref). `:` and `@` can be percent-encoded. |

### Response Fields

The fields of [List Radios](#list-radios), plus:

| Field                                         | Type   | Description |
| --------------------------------------------- | ------ | ----------- |
| `ue_connections_by_operator`                  | array  | UE-associated logical S1-connections or NG-connections from the radio, by operator. Lists every operator that owns a PLMN the radio broadcasts, and every other operator with UE connections from the radio, sorted by operator name. Absent when empty. |
| `ue_connections_by_operator[].operator`       | string | Operator name. |
| `ue_connections_by_operator[].ue_connections` | int    | UE-associated logical S1-connections or NG-connections. |

### Response Codes

| Code  | Description |
| ----- | ----------- |
| `404` | No radio has that `ref`. |

### Sample Response

```json
{
    "result": {
        "ref": "eNB:001-01:MacroeNB-0019c",
        "type": "eNB",
        "global_enb_id": {
            "plmn": {"mcc": "001", "mnc": "01"},
            "enb_id_type": "macro",
            "enb_id": 412
        },
        "name": "enb-south",
        "supported_tas": [
            {
                "tac": "000001",
                "broadcast_plmns": [
                    {"plmn": {"mcc": "001", "mnc": "01"}},
                    {"plmn": {"mcc": "001", "mnc": "02"}}
                ]
            }
        ],
        "default_paging_drx": 128,
        "state": "up",
        "connected_at": "2026-11-13T16:31:55.645045565Z",
        "tnl_addresses": ["127.0.0.1"],
        "ue_connections": 1,
        "ue_connections_by_operator": [
            {"operator": "Operator A", "ue_connections": 0},
            {"operator": "Operator B", "ue_connections": 1}
        ]
    }
}
```
