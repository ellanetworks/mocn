---
description: RESTful API reference for initializing Ella MOCN.
---

# Initialize

## Initialize Ella MOCN

This path creates the first user, an Admin, and logs it in: it returns an access token and sets the session cookie, as [log in](auth.md#log-in) does.

| Method | Path           |
| ------ | -------------- |
| POST   | `/api/v1/init` |

### Request Body

| Field      | Type   | Required | Description |
| ---------- | ------ | -------- | ----------- |
| `email`    | string | Yes      | Email address. ASCII letters are stored in lower case. |
| `password` | string | Yes      | 8 to 72 bytes. |

### Response Fields

| Field     | Type   | Description |
| --------- | ------ | ----------- |
| `message` | string | `System initialized successfully`. |
| `token`   | string | Access token, valid for 15 minutes. |

### Response Codes

| Code  | Description |
| ----- | ----------- |
| `201` | Created. |
| `400` | Invalid email or password. |
| `403` | A user exists. |
| `429` | More than 20 attempts in a minute from the client address, or its IPv6 /64, counting logins. |

### Sample Request

```json
{
    "email": "admin@example.com",
    "password": "correct horse"
}
```

### Sample Response

```json
{
    "result": {
        "message": "System initialized successfully",
        "token": "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9..."
    }
}
```
