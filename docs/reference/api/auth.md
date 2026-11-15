---
description: RESTful API reference for logging in to Ella MOCN.
---

# Authentication

## Log In

This path returns an access token, and sets the session cookie, `mocn_session`, or `__Host-mocn_session` over TLS. The session lasts 30 days, and a user has at most 10: a new one ends the oldest.

It answers `429 Too Many Requests` after 20 attempts in a minute from one client address (IPv4 address or IPv6 /64), or after 100 wrong passwords in an hour for one account.

| Method | Path                 |
| ------ | -------------------- |
| POST   | `/api/v1/auth/login` |

### Request Body

| Field      | Type   | Required | Description |
| ---------- | ------ | -------- | ----------- |
| `email`    | string | Yes      | Email address, in any case. |
| `password` | string | Yes      | Password. |

### Response Fields

| Field     | Type   | Description |
| --------- | ------ | ----------- |
| `message` | string | `Login successful`. |
| `token`   | string | Access token, valid for 15 minutes. |

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
        "message": "Login successful",
        "token": "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9..."
    }
}
```

## Refresh the Access Token

This path returns a new access token for the session of the cookie.

| Method | Path                   |
| ------ | ---------------------- |
| POST   | `/api/v1/auth/refresh` |

### Response Fields

| Field   | Type   | Description |
| ------- | ------ | ----------- |
| `token` | string | Access token, valid for 15 minutes. |

### Sample Response

```json
{
    "result": {
        "token": "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9..."
    }
}
```

## Log Out

This path ends the session of the cookie, and its access tokens, and clears the cookie.

| Method | Path                  |
| ------ | --------------------- |
| POST   | `/api/v1/auth/logout` |
