---
description: RESTful API reference for viewing the status and health of Ella MOCN.
---

# Status

## Get the Status

This path returns the version of the MOCN, and whether it is initialized.

| Method | Path             |
| ------ | ---------------- |
| GET    | `/api/v1/status` |

### Response Fields

| Field         | Type    | Description |
| ------------- | ------- | ----------- |
| `version`     | string  | Version. |
| `revision`    | string  | Git commit of the build. Empty in development builds. |
| `initialized` | boolean | Whether the first user exists. See [Initialize](initialize.md). |

### Response Codes

| Code  | Description |
| ----- | ----------- |
| `500` | The users could not be counted. |

### Sample Response

```json
{
    "result": {
        "version": "v0.0.1",
        "revision": "",
        "initialized": true
    }
}
```

## Check Liveness

This path answers while the process serves HTTP.

| Method | Path            |
| ------ | --------------- |
| GET    | `/api/v1/livez` |

It answers `204 No Content`.

## Check Readiness

This path returns whether the MOCN is ready to receive traffic. The response has `Cache-Control: no-store`.

| Method | Path             |
| ------ | ---------------- |
| GET    | `/api/v1/readyz` |

### Checks

| Name       | Passes when |
| ---------- | ----------- |
| `database` | The database answers. |

### Response Fields

| Field             | Type    | Description |
| ----------------- | ------- | ----------- |
| `ready`           | boolean | Whether every check passes. |
| `checks`          | array   | The checks, in order. |
| `checks[].name`   | string  | Name of the check. |
| `checks[].ok`     | boolean | Whether it passes. |
| `checks[].detail` | string  | Why it fails. Empty when it passes. |

### Response Codes

| Code  | Description |
| ----- | ----------- |
| `200` | Every check passes. |
| `503` | A check fails. |

### Sample Response

```json
{
    "result": {
        "ready": true,
        "checks": [
            {"name": "database", "ok": true, "detail": ""}
        ]
    }
}
```
