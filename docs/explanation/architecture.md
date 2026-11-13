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
Radios ══ user plane (direct) ════► each operator's gateways
```

- **Radios** connect to Ella MOCN as if it were their core: eNBs see one MME, gNBs see one AMF. They broadcast the PLMNs of every operator they serve, and need no per-operator configuration.
- **Each operator** sees Ella MOCN as one eNB and one gNB, with the Global eNB ID and Global gNB ID that operator assigned, serving the tracking areas of the neutral host's radios that broadcast its PLMNs. Ella MOCN connects to each of the operator's MMEs and AMFs.
- **Both sides** see the name "Ella MOCN".

This is the gateway model 3GPP defines for small cells (the HeNB gateway, TS 36.300 clause 4.6, and the NR Femto gateway, TS 38.300 clause 4.10).

## How a phone reaches its operator

When a phone connects, it selects one of the PLMNs the radio broadcasts. Ella MOCN sends its signalling to the operator that owns that PLMN, and within that operator:

1. to the MME or AMF that already knows the phone, if any;
2. on 5G, otherwise to another AMF of the same AMF set, if the phone's AMF is unavailable or unknown;
3. otherwise to one of the operator's MMEs or AMFs that are up, at random, in proportion to the capacity each advertises. A node that advertises no capacity gets no new phones.

If no operator owns the PLMN, or the operator has no MME or AMF up, the phone is released and can try again. When an AMF asks for a phone to be moved to another set of AMFs, Ella MOCN moves it within the same operator.

An AMF that announces it is going out of service gets no new phones, and the radios are told.

## Reaching an idle phone

When an operator pages an idle phone, the page reaches only the radios that serve the phone's tracking areas and broadcast that operator's PLMN. Radios that serve only other operators never see it.

## Overloaded MMEs and AMFs

When an MME is overloaded and asks for less traffic, Ella MOCN passes the request on to the radios that broadcast that operator's PLMNs, naming that MME so that the radios reject only the traffic bound for it. Radios that connect later are told too. Other operators, and the operator's other MMEs, are not affected. Ella MOCN does not reject phones itself and keeps choosing MMEs by capacity. When the MME recovers, or its connection drops, the radios resume normal operation.

When an AMF is overloaded, Ella MOCN shows it on the operator's page. It does not pass the request on to the radios yet, and keeps choosing AMFs by capacity.

## What Ella MOCN does not touch

- **Subscriber signalling and keys:** the messages between the phone and its operator's core pass through unchanged. Ella MOCN never reads, logs or stores them, so it never knows who the subscribers are.
- **User traffic:** data goes directly between the radio and the operator's gateways, without crossing Ella MOCN.
- **Transport security:** each operator is reached through an IPsec tunnel set up on the host, as agreed with that operator.

## Connections and failures

- A radio is accepted once at least one of its PLMNs belongs to an operator with an MME (4G) or an AMF (5G) that is up.
- Ella MOCN completes S1 Setup or NG Setup with an operator's MMEs and AMFs once a radio broadcasting that operator's PLMNs is connected, and keeps reconnecting if a connection drops.
- When a radio, an MME or an AMF disconnects, the phones it carried are released on the other side, so neither side keeps stale state.
