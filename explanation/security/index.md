# Security

> [!NOTE]
>
> **Info**
>
> To report a security vulnerability, please file a [Private Security Report](<https://github.com/ellanetworks/mocn/security/advisories/new>).

Security is one of Ella MOCN's core tenets. From the UI to the database, security is built into every layer of the system.

## Authentication & Authorization

Ella MOCN enforces authentication on API requests towards most endpoints. Two authentication methods are supported:

- **Session-based authentication.** Users authenticate with email and password, or through [single sign-on](<https://docs.ellanetworks.com/mocn/explanation/single_sign_on/index.md>). A session cookie and a short-lived access token are issued. The login endpoint limits attempts per client address and per account.
- **API tokens.** Per-user tokens that can expire and be revoked individually. A token has its user's role, but cannot create API tokens, change a password or manage users. Recommended for programmatic access.

### Role-Based Access Control

Every request is authorized against a role-based permission system with three built-in roles:

| Role | Scope |
| --- | --- |
| **Admin** | Full access to all resources and operations. |
| **Network Manager** | Read Only, plus creating, updating and deleting operators. Cannot manage users. |
| **Read Only** | Reads operators, radios and UE connections. |

## Secret Storage

- **User passwords** are stored as one-way hashes. Verification uses constant-time comparison.
- **API token secrets** are stored as one-way hashes. The raw token is returned only once at creation time and is never retrievable afterward.
- **Session tokens** are cryptographically random values. Only a one-way hash is persisted.
- **JWT signing secret** is a cryptographically random value generated once and stored in the database.

## Transport Security

### API and UI

Ella MOCN uses TLS to secure its API and web interface. The TLS configuration is defined in the [configuration file](<https://docs.ellanetworks.com/mocn/reference/config_file/index.md>).

Ella MOCN supports TLS `1.2` and `1.3`.

### Operators

Ella MOCN can reach operator cores with IPsec tunnels. The tunnels are set up on the host, and Ella MOCN uses them to reach the operator's MMEs and AMFs, and its S-GWs and UPFs with [user-plane aggregation](<https://docs.ellanetworks.com/mocn/explanation/user_plane_aggregation_with_ebpf/index.md>). See [Connect an Operator over IPsec](<https://docs.ellanetworks.com/mocn/how_to/connect_an_operator_over_ipsec/index.md>).

### Radios

Ella MOCN does not encrypt S1 and NG toward the radios, and does not check a radio's identity. Protect the link to the radios, for example with IPsec set up on the host or a private network.

## Minimal Attack Surface

Ella MOCN minimizes its attack surface through minimal packaging:

- **Container image.** Built on a distroless base with no operating system layer, shell, or package manager. Only the strictly necessary runtime dependencies are included. Image size: **under 100 MB**.

## Audit Logging

Ella MOCN writes security-relevant events, such as logins and changes to operators and users, to its log as audit events. Each records who did what, and when.
