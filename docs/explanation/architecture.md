---
description: Where Ella MOCN sits between a neutral host's radios and the operators' cores, and what each side sees.
---

# Architecture

Ella MOCN is a Multi-Operator Core Network (MOCN) gateway. It lets one neutral host radio network serve the subscribers of several mobile operators, with each operator keeping its own core network.

Ella MOCN is a single application, with an embedded database, an embedded web UI, and an HTTP API.

## What each side sees

```
Radios ──S1──► Ella MOCN ──S1──► Operator A's MMEs
                         └─S1──► Operator B's MMEs
Radios ══ user plane (direct) ═══════════════════► each operator's gateways
```

- **Radios** connect to Ella MOCN as if it were their MME. They broadcast the PLMNs of every operator they serve, and need no per-operator configuration.
- **Each operator** sees Ella MOCN as one eNB, with the Global eNB ID that operator assigned, serving the tracking areas of the neutral host's radios that broadcast its PLMNs. Ella MOCN connects to each of the operator's MMEs.

This is the gateway model 3GPP defines for small cells (the HeNB gateway, TS 36.300 clause 4.6).

## How a phone reaches its operator

When a phone connects, it selects one of the PLMNs the radio broadcasts. Ella MOCN sends its signalling to the operator that owns that PLMN, and within that operator:

1. to the MME that already knows the phone, if any;
2. otherwise to one of the operator's MMEs that are up, at random, in proportion to the capacity each MME advertises. An MME that advertises no capacity gets no new phones.

If no operator owns the PLMN, or the operator has no MME up, the phone is released and can try again.

## Reaching an idle phone

When an operator pages an idle phone, the page reaches only the radios that serve the phone's tracking areas and broadcast that operator's PLMN. Radios that serve only other operators never see it.

## Overloaded MMEs

When an MME is overloaded and asks for less traffic, Ella MOCN passes the request on to the radios that broadcast that operator's PLMNs, naming that MME so that the radios reject only the traffic bound for it. Radios that connect later are told too. Other operators, and the operator's other MMEs, are not affected. Ella MOCN does not reject phones itself and keeps choosing MMEs by capacity. When the MME recovers, or its connection drops, the radios resume normal operation.

## What Ella MOCN does not touch

- **Subscriber signalling and keys:** the messages between the phone and its operator's core pass through unchanged. Ella MOCN never reads, logs or stores them, so it never knows who the subscribers are.
- **User traffic:** data goes directly between the radio and the operator's gateways, without crossing Ella MOCN.
- **Transport security:** each operator is reached through an IPsec tunnel set up on the host, as agreed with that operator.

## Connections and failures

- A radio is accepted once at least one of its PLMNs belongs to an operator with an MME that is up.
- Ella MOCN completes S1 Setup with an operator's MMEs once a radio broadcasting that operator's PLMNs is connected, and keeps reconnecting if a connection drops.
- When a radio or an MME disconnects, the phones it carried are released on the other side, so neither side keeps stale state.
