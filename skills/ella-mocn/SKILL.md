---
name: ella-mocn
description: >
  Manages and inspects a running Ella MOCN (multi-operator core network gateway that shares
  4G eNBs and 5G gNBs between operators, relaying S1 and NG to each operator's MMEs and AMFs)
  through its REST API. Use when the user asks about operators, their PLMNs, Global eNB and
  gNB IDs, MMEs, AMFs and their state, connected radios, radios down or failing setup, how
  many phones or UEs each operator or radio serves, UE connections, user-plane aggregation,
  core overload, users and API tokens, or metrics of an Ella MOCN instance.
---

# Ella MOCN

## Connection

- Base URL: `ELLA_MOCN_URL`, e.g. `http://10.0.0.5:5030`. The API listens on port `5030` by default, over plain HTTP unless `api.tls` is set in the configuration file; use `curl -k` for a self-signed `https://` URL.
- Token: `ELLA_MOCN_TOKEN`, either an API token (starts with `ellamocn_`, does not expire unless set to) or an access token from login (valid 15 minutes).

Ask the user for anything not in the environment or the conversation. If the user gives an email and password instead of a token, log in once and reuse the token (login is rate-limited):

```bash
ELLA_MOCN_TOKEN=$(curl -s -X POST -H 'Content-Type: application/json' \
  -d '{"email":"admin@example.com","password":"..."}' \
  "$ELLA_MOCN_URL/api/v1/auth/login" | jq -r .result.token)
```

On a `401` with an access token, log in again.

Send `Authorization: Bearer $ELLA_MOCN_TOKEN` on every request, except `GET /api/v1/status`, `/livez`, `/readyz`, `/metrics` and `/openapi.yaml`.

## Finding the endpoint

The OpenAPI spec is the source of truth. Fetch it once per session when an operation is not covered below; never guess paths or fields:

```bash
curl -s "$ELLA_MOCN_URL/api/v1/openapi.yaml" | grep -E '^  /api'
```

| Question | Endpoint |
|----------|----------|
| Version, health | `GET /api/v1/status`, `GET /api/v1/readyz` |
| Operators, phones per operator, MME and AMF state | `GET /api/v1/operators` |
| One operator, phones on each of its radios | `GET /api/v1/operators/{name}` (`ue_connections_by_radio`) |
| Connected radios and their state | `GET /api/v1/radios` |
| One radio, phones per operator | `GET /api/v1/radios/{ref}` (`ue_connections_by_operator`) |
| Individual phones, cell, tunnels | `GET /api/v1/ue-connections` (`operator`: name, `radio`: ref) |
| Counters and gauges | `GET /api/v1/metrics` (Prometheus text, `ellamocn_*`) |

Responses are `{"result": ...}` or `{"error": "..."}`. Radios, UE connections, users and API tokens are paginated: `page` and `per_page` (default `25`, max `100`), returning `items`, `page`, `per_page`, `total_count`. Operators return `items` only. URL-encode operator names (`Lakeside%20Mobile`). A radio `ref` looks like `eNB:001-01:MacroeNB-00001` or `gNB:001-01:000004@24`. For a count, request `per_page=1` and read `total_count`, or read the `ellamocn_*` gauges from `/api/v1/metrics`.

## Phones and radios

`ue_connections` counts the UE-associated logical S1- and NG-connections Ella MOCN relays. Per operator it is on each operator and on each of its MMEs and AMFs (`status.ue_connections`); per radio it is on each radio.

```bash
curl -s -H "Authorization: Bearer $ELLA_MOCN_TOKEN" "$ELLA_MOCN_URL/api/v1/operators" \
  | jq -r '.result.items[] | [.name, .ue_connections,
    ([.mmes[].status.state] | join(",")), ([.amfs[].status.state] | join(",")),
    (.user_plane.status.state // "-")] | @tsv'
```

Radios are learned at runtime, not configured: a radio that lost its SCTP association is no longer listed, and Ella MOCN has no list of expected radios. To find radios down, page through all of them, report any whose `state` is not `up` (`setup_failed` comes with `setup_failure.cause` and `reason`), and compare `total_count` with what the user expects:

```bash
page=1
while :; do
  resp=$(curl -s -H "Authorization: Bearer $ELLA_MOCN_TOKEN" \
    "$ELLA_MOCN_URL/api/v1/radios?per_page=100&page=$page")
  echo "$resp" | jq -r '.result.items[] | select(.state != "up")
    | [(.ref // "-"), (.name // "-"), .state, (.setup_failure.reason // "-")] | @tsv'
  [ $((page * 100)) -ge "$(echo "$resp" | jq .result.total_count)" ] && break
  page=$((page + 1))
done
```

For totals without paging, `/api/v1/metrics` has `ellamocn_radio_associations` (by `interface` and `state`), `ellamocn_ue_connections` (by `interface` and `operator`), `ellamocn_core_associations` (by `operator` and `state`) and `ellamocn_core_overloaded`.

An MME or AMF `status.state` is `disconnected`, `connected` (SCTP up, no setup yet), `up` or `setup_failed`; `overload` lists ongoing overload actions. With user-plane aggregation, `user_plane.status.state` is `ready`, `unavailable` (with a `reason`: new sessions are refused, UEs still register) or `off`.

## Changing things

Roles: Admin (`1`) does everything; Read Only (`2`) reads operators, radios and UE connections; Network Manager (`3`) also creates, updates and deletes operators. A `403` means the role or the token type is not allowed; do not retry. Only an Admin manages users, and an API token cannot create API tokens, change a password or manage users: those need an access token from login.

Before any `POST`, `PUT` or `DELETE`, say what will change and get the user's approval. `GET` the current state first, make one change at a time, and `GET` again to confirm it. Operators carry live traffic.

- `PUT /api/v1/operators/{name}` replaces the whole operator, and a different `name` renames it. A field left out is removed (e.g. `global_enb_id`, the MMEs) or takes its default (`user_plane.aggregation` becomes `true` when aggregation is enabled): `GET` it, edit the `result` and send it back; read-only fields such as `status` and `ue_connections` are ignored.
- An operator needs `name` and 1 to 12 `plmns`. MMEs need a `global_enb_id`, AMFs a `global_gnb_id`, each with a PLMN of the operator. MMEs are reached on SCTP port `36412`, AMFs on `38412`.
- `409`: another operator already has that name or PLMN, or is already the `pws_operator` (at most one).
- An API token is shown once, in the response to `POST /api/v1/users/me/api-tokens` (`{"name": "..."}`, optional `expires_at`).

Request bodies and constraints are in the spec, or at `https://docs.ellanetworks.com/mocn/reference/api/`.
