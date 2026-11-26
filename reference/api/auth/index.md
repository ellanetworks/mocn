# Authentication

## Log In

This path returns an access token, and sets the session cookie, `mocn_session`, or `__Host-mocn_session` over TLS. The session lasts 30 days, and a user has at most 10: a new one ends the oldest.

It answers `429 Too Many Requests` after 20 attempts in a minute from one client address (IPv4 address or IPv6 /64), or after 100 wrong passwords in an hour for one account.

| Method | Path |
| --- | --- |
| POST | `/api/v1/auth/login` |

### Request Body

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `email` | string | Yes | Email address, in any case. |
| `password` | string | Yes | Password. |

### Response Fields

| Field | Type | Description |
| --- | --- | --- |
| `message` | string | `Login successful`. |
| `token` | string | Access token, valid for 15 minutes. |

### Sample Request

```
{
    "email": "admin@example.com",
    "password": "correct horse"
}
```

### Sample Response

```
{
    "result": {
        "message": "Login successful",
        "token": "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9..."
    }
}
```

## Refresh the Access Token

This path returns a new access token for the session of the cookie.

| Method | Path |
| --- | --- |
| POST | `/api/v1/auth/refresh` |

### Response Fields

| Field | Type | Description |
| --- | --- | --- |
| `token` | string | Access token, valid for 15 minutes. |

### Sample Response

```
{
    "result": {
        "token": "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9..."
    }
}
```

## Log Out

This path ends the session of the cookie, and its access tokens, and clears the cookie. For a single sign-on session, it answers `200` with the identity provider's logout URL. Otherwise, it answers `204` with no body.

| Method | Path |
| --- | --- |
| POST | `/api/v1/auth/logout` |

### Response Fields

| Field | Type | Description |
| --- | --- | --- |
| `redirect_url` | string | Logout URL of the identity provider. |

### Sample Response

```
{
    "result": {
        "redirect_url": "https://idp.example.com/realms/mocn/protocol/openid-connect/logout?client_id=mocn&post_logout_redirect_uri=https%3A%2F%2Fmocn.example.com%2Flogin"
    }
}
```

## Get the Single Sign-On Option

This path returns whether single sign-on is enabled.

| Method | Path |
| --- | --- |
| GET | `/api/v1/auth/oidc` |

### Response Fields

| Field | Type | Description |
| --- | --- | --- |
| `enabled` | boolean | Single sign-on is enabled. |
| `display_name` | string | Name on the login button. |

### Sample Response

```
{
    "result": {
        "enabled": true,
        "display_name": "Keycloak"
    }
}
```

## Log In Through the Identity Provider

This path redirects the browser to the identity provider.

| Method | Path |
| --- | --- |
| GET | `/api/v1/auth/oidc/login` |

### Query Parameters

| Name | In | Type | Description |
| --- | --- | --- | --- |
| `link` | query | string | `1` links the identity provider account to the user of the session cookie, and redirects to `/profile`, or `/profile?error=<error>`. |

## Return From the Identity Provider

This path is the redirect URL. On success, it sets the session cookie and redirects to `/`. Otherwise, it redirects to `/login?error=<error>`.

| Method | Path |
| --- | --- |
| GET | `/api/v1/auth/oidc/callback` |

### Errors

| Error | Description |
| --- | --- |
| `rate_limited` | Too many logins from the address. |
| `unavailable` | Single sign-on is off, or the identity provider cannot be reached. |
| `failed` | The login expired, was already used or is invalid, the single sign-on settings changed during it, or the identity provider returned an error. |
| `denied` | The user or the identity provider refused access. |
| `not_set_up` | The identity provider gives no email, or no user has it and no group mapping is set. |
| `email_not_verified` | The identity provider marks the email as not verified, or does not say and [`trust_unverified_email`](<https://docs.ellanetworks.com/mocn/reference/api/oidc/index.md#update-the-oidc-settings>) is off. |
| `linked_elsewhere` | The user with the email is linked to another account at the identity provider. |
| `link_required` | The user has a password and must link the account from their profile. |
| `no_group` | None of the user's groups is mapped to a role. |
| `groups_overage` | The user is in too many groups for the identity provider to list them. |
| `too_many_users` | 50 users exist. |
| `error` | Internal error. |
