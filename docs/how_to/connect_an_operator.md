---
description: Connect Ella MOCN to an operator's MMEs and AMFs.
---

# Connect an Operator

1. In **Operators**, click **Create**, then enter the operator's name and PLMNs.
2. Open the operator. Next to **Gateway identity**, click the edit icon. Turn on **4G** and enter the Global eNB ID the operator assigned to you, turn on **5G** and enter the Global gNB ID the operator assigned to you, or both.
3. [Connect the operator's MME](#connect-the-operators-mme), [the operator's AMF](#connect-the-operators-amf), or both.
4. [Connect a radio](connect_a_radio.md) that broadcasts one of the operator's PLMNs.

## Connect the Operator's MME

1. In **Core network nodes**, click **Add**, choose **MME** and enter the MME's addresses.
2. Ask the operator to accept your Global eNB ID and the TAIs of your 4G radios on its MMEs.
3. Once a 4G radio is connected, check that the MME's status is **Up**.

## Connect the Operator's AMF

1. In **Core network nodes**, click **Add**, choose **AMF** and enter the AMF's addresses.
2. Ask the operator to accept your Global gNB ID, the TAIs of your 5G radios and the name `Ella MOCN` on its AMFs.
3. Once a 5G radio is connected, check that the AMF's status is **Up**.
