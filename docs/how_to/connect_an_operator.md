---
description: Connect Ella MOCN to an operator's MMEs and AMFs.
---

# Connect an Operator

1. In **Operators**, click **Create**, then enter the operator's name and PLMNs.
2. Open the operator and set the gateway identities the operator assigned to you:
    - for 4G, next to **4G Gateway Identity (Global eNB ID)**, click the edit icon, turn on **Set a Global eNB ID**, enter the Global eNB ID and click **Save**;
    - for 5G, next to **5G Gateway Identity (Global gNB ID)**, click the edit icon, turn on **Set a Global gNB ID**, enter the Global gNB ID and click **Save**.
3. [Connect the operator's MME](#connect-the-operators-mme), [the operator's AMF](#connect-the-operators-amf), or both.
4. [Connect a radio](connect_a_radio.md) that broadcasts one of the operator's PLMNs.

## Connect the Operator's MME

1. In **Core Network Nodes**, click **Add Core Node**, choose **MME (4G)**, enter the MME's addresses and click **Add**.
2. Ask the operator to accept your Global eNB ID, the TAIs of your 4G radios and the name `Ella MOCN` on its MMEs.
3. Once a 4G radio is connected, check that the MME's status is **Up**.

## Connect the Operator's AMF

1. In **Core Network Nodes**, click **Add Core Node**, choose **AMF (5G)**, enter the AMF's addresses and click **Add**.
2. Ask the operator to accept your Global gNB ID, the TAIs of your 5G radios and the name `Ella MOCN` on its AMFs.
3. Once a 5G radio is connected, check that the AMF's status is **Up**.

To carry the operator's subscriber traffic through Ella MOCN, [enable user-plane aggregation](enable_user_plane_aggregation.md).
