---
description: RESTful API reference for managing users and API tokens in Ella MOCN.
---

# Users

Paths under `/api/v1/users/me` act on the user of the token. The others require Admin, and an access token: an API token gets `403`.

## Get My User

| Method | Path               |
| ------ | ------------------ |
| GET    | `/api/v1/users/me` |

### Response Fields

| Field          | Type    | Description |
| -------------- | ------- | ----------- |
| `email`        | string  | Email address. |
| `role_id`      | int     | `1` to `3`. See [roles](index.md#roles). |
| `role_pinned`  | boolean | Stored with the user. Ella MOCN has no single sign-on, so it has no effect. |
| `password_set` | boolean | The user has a password. |
| `oidc_linked`  | boolean | Always `false`. Ella MOCN has no single sign-on. |

The other paths that return users return the same fields.

### Sample Response

```json
{
    "result": {
        "email": "admin@example.com",
        "role_id": 1,
        "role_pinned": false,
        "password_set": true,
        "oidc_linked": false
    }
}
```

## Update My Password

This path ends the user's sessions, except the one of the session cookie the request carries. Without that cookie, it ends all of them. It requires an access token.

| Method | Path                        |
| ------ | --------------------------- |
| PUT    | `/api/v1/users/me/password` |

### Request Body

| Field              | Type   | Required | Description |
| ------------------ | ------ | -------- | ----------- |
| `current_password` | string | Yes      | Current password. |
| `password`         | string | Yes      | New password, 8 to 72 bytes. |

### Response Codes

| Code  | Description |
| ----- | ----------- |
| `400` | Wrong current password, or invalid new password. |
| `409` | The user has no password. |
| `429` | 100 wrong passwords in an hour for the account, counting failed logins. |
| `503` | The request was cancelled while waiting to check the password. |

### Sample Request

```json
{
    "current_password": "correct horse",
    "password": "correct horse battery"
}
```

### Sample Response

```json
{
    "result": {
        "message": "Password updated successfully"
    }
}
```

## List Users

This path returns the users, oldest first.

| Method | Path            |
| ------ | --------------- |
| GET    | `/api/v1/users` |

### Query Parameters

| Name       | In    | Type | Default | Allowed | Description |
| ---------- | ----- | ---- | ------- | ------- | ----------- |
| `page`     | query | int  | `1`     | `>= 1`  | 1-based page index. |
| `per_page` | query | int  | `25`    | `1…100` | Number of items per page. |

### Sample Response

```json
{
    "result": {
        "items": [
            {
                "email": "admin@example.com",
                "role_id": 1,
                "role_pinned": false,
                "password_set": true,
                "oidc_linked": false
            },
            {
                "email": "noc@example.com",
                "role_id": 3,
                "role_pinned": false,
                "password_set": true,
                "oidc_linked": false
            },
            {
                "email": "viewer@example.com",
                "role_id": 2,
                "role_pinned": false,
                "password_set": true,
                "oidc_linked": false
            }
        ],
        "page": 1,
        "per_page": 25,
        "total_count": 3
    }
}
```

## Create a User

At most 50 users.

| Method | Path            |
| ------ | --------------- |
| POST   | `/api/v1/users` |

### Request Body

| Field         | Type    | Required | Description |
| ------------- | ------- | -------- | ----------- |
| `email`       | string  | Yes      | Email address. ASCII letters are stored in lower case. |
| `password`    | string  | No       | Empty, or 8 to 72 bytes. Empty or without it, the user cannot log in. |
| `role_id`     | int     | Yes      | `1` to `3`. See [roles](index.md#roles). |
| `role_pinned` | boolean | No       | Default `false`. |

### Response Codes

| Code  | Description |
| ----- | ----------- |
| `201` | Created. |
| `400` | Invalid field, or 50 users exist. |
| `409` | A user has the email, in any case. |

### Sample Request

```json
{
    "email": "noc@example.com",
    "password": "another horse",
    "role_id": 3
}
```

### Sample Response

```json
{
    "result": {
        "message": "User created successfully"
    }
}
```

## Get a User

The email matches in any case.

| Method | Path                    |
| ------ | ----------------------- |
| GET    | `/api/v1/users/{email}` |

### Sample Response

```json
{
    "result": {
        "email": "noc@example.com",
        "role_id": 3,
        "role_pinned": false,
        "password_set": true,
        "oidc_linked": false
    }
}
```

## Update a User

This path sets the role of a user. A role change ends the user's sessions. An Admin cannot change its own role, and the last Admin with a password stays an Admin.

| Method | Path                    |
| ------ | ----------------------- |
| PUT    | `/api/v1/users/{email}` |

### Request Body

| Field         | Type    | Required | Description |
| ------------- | ------- | -------- | ----------- |
| `role_id`     | int     | Yes      | `1` to `3`. |
| `role_pinned` | boolean | No       | Without it, the current value is kept. |

### Response Codes

| Code  | Description |
| ----- | ----------- |
| `403` | The user is the Admin of the token. |
| `409` | The user is the last Admin with a password. |

### Sample Request

```json
{
    "role_id": 2
}
```

### Sample Response

```json
{
    "result": {
        "message": "User updated successfully"
    }
}
```

## Update a User's Password

This path ends the user's sessions. An Admin cannot update its own password here.

| Method | Path                             |
| ------ | -------------------------------- |
| PUT    | `/api/v1/users/{email}/password` |

### Request Body

| Field      | Type   | Required | Description |
| ---------- | ------ | -------- | ----------- |
| `password` | string | Yes      | 8 to 72 bytes. |

### Response Codes

| Code  | Description |
| ----- | ----------- |
| `403` | The user is the Admin of the token. |

### Sample Response

```json
{
    "result": {
        "message": "Password updated successfully"
    }
}
```

## Remove a User's Password

This path removes the password of a user, who then cannot log in. It ends the user's sessions. An Admin cannot remove its own password, and the last Admin with a password keeps it.

| Method | Path                             |
| ------ | -------------------------------- |
| DELETE | `/api/v1/users/{email}/password` |

### Response Codes

| Code  | Description |
| ----- | ----------- |
| `403` | The user is the Admin of the token. |
| `409` | The user has no password, or is the last Admin with a password. |

### Sample Response

```json
{
    "result": {
        "message": "Password removed successfully"
    }
}
```

## Delete a User

This path deletes the user with its sessions and API tokens. An Admin cannot delete itself, and the last Admin with a password cannot be deleted.

| Method | Path                    |
| ------ | ----------------------- |
| DELETE | `/api/v1/users/{email}` |

### Response Codes

| Code  | Description |
| ----- | ----------- |
| `403` | The user is the Admin of the token. |
| `409` | The user is the last Admin with a password. |

### Sample Response

```json
{
    "result": {
        "message": "User deleted successfully"
    }
}
```

## Create an API Token

This path returns the token, the only time it is shown. At most 12 per user. Requires an access token.

| Method | Path                               |
| ------ | ---------------------------------- |
| POST   | `/api/v1/users/me/api-tokens`      |
| POST   | `/api/v1/users/{email}/api-tokens` |

### Request Body

| Field        | Type   | Required | Description |
| ------------ | ------ | -------- | ----------- |
| `name`       | string | Yes      | 3 to 50 characters, unique among the user's tokens. |
| `expires_at` | string | No       | RFC 3339 time in the future. Without it, the token does not expire. |

### Response Fields

| Field   | Type   | Description |
| ------- | ------ | ----------- |
| `token` | string | `ellamocn_`, the token identifier, `_` and the secret. |

### Response Codes

| Code  | Description |
| ----- | ----------- |
| `201` | Created. |
| `400` | Invalid field, or 12 tokens exist. |
| `409` | The user has a token of that name. |

### Sample Request

```json
{
    "name": "Prometheus scraper",
    "expires_at": "2027-11-13T00:00:00Z"
}
```

### Sample Response

```json
{
    "result": {
        "token": "ellamocn_95Q0Y4lBtx2O_FAPT4VAIG3XDCT2V3D2LTEEYCS"
    }
}
```

## List API Tokens

This path returns the user's API tokens, oldest first.

| Method | Path                               |
| ------ | ---------------------------------- |
| GET    | `/api/v1/users/me/api-tokens`      |
| GET    | `/api/v1/users/{email}/api-tokens` |

### Query Parameters

| Name       | In    | Type | Default | Allowed | Description |
| ---------- | ----- | ---- | ------- | ------- | ----------- |
| `page`     | query | int  | `1`     | `>= 1`  | 1-based page index. |
| `per_page` | query | int  | `25`    | `1…100` | Number of items per page. |

### Response Fields

| Field          | Type   | Description |
| -------------- | ------ | ----------- |
| `id`           | string | Token identifier: the part of the token between `ellamocn_` and the next `_`. |
| `name`         | string | Name. |
| `created_at`   | string | Creation time. |
| `expires_at`   | string | Expiry time. Absent for a token that does not expire. |
| `last_used_at` | string | Time of the last use, updated at most once a minute. Absent for a token never used. |

### Sample Response

```json
{
    "result": {
        "items": [
            {
                "id": "95Q0Y4lBtx2O",
                "name": "Prometheus scraper",
                "created_at": "2026-11-13T16:30:39.168Z",
                "expires_at": "2027-11-13T00:00:00.000Z",
                "last_used_at": "2026-11-13T16:30:55.267Z"
            }
        ],
        "page": 1,
        "per_page": 25,
        "total_count": 1
    }
}
```

## Delete an API Token

| Method | Path                                    |
| ------ | --------------------------------------- |
| DELETE | `/api/v1/users/me/api-tokens/{id}`      |
| DELETE | `/api/v1/users/{email}/api-tokens/{id}` |

### Response Codes

| Code  | Description |
| ----- | ----------- |
| `404` | The user has no token with that identifier. |

### Sample Response

```json
{
    "result": {
        "message": "API token deleted successfully"
    }
}
```
