# Connect an Operator

This guide connects Ella MOCN to an operator's core, through its MMEs for 4G, its AMFs for 5G, or both.

1. In **Operators**, click **Create**, then enter the operator's name and PLMNs.
2. [Connect the operator's MME](<#connect-the-operators-mme>), [the operator's AMF](<#connect-the-operators-amf>), or both.
3. [Connect a radio](<https://docs.ellanetworks.com/mocn/how_to/connect_a_radio/index.md>) that broadcasts one of the operator's PLMNs.

## Connect the Operator's MME

1. Open the operator. Next to **4G Gateway Identity (Global eNB ID)**, click the edit icon, turn on **Set a Global eNB ID**, enter the Global eNB ID the operator assigned to you and click **Save**.
2. In **Core Network Nodes**, click **Add Core Node**, choose **MME (4G)**, enter the MME's addresses and click **Add**.
3. Ask the operator to accept your Global eNB ID, the TAIs of your 4G radios and the name `Ella MOCN` on its MMEs.
4. Once a 4G radio is connected, check that the MME's status is **Up**.

## Connect the Operator's AMF

1. Open the operator. Next to **5G Gateway Identity (Global gNB ID)**, click the edit icon, turn on **Set a Global gNB ID**, enter the Global gNB ID the operator assigned to you and click **Save**.
2. In **Core Network Nodes**, click **Add Core Node**, choose **AMF (5G)**, enter the AMF's addresses and click **Add**.
3. Ask the operator to accept your Global gNB ID, the TAIs of your 5G radios and the name `Ella MOCN` on its AMFs.
4. Once a 5G radio is connected, check that the AMF's status is **Up**.

To carry the operator's subscriber traffic through Ella MOCN, [enable user-plane aggregation](<https://docs.ellanetworks.com/mocn/how_to/enable_user_plane_aggregation/index.md>).
