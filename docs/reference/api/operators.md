---
description: RESTful API reference for managing the operators that share Ella MOCN.
---

# Operators

An operator is a core network that shares the radios through Ella MOCN: its PLMNs, the Global eNB ID and Global gNB ID with which Ella MOCN presents itself to its core, and its MMEs and AMFs.

## List Operators

This path returns the operators, sorted by name, with the state of their MMEs.

| Method | Path                |
| ------ | ------------------- |
| GET    | `/api/v1/operators` |

### Response Fields

| Field                       | Type    | Description |
| --------------------------- | ------- | ----------- |
| `name`                      | string  | Name. |
| `pws_operator`              | boolean | Whether the operator's core connects the common CBC, whose warnings (PWS) are broadcast to every cell. |
| `plmns`                     | array   | PLMNs, each with `mcc` and `mnc`. |
| `global_enb_id`             | object  | Global eNB ID that Ella MOCN uses towards the operator's MMEs. `null` when unset. |
| `global_enb_id.plmn`        | object  | PLMN, with `mcc` and `mnc`. |
| `global_enb_id.enb_id_type` | string  | `macro`, `short_macro` or `long_macro`. |
| `global_enb_id.enb_id`      | int     | eNB ID. |
| `global_gnb_id`             | object  | Global gNB ID that Ella MOCN uses towards the operator's AMFs. `null` when unset. |
| `global_gnb_id.plmn`        | object  | PLMN, with `mcc` and `mnc`. |
| `global_gnb_id.gnb_id`      | int     | gNB ID. |
| `global_gnb_id.gnb_id_length` | int   | gNB ID length, in bits. |
| `mmes`                      | array   | MMEs. Empty when none. |
| `mmes[].tnl_addresses`      | array   | Addresses of the MME, reached over one SCTP association to port 36412. |
| `mmes[].status`             | object  | State of the MME. See [MME Status](#mme-status). |
| `amfs`                      | array   | AMFs. Empty when none. |
| `amfs[].tnl_addresses`      | array   | Addresses of the AMF. |
| `ue_connections`            | int     | UE-associated logical S1-connections across the operator's MMEs. |

### Sample Response

```json
{
    "result": {
        "items": [
            {
                "name": "Operator A",
                "pws_operator": true,
                "plmns": [{"mcc": "001", "mnc": "01"}],
                "global_enb_id": {
                    "plmn": {"mcc": "001", "mnc": "01"},
                    "enb_id_type": "macro",
                    "enb_id": 1
                },
                "global_gnb_id": {
                    "plmn": {"mcc": "001", "mnc": "01"},
                    "gnb_id": 1,
                    "gnb_id_length": 22
                },
                "mmes": [
                    {
                        "tnl_addresses": ["127.0.0.2"],
                        "status": {
                            "state": "up",
                            "since": "2026-11-13T16:31:55.645317884Z",
                            "primary_tnl_address": "127.0.0.2",
                            "mme_name": "mme-a",
                            "served_gummeis": [
                                {
                                    "served_plmns": [{"mcc": "001", "mnc": "01"}],
                                    "served_group_ids": [1],
                                    "served_mmecs": [1]
                                }
                            ],
                            "relative_mme_capacity": 255,
                            "overload": [
                                {
                                    "gummei": {
                                        "plmn": {"mcc": "001", "mnc": "01"},
                                        "mme_group_id": 1,
                                        "mme_code": 1
                                    },
                                    "overload_action": "reject_non_emergency_mo_dt",
                                    "traffic_load_reduction_indication": 50
                                }
                            ],
                            "ue_connections": 2
                        }
                    }
                ],
                "amfs": [{"tnl_addresses": ["10.0.1.5"]}],
                "ue_connections": 2
            },
            {
                "name": "Operator B",
                "pws_operator": false,
                "plmns": [{"mcc": "001", "mnc": "02"}],
                "global_enb_id": {
                    "plmn": {"mcc": "001", "mnc": "02"},
                    "enb_id_type": "macro",
                    "enb_id": 2
                },
                "global_gnb_id": null,
                "mmes": [
                    {
                        "tnl_addresses": ["127.0.0.3"],
                        "status": {
                            "state": "up",
                            "since": "2026-11-13T16:31:55.645358627Z",
                            "primary_tnl_address": "127.0.0.3",
                            "mme_name": "mme-b",
                            "served_gummeis": [
                                {
                                    "served_plmns": [{"mcc": "001", "mnc": "02"}],
                                    "served_group_ids": [2],
                                    "served_mmecs": [1]
                                }
                            ],
                            "relative_mme_capacity": 255,
                            "ue_connections": 2
                        }
                    }
                ],
                "amfs": [],
                "ue_connections": 2
            }
        ]
    }
}
```

### MME Status

The state of an MME, learned from its S1 association and not stored.

| Field                   | Type   | Description |
| ----------------------- | ------ | ----------- |
| `state`                 | string | `disconnected`: no SCTP association. `connected`: SCTP association up, S1 Setup not completed. `up`: S1 Setup succeeded. `setup_failed`: the MME answered S1 SETUP FAILURE. |
| `since`                 | string | When the MME entered its current state. |
| `primary_tnl_address`   | string | The MME address the SCTP association sends to. Absent without an association. |
| `mme_name`              | string | MME Name from the S1 SETUP RESPONSE or MME CONFIGURATION UPDATE. Absent when the MME sent none. |
| `served_gummeis`        | array  | Served GUMMEIs from the S1 SETUP RESPONSE or MME CONFIGURATION UPDATE. Absent before the S1 Setup. |
| `served_gummeis[].served_plmns`     | array | Served PLMNs, each with `mcc` and `mnc`. |
| `served_gummeis[].served_group_ids` | array | Served MME group IDs, `0` to `65535`. |
| `served_gummeis[].served_mmecs`     | array | Served MME codes, `0` to `255`. |
| `relative_mme_capacity` | int    | Relative MME Capacity, `0` to `255`. Absent before the S1 Setup. |
| `overload`              | array  | The GUMMEIs of the MME with an ongoing overload action, from OVERLOAD START and OVERLOAD STOP. Absent when none. Cleared when the S1 association is lost. |
| `overload[].gummei`     | object | GUMMEI: `plmn`, `mme_group_id` (`0` to `65535`) and `mme_code` (`0` to `255`). |
| `overload[].overload_action` | string | Overload Action from the last OVERLOAD START that applies to the GUMMEI: `reject_non_emergency_mo_dt`, `reject_rrc_cr_signalling`, `permit_emergency_sessions_and_mobile_terminated_services_only`, `permit_high_priority_sessions_and_mobile_terminated_services_only`, `reject_delay_tolerant_access`, `permit_high_priority_sessions_and_exception_reporting_and_mobile_terminated_services_only` or `not_accept_mo_data_or_delay_tolerant_access_from_cp_ciot`. |
| `overload[].traffic_load_reduction_indication` | int | Traffic Load Reduction Indication from the same OVERLOAD START: the percentage of the traffic to reject, `1` to `99`. Absent when the MME did not include it. |
| `ue_connections`        | int    | UE-associated logical S1-connections relayed to the MME. |

## Get an Operator

This path returns an operator, with the state of its MMEs and its UE connections by radio.

| Method | Path                       |
| ------ | -------------------------- |
| GET    | `/api/v1/operators/{name}` |

### Path Parameters

| Name   | Type   | Description |
| ------ | ------ | ----------- |
| `name` | string | Operator name. |

### Response Fields

The fields of [List Operators](#list-operators), plus:

| Field                                      | Type   | Description |
| ------------------------------------------ | ------ | ----------- |
| `ue_connections_by_radio`                  | array  | UE-associated logical S1-connections of the operator, by radio. Lists every radio that broadcasts one of the operator's PLMNs, and every other radio with UE connections of the operator, sorted by name, or by `ref` for a radio without a name. |
| `ue_connections_by_radio[].radio`          | string | The radio's [`ref`](radios.md#list-radios). |
| `ue_connections_by_radio[].name`           | string | The radio's eNB Name. Absent when the radio sent none. |
| `ue_connections_by_radio[].ue_connections` | int    | UE-associated logical S1-connections. |

### Response Codes

| Code  | Description |
| ----- | ----------- |
| `404` | No operator has that name. |

### Sample Response

```json
{
    "result": {
        "name": "Operator B",
        "pws_operator": false,
        "plmns": [{"mcc": "001", "mnc": "02"}],
        "global_enb_id": {
            "plmn": {"mcc": "001", "mnc": "02"},
            "enb_id_type": "macro",
            "enb_id": 2
        },
        "global_gnb_id": null,
        "mmes": [
            {
                "tnl_addresses": ["127.0.0.3"],
                "status": {
                    "state": "up",
                    "since": "2026-11-13T16:31:55.645358627Z",
                    "primary_tnl_address": "127.0.0.3",
                    "mme_name": "mme-b",
                    "served_gummeis": [
                        {
                            "served_plmns": [{"mcc": "001", "mnc": "02"}],
                            "served_group_ids": [2],
                            "served_mmecs": [1]
                        }
                    ],
                    "relative_mme_capacity": 255,
                    "ue_connections": 2
                }
            }
        ],
        "amfs": [],
        "ue_connections": 2,
        "ue_connections_by_radio": [
            {"radio": "001-01:macro:411", "name": "enb-north", "ue_connections": 1},
            {"radio": "001-01:macro:412", "name": "enb-south", "ue_connections": 1}
        ]
    }
}
```

## Create an Operator

This path returns the operator, without `mmes[].status`, `ue_connections` and `ue_connections_by_radio`.

| Method | Path                |
| ------ | ------------------- |
| POST   | `/api/v1/operators` |

### Request Body

| Field                         | Type    | Required | Description |
| ----------------------------- | ------- | -------- | ----------- |
| `name`                        | string  | Yes      | 1 to 64 characters, after leading and trailing spaces are removed. Unique. |
| `pws_operator`                | boolean | No       | Default `false`. At most one operator has `true`. |
| `plmns`                       | array   | Yes      | 1 to 12 PLMNs, each listed once, and owned by no other operator. Operators with MMEs have at most 32 PLMNs in total, and operators with AMFs at most 12. |
| `plmns[].mcc`                 | string  | Yes      | 3 digits. |
| `plmns[].mnc`                 | string  | Yes      | 2 or 3 digits. |
| `global_enb_id`               | object  | No       | `null` or absent for none. Required with `mmes`. |
| `global_enb_id.plmn`          | object  | Yes      | One of `plmns`. |
| `global_enb_id.enb_id_type`   | string  | Yes      | `macro`, `short_macro` or `long_macro`. |
| `global_enb_id.enb_id`        | int     | Yes      | `0` to `1048575` for `macro`, `262143` for `short_macro`, `2097151` for `long_macro`. |
| `global_gnb_id`               | object  | No       | `null` or absent for none. Required with `amfs`. |
| `global_gnb_id.plmn`          | object  | Yes      | One of `plmns`. |
| `global_gnb_id.gnb_id`        | int     | Yes      | `0` to 2^`gnb_id_length` - 1. |
| `global_gnb_id.gnb_id_length` | int     | Yes      | `22` to `32`. |
| `mmes`                        | array   | No       | MMEs. |
| `mmes[].tnl_addresses`        | array   | Yes      | At least one IPv4 or IPv6 address, without a zone. An address appears at most once across the operator's MMEs. Returned in canonical form. |
| `amfs`                        | array   | No       | AMFs. |
| `amfs[].tnl_addresses`        | array   | Yes      | At least one IPv4 or IPv6 address, without a zone. An address appears at most once across the operator's AMFs. Returned in canonical form. |

The read-only fields of the responses, `mmes[].status`, `ue_connections` and `ue_connections_by_radio`, are ignored. Other unknown fields get `400`.

### Response Codes

| Code  | Description |
| ----- | ----------- |
| `201` | Created. |
| `400` | Invalid field. |
| `409` | Another operator has the name or one of the PLMNs, or is the PWS operator. |

### Sample Request

```json
{
    "name": "Operator A",
    "pws_operator": true,
    "plmns": [{"mcc": "001", "mnc": "01"}],
    "global_enb_id": {"plmn": {"mcc": "001", "mnc": "01"}, "enb_id_type": "macro", "enb_id": 1},
    "global_gnb_id": {"plmn": {"mcc": "001", "mnc": "01"}, "gnb_id": 1, "gnb_id_length": 22},
    "mmes": [{"tnl_addresses": ["127.0.0.2"]}],
    "amfs": [{"tnl_addresses": ["10.0.1.5"]}]
}
```

### Sample Response

```json
{
    "result": {
        "name": "Operator A",
        "pws_operator": true,
        "plmns": [{"mcc": "001", "mnc": "01"}],
        "global_enb_id": {
            "plmn": {"mcc": "001", "mnc": "01"},
            "enb_id_type": "macro",
            "enb_id": 1
        },
        "global_gnb_id": {
            "plmn": {"mcc": "001", "mnc": "01"},
            "gnb_id": 1,
            "gnb_id_length": 22
        },
        "mmes": [{"tnl_addresses": ["127.0.0.2"]}],
        "amfs": [{"tnl_addresses": ["10.0.1.5"]}]
    }
}
```

## Update an Operator

This path replaces the whole operator. A different `name` renames it. It takes the request body of [Create an Operator](#create-an-operator), and returns the operator the same way.

| Method | Path                       |
| ------ | -------------------------- |
| PUT    | `/api/v1/operators/{name}` |

### Response Codes

| Code  | Description |
| ----- | ----------- |
| `400` | Invalid field. |
| `404` | No operator has that name. |
| `409` | Another operator has the name or one of the PLMNs, or is the PWS operator. |

### Sample Request

```json
{
    "name": "Operator D",
    "plmns": [{"mcc": "001", "mnc": "04"}]
}
```

### Sample Response

```json
{
    "result": {
        "name": "Operator D",
        "pws_operator": false,
        "plmns": [{"mcc": "001", "mnc": "04"}],
        "global_enb_id": null,
        "global_gnb_id": null,
        "mmes": [],
        "amfs": []
    }
}
```

## Delete an Operator

| Method | Path                       |
| ------ | -------------------------- |
| DELETE | `/api/v1/operators/{name}` |

### Response Codes

| Code  | Description |
| ----- | ----------- |
| `404` | No operator has that name. |

### Sample Response

```json
{
    "result": {
        "message": "Operator deleted successfully"
    }
}
```
