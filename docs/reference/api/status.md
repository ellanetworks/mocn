---
description: RESTful API reference for viewing the status and health of Ella MOCN.
---

# Status

## Get the Status

This path returns the version of the MOCN, whether it is initialized, and whether user-plane aggregation is enabled.

| Method | Path             |
| ------ | ---------------- |
| GET    | `/api/v1/status` |

### Response Fields

| Field         | Type    | Description |
| ------------- | ------- | ----------- |
| `version`     | string  | Version. |
| `revision`    | string  | Git commit of the build. Empty in development builds. |
| `initialized` | boolean | Whether the first user exists. See [Initialize](initialize.md). |
| `user_plane_aggregation` | boolean | Whether operators can use user-plane aggregation: `ran.user_plane.enabled` in the configuration file. |

### Sample Response

```json
{
    "result": {
        "version": "v0.0.1",
        "revision": "",
        "initialized": true,
        "user_plane_aggregation": false
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
