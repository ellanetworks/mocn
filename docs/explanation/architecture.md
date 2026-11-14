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
- **Each operator** sees Ella MOCN as one eNB and one gNB, with the Global eNB ID and Global gNB ID that operator assigned. The operator provisions one node, whatever the size of the neutral host's network.

Adding an operator does not touch the radios, and adding a radio does not involve the operators. This is the gateway model 3GPP defines for small cells (the HeNB gateway, TS 36.300 clause 4.6, and the NR Femto gateway, TS 38.300 clause 4.10), so the operators' cores need no change.

When a phone connects, it selects one of the PLMNs the radio broadcasts, and Ella MOCN sends its signalling to the operator that owns that PLMN.

## What Ella MOCN does not touch

- **Subscriber signalling:** the messages between a phone and its operator's core are protected with keys only the two of them have. Ella MOCN passes them through unchanged, so the neutral host never learns who the subscribers are.
- **User traffic:** data goes directly between the radio and the operator's gateways. Ella MOCN carries only signalling, and each operator keeps charging and policy in its own core.
- **Transport security:** each operator is reached through an IPsec tunnel set up on the host, as agreed with that operator.

## Mobility

- **S1 and NG handover** go through the operator's core, even between two radios of Ella MOCN. The core routes a handover into Ella MOCN by the target TAI, which is why the neutral host's TACs are not reused elsewhere in the operator's network (TS 36.300 clause 4.6.2).
- **The target radio** of an NG handover is the one named by the Target RAN Node ID that the AMF adds to the HANDOVER REQUEST (TS 38.300 clause 4.10.2.2). Cores without it, and every S1 handover, leave the target cell in the source radio's transparent container: the target radio is then the one whose eNB ID or gNB ID is the leftmost bits of that cell's identity (TS 36.413 clause 9.2.1.37, TS 38.413 clause 9.3.1.6).
- **X2 and Xn handover** into a radio of Ella MOCN ends with a PATH SWITCH REQUEST. Ella MOCN sends it to the MME or AMF named by the Source MME GUMMEI or Source AMF GUAMI that the radio includes. Without a Source AMF GUAMI, it sends it to the AMF set of the operator of the UE's PLMN, when that operator has one AMF set: an AMF UE NGAP ID is unique within an AMF set (TS 23.501 clause 5.9.9). When the source is also a radio of Ella MOCN, Ella MOCN then releases the source's connection with a UE CONTEXT RELEASE COMMAND, as a core does for the old connection of a UE (TS 38.413 clause 8.3.3.1).
