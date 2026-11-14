---
description: Why Ella MOCN sits between a neutral host's radios and the operators' cores, and what each side sees.
---

# Architecture

Ella MOCN is a Multi-Operator Core Network (MOCN) gateway. It lets one neutral host radio network serve the subscribers of several mobile operators, with each operator keeping its own core network.

Ella MOCN is a single application, with an embedded database, an embedded web UI, and an HTTP API, so that a neutral host runs one process per site and nothing else.

## Why a gateway

A radio can be connected to several operators' cores directly, but then every radio needs the address and the settings of every operator, and every operator must provision every radio. Adding an operator means touching every radio, and adding a radio means asking every operator.

Ella MOCN stands between them instead:

```
4G radios ──S1──► Ella MOCN ──S1──► Operator A's MMEs, Operator B's MMEs
5G radios ──NG──►           ──NG──► Operator A's AMFs, Operator B's AMFs
Radios ══ user plane (direct) ════► each operator's gateways
```

- **Radios** connect to Ella MOCN as if it were their core: eNBs see one MME, gNBs see one AMF. They broadcast the PLMNs of every operator they serve, and need no per-operator configuration. Adding an operator does not touch them.
- **Each operator** sees Ella MOCN as one eNB and one gNB, with the Global eNB ID and Global gNB ID that operator assigned. The operator provisions one node, whatever the size of the neutral host's network, and the neutral host adds or removes radios without asking the operator. The radios' own IDs never reach the operator, so they cannot clash with the IDs of the operator's own radios.
- **Both sides** see the name "Ella MOCN".

This is the gateway model 3GPP defines for small cells (the HeNB gateway, TS 36.300 clause 4.6, and the NR Femto gateway, TS 38.300 clause 4.10), so the operators' cores need no change.

## Why Ella MOCN does not touch subscriber signalling

The messages between a phone and its operator's core (NAS) are protected with keys that only the phone and the core have. Ella MOCN passes them through unchanged, and never reads, logs or stores them. It chooses the operator from what the radio reports, such as the PLMN the phone selected. The neutral host never knows who the subscribers are, and the operators do not have to trust it with their subscribers' identities or keys.

## Why user traffic bypasses Ella MOCN

Data goes directly between the radio and the operator's gateways. Ella MOCN carries only signalling, so its load does not grow with traffic, it adds no delay to data, and each operator keeps charging and policy for its traffic in its own core.

## Why IPsec is set up on the host

Each operator has its own security gateway and its own requirements for the link to it. The tunnel to each operator is set up on the host with standard IPsec tools, as agreed with that operator, rather than inside Ella MOCN. Each tunnel follows its operator's agreement, and Ella MOCN works the same with or without them.

## How a phone reaches its operator

When a phone connects, it selects one of the PLMNs the radio broadcasts. Ella MOCN sends its signalling to the operator that owns that PLMN, to the MME or AMF that already knows the phone, or else to one of the operator's MMEs or AMFs that are up, in proportion to the capacity each advertises. If no operator can take the phone, it is released and can try again.

When an operator pages an idle phone, the page reaches only the radios that broadcast that operator's PLMN, so radios that serve only other operators never see it.

## Overloaded MMEs and AMFs

When an MME or AMF is overloaded and asks for less traffic, Ella MOCN relays the request to the radios that broadcast that operator's PLMNs, and to radios that connect later. The radios then hold back the traffic bound for that node. While the operator has another MME or AMF up with a non-zero capacity, Ella MOCN also stops choosing the overloaded node for new phones.

Ella MOCN never rejects phones itself. Deciding which phones to turn away is the radio's job in 3GPP, and the radio knows why each phone connects. Other operators are not affected. When the node recovers, its connection drops, or its operator is removed, the radios resume normal operation.

## Connections and failures

Ella MOCN accepts a radio once one of its PLMNs belongs to an operator with an MME (4G) or an AMF (5G) that is up, and keeps reconnecting to the operators' MMEs and AMFs when a connection drops. When one side drops, the phones it carried are released on the other side, so neither side keeps stale state. On 5G, a phone whose AMF goes away moves to another AMF of the same AMF set, which shares its state.
