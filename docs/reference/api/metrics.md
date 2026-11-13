---
description: RESTful API reference for the Prometheus metrics of Ella MOCN.
---

# Metrics

## Get Metrics

This path returns the metrics of the MOCN in a [Prometheus exposition format](https://prometheus.io/docs/instrumenting/exposition_formats/) negotiated from the request's `Accept` header: the protobuf format (`application/vnd.google.protobuf`, with encoding `delimited`, `text` or `compact-text`), or else the text format 0.0.4 (`text/plain`). The body is compressed with gzip when `Accept-Encoding` asks for it. See [Observability](../observability.md) for the list.

| Method | Path              |
| ------ | ----------------- |
| GET    | `/api/v1/metrics` |

### Response Codes

| Code  | Description |
| ----- | ----------- |
| `500` | No metric could be gathered. |
| `503` | 32 requests already in flight, or gathering took more than 8 seconds. |

### Sample Response

```
# HELP ellamocn_overload_messages_total OVERLOAD START and OVERLOAD STOP messages received from MMEs, by interface (s1), procedure (overload_start, overload_stop) and result (relayed: sent to the radios that broadcast one of the operator's PLMNs; no_radio: no such radio is up; ignored: OVERLOAD STOP for GUMMEIs with no ongoing overload action; invalid: undecodable or with an invalid GUMMEI).
# TYPE ellamocn_overload_messages_total counter
ellamocn_overload_messages_total{interface="s1",procedure="overload_start",result="invalid"} 0
ellamocn_overload_messages_total{interface="s1",procedure="overload_start",result="no_radio"} 0
ellamocn_overload_messages_total{interface="s1",procedure="overload_start",result="relayed"} 1
ellamocn_overload_messages_total{interface="s1",procedure="overload_stop",result="ignored"} 0
ellamocn_overload_messages_total{interface="s1",procedure="overload_stop",result="invalid"} 0
ellamocn_overload_messages_total{interface="s1",procedure="overload_stop",result="no_radio"} 0
ellamocn_overload_messages_total{interface="s1",procedure="overload_stop",result="relayed"} 0
# HELP ellamocn_radio_associations Radio (eNodeB) associations, by interface (s1) and state (connected: SCTP association up, no successful S1 Setup; up: S1 Setup succeeded).
# TYPE ellamocn_radio_associations gauge
ellamocn_radio_associations{interface="s1",state="connected"} 0
ellamocn_radio_associations{interface="s1",state="up"} 2
# HELP ellamocn_ue_connections UE-associated logical S1-connections (TS 36.413 clause 3.1) relayed, by interface (s1) and operator.
# TYPE ellamocn_ue_connections gauge
ellamocn_ue_connections{interface="s1",operator="Operator A"} 2
ellamocn_ue_connections{interface="s1",operator="Operator B"} 2
```
