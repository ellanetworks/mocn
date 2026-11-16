---
description: Step-by-step instructions to connect a radio to Ella MOCN.
---

# Connect a Radio

This guide connects a 4G radio (eNB) or a 5G radio (gNB) to Ella MOCN, which connects it to every operator whose PLMN it broadcasts.

1. Set the radio's core address:
    - for a 4G radio (eNB), set the MME address to an address of the Ella MOCN host that `ran.signalling.address` listens on (all IPv4 addresses (`0.0.0.0`) by default), and the port to `ran.signalling.s1_port` (`36412` by default);
    - for a 5G radio (gNB), set the AMF address to an address of the Ella MOCN host that `ran.signalling.address` listens on (all IPv4 addresses (`0.0.0.0`) by default), and the port to `ran.signalling.ng_port` (`38412` by default).
2. Broadcast at least one PLMN of an operator that has MMEs (4G) or AMFs (5G).
3. In **Radios**, check that the radio's status is **Up**.
