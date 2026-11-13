---
description: Connect a simulated 4G radio and phone to an operator's core through Ella MOCN.
---

# Connect Your First Radio

In this tutorial, we will run an operator's 4G core, Ella MOCN, and a simulated radio and phone on one machine, and attach the phone to the core through Ella MOCN. Ella Core is the operator's core, and srsRAN 4G simulates the radio and the phone.

No radio hardware is required.

You can expect to spend about 15 minutes completing this tutorial.

## Pre-requisites

To complete this tutorial, you will need a Linux machine with [Docker](https://www.docker.com/) installed.

## 1. Write the Compose File

Create a new directory for this tutorial and navigate into it:

```shell
mkdir ella-mocn-tutorial
cd ella-mocn-tutorial
```

Copy the following file into this directory:

```yaml title="docker-compose.yaml"
configs:
  ella_core_config:
    content: |
      logging:
        system:
          level: "info"
          output: "stdout"
        audit:
          output: "stdout"
      db:
        path: "/data/core.db"
      interfaces:
        n2:
          address: "10.93.0.2"
        n3:
          name: "n3"
        n6:
          name: "eth0"
        api:
          address: "0.0.0.0"
          port: 5002
      datapath:
        attach-mode: "tcx"
  ella_mocn_config:
    content: |
      logging:
        level: info
      db:
        path: /data/mocn.db
      api:
        address: 0.0.0.0
        port: 5030
      s1:
        address: 0.0.0.0
        port: 36412

services:
  ella-core:
    image: ghcr.io/ellanetworks/ella-core:v1.22.0
    configs:
      - source: ella_core_config
        target: /core.yaml
    entrypoint: /bin/core --config /core.yaml
    restart: unless-stopped
    privileged: true
    volumes:
      - ella-core-data:/data
    ports:
      - "5002:5002"
    networks:
      default:
        driver_opts:
          com.docker.network.endpoint.ifname: eth0
      ran:
        driver_opts:
          com.docker.network.endpoint.ifname: n3
        ipv4_address: 10.93.0.2

  ella-mocn:
    image: ghcr.io/ellanetworks/ella-mocn:latest
    configs:
      - source: ella_mocn_config
        target: /mocn.yaml
    entrypoint: /bin/mocn --config /mocn.yaml
    restart: unless-stopped
    volumes:
      - ella-mocn-data:/data
    ports:
      - "5030:5030"
    networks:
      default: {}
      ran:
        ipv4_address: 10.93.0.3

  srsenb:
    image: gradiant/srsran-4g:23_11
    command: ["enb", "--rf.device_args=tx_port=tcp://*:2000,rx_port=tcp://10.93.1.3:2001,id=enb,base_srate=23.04e6", "--expert.rrc_inactivity_timer=86400000"]
    restart: unless-stopped
    privileged: true
    environment:
      ENB_ID: "0x19B"
      MCC: "001"
      MNC: "01"
      TAC: "1"
      MME_ADDR: 10.93.0.3
      GTP_BIND_ADDR: 10.93.0.4
      S1C_BIND_ADDR: 10.93.0.4
      N_PRB: "50"
      TX_GAIN: "50"
      RX_GAIN: "40"
      ZMQ: "true"
      UE_ADDRESS: 10.93.1.3
    networks:
      ran:
        ipv4_address: 10.93.0.4
      radio:
        ipv4_address: 10.93.1.2

  srsue:
    image: gradiant/srsran-4g:23_11
    command: ["ue", "--rf.device_args=tx_port=tcp://10.93.1.3:2001,rx_port=tcp://10.93.1.2:2000,id=ue,base_srate=23.04e6"]
    restart: on-failure
    privileged: true
    depends_on: [srsenb]
    environment:
      ZMQ: "true"
      ENB_ADDRESS: 10.93.1.2
      UE_ADDRESS: 10.93.1.3
      ALGO: milenage
      KEY: 465b5ce8b199b49faa5f0a2ee238a6bc
      OPC: cd63cb71954a9f4e48a5994e37a02baf
      MCC: "001"
      MNC: "01"
      MSISDN: "0000000001"
      APN: internet
    networks:
      radio:
        ipv4_address: 10.93.1.3

networks:
  default:
    ipam:
      config:
        - subnet: 10.93.2.0/24
  ran:
    internal: true
    ipam:
      config:
        - subnet: 10.93.0.0/24
  radio:
    internal: true
    ipam:
      config:
        - subnet: 10.93.1.0/24

volumes:
  ella-core-data:
  ella-mocn-data:
```

## 2. Start Ella Core and Ella MOCN

Start the Ella Core and Ella MOCN containers:

```shell
docker compose up -d ella-core ella-mocn
```

You should see the following output:

```shell
 ✔ Network ella-mocn-tutorial_ran                  Created
 ✔ Network ella-mocn-tutorial_default              Created
 ✔ Volume ella-mocn-tutorial_ella-mocn-data        Created
 ✔ Volume ella-mocn-tutorial_ella-core-data        Created
 ✔ Container ella-mocn-tutorial-ella-mocn-1        Started
 ✔ Container ella-mocn-tutorial-ella-core-1        Started
```

## 3. Configure Ella Core

Open your browser at `http://127.0.0.1:5002/`. The **Initialize Ella Core** page opens.

Create the first user with the following credentials, and click **Create**:

- Email: `admin@ellanetworks.com`
- Password: `admin`

The Ella Core dashboard opens.

Go to **Operator**. **Operator ID (MCC/MNC)** is `001 / 01` and **Supported TACs** is `000001(1)`.

Go to **Subscribers** and click **Create**. Create a subscriber with the following parameters:

- IMSI: `001010000000001`
- Key: `465b5ce8b199b49faa5f0a2ee238a6bc`
- Sequence Number: keep the default value.
- Profile: keep the default value, `default`.
- Select **Provide custom OPC** and set **OPC** to `cd63cb71954a9f4e48a5994e37a02baf`.

Click **Create**. The subscriber `001010000000001` appears with the status **Deregistered**.

## 4. Initialize Ella MOCN

Open your browser at `http://127.0.0.1:5030/`. The **Initialize Ella MOCN** page opens.

Create the first user with the following credentials, and click **Create**:

- Email: `admin@ellanetworks.com`
- Password: `adminadmin`

The Ella MOCN dashboard opens.

## 5. Create the Operator

Go to **Operators** and click **Create**. Set:

- Name: `Operator A`
- MCC: `001`
- MNC: `01`

Click **Create**. The **Operators / Operator A** page opens.

Next to **Gateway identity**, click the edit icon. Under **4G (Global eNB ID)**, turn on **Set** and set **eNB ID** to `1`. Keep **PLMN** at `001/01` and **eNB ID type** at **Macro (20 bits)**. Click **Save**.

For 4G, **Gateway identity** shows `001/01 · Macro (20 bits) · 1`.

In **Core network nodes**, click **Add**. Keep **Type** at **MME**, set **Address 1** to `10.93.0.2`, Ella Core's address, and click **Add**.

The MME appears with the status **Connected**. It stays **Connected** until a radio connects.

## 6. Start the Radio and the Phone

Go back to your terminal, in the directory with the `docker-compose.yaml` file.

Start the simulated radio and phone:

```shell
docker compose up -d srsenb srsue
```

Wait about 20 seconds, then read the phone's logs:

```shell
docker compose logs srsue
```

The last lines show that the phone attached to Ella Core:

```shell
srsue-1  | Found PLMN:  Id=00101, TAC=1
srsue-1  | RRC Connected
srsue-1  | Random Access Complete.     c-rnti=0x46, ta=0
srsue-1  | Network attach successful. IP: 10.45.0.1
```

## 7. Check the Results

In the Ella MOCN UI, go to **Radios**. The radio `srsenb01` has the status **Up** and **1** UE connection.

Click `srsenb01`. Under **UE connections by operator**, `Operator A` has **1** UE connection.

Go to **Operators** and click `Operator A`. In **Core network nodes**, the MME has the name `ella`, the status **Up** and **1** UE connection.

You have connected your first radio to an operator's core through Ella MOCN.

## Clean Up

Stop and remove the containers, networks and volumes:

```shell
docker compose down -v
```

## Next steps

- [Install Ella MOCN](../how_to/install.md) on your own machine.
- [Connect an Operator](../how_to/connect_an_operator.md).
- [Connect a Radio](../how_to/connect_a_radio.md).
