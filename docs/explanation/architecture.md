---
description: Where Ella MOCN sits between a neutral host's radios and the operators' cores, and what each side sees.
---

# Architecture

Ella MOCN is a Multi-Operator Core Network (MOCN) gateway. It lets one neutral host radio network serve the subscribers of several mobile operators, with each operator keeping its own core network.

Ella MOCN is a single application, with an embedded database, an embedded web UI, and an HTTP API.

## What each side sees

```
4G radios ──S1──► Ella MOCN ──S1──► Operator A's MMEs, Operator B's MMEs
5G radios ──NG──►           ──NG──► Operator A's AMFs, Operator B's AMFs
Radios ══ user plane ══► Ella MOCN or directly ══► each operator's gateways
```

- **Radios** connect to Ella MOCN as if it were their core. They broadcast the networks of every operator they serve, and need no per-operator configuration.
- **Each operator** sees Ella MOCN as a single base station. The operator provisions one node, however large the neutral host's network grows.

Adding an operator does not touch the radios, and adding a radio does not involve the operators. This follows the gateway model 3GPP defines for small cells, so the operators' cores need no change.

When a phone connects, it picks one of the networks the radio broadcasts, and Ella MOCN sends its signalling to the operator that owns that network.

## What Ella MOCN does not touch

- **Subscriber signalling:** the messages between a phone and its operator's core are protected with keys only the two of them have. Ella MOCN passes them through unchanged, so the neutral host never learns who the subscribers are.
- **User traffic:** per operator, data goes either directly between the radio and the operator's gateways, or through Ella MOCN so the operator sees only Ella MOCN's addresses (see [User-Plane Aggregation](user_plane_aggregation_with_ebpf.md)). Ella MOCN never reads or changes what the traffic carries.
- **Transport security:** each operator is reached through an IPsec tunnel set up on the host, as agreed with that operator (see [Connect an Operator over IPsec](../how_to/connect_an_operator_over_ipsec.md)).

## Mobility

When a phone moves between radios, the operator's core stays in charge of the handover, as it would with its own radios. Ella MOCN delivers the handover to the right radio, so phones keep their connection as they move through the neutral host's network. The core finds Ella MOCN by tracking area, so the neutral host's tracking areas must not be reused elsewhere in the operator's network.

## Public warnings

The sharing operators agree on one of them to send public warnings, such as emergency alerts, for the whole shared network. Ella MOCN broadcasts that operator's warnings on every radio in the warning area, and does not broadcast warnings from the others. When a radio restarts, Ella MOCN tells that operator, so its warnings are sent to the radio again.
