---
description: Connect a radio to Ella MOCN.
---

# Connect a Radio

1. Set the radio's core address:
    - for a 4G radio (eNB), set the MME address to an address of the Ella MOCN host that `s1.address` listens on (all IPv4 addresses (`0.0.0.0`) by default), and the port to `s1.port` (`36412` by default);
    - for a 5G radio (gNB), set the AMF address to an address of the Ella MOCN host that `ng.address` listens on (all IPv4 addresses (`0.0.0.0`) by default), and the port to `ng.port` (`38412` by default).
2. Broadcast at least one PLMN of an operator that has MMEs (4G) or AMFs (5G).
3. In **Radios**, check that the radio's status is **Up**.
