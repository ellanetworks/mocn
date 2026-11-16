---
description: Share a real 4G radio between two operators' cores through Ella MOCN, and attach a phone of each operator.
---

# Connect Your First Radio

In this tutorial, we will run two operators' 4G cores and Ella MOCN on one machine, connect a 4G radio to Ella MOCN, and attach a phone of each operator to its own core through that radio. Ella Core is the core of both operators.

You can expect to spend about 30 minutes completing this tutorial.

## Pre-requisites

To complete this tutorial, you will need:

- A Linux machine with [Docker](https://www.docker.com/) installed, and an Ethernet port, `eth1`, connected to the radio.
- A 4G radio (eNB) that can broadcast two PLMNs.
- Two phones, with SIM cards programmed with these values:

| Phone | IMSI | Key | OPc |
|---|---|---|---|
| Operator A's phone | `001010000000001` | `465b5ce8b199b49faa5f0a2ee238a6bc` | `cd63cb71954a9f4e48a5994e37a02baf` |
| Operator B's phone | `001020000000001` | `465b5ce8b199b49faa5f0a2ee238a6bc` | `cd63cb71954a9f4e48a5994e37a02baf` |

## 1. Write the Compose File

Create a new directory for this tutorial and navigate into it:

```shell
mkdir ella-mocn-tutorial
cd ella-mocn-tutorial
```

Copy the following file into this directory:

```yaml title="docker-compose.yaml"
configs:
  ella_core_a_config:
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
          address: "192.168.10.2"
        n3:
          name: "n3"
        n6:
          name: "eth0"
        api:
          address: "0.0.0.0"
          port: 5002
      datapath:
        attach-mode: "tcx"
  ella_core_b_config:
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
          address: "192.168.10.5"
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
      ran:
        signalling:
          address: 0.0.0.0
          s1_port: 36412

services:
  ella-core-a:
    image: ghcr.io/ellanetworks/ella-core:v1.22.0
    configs:
      - source: ella_core_a_config
        target: /core.yaml
    entrypoint: /bin/core --config /core.yaml
    restart: unless-stopped
    privileged: true
    volumes:
      - ella-core-a-data:/data
    ports:
      - "5002:5002"
    networks:
      default:
        driver_opts:
          com.docker.network.endpoint.ifname: eth0
      ran:
        driver_opts:
          com.docker.network.endpoint.ifname: n3
        ipv4_address: 192.168.10.2

  ella-core-b:
    image: ghcr.io/ellanetworks/ella-core:v1.22.0
    configs:
      - source: ella_core_b_config
        target: /core.yaml
    entrypoint: /bin/core --config /core.yaml
    restart: unless-stopped
    privileged: true
    volumes:
      - ella-core-b-data:/data
    ports:
      - "5003:5002"
    networks:
      default:
        driver_opts:
          com.docker.network.endpoint.ifname: eth0
      ran:
        driver_opts:
          com.docker.network.endpoint.ifname: n3
        ipv4_address: 192.168.10.5

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
        ipv4_address: 192.168.10.3

networks:
  ran:
    driver: macvlan
    driver_opts:
      parent: eth1
    ipam:
      config:
        - subnet: 192.168.10.0/24

volumes:
  ella-core-a-data:
  ella-core-b-data:
  ella-mocn-data:
```

The radio reaches Ella MOCN at `192.168.10.3`, Operator A's core at `192.168.10.2` and Operator B's core at `192.168.10.5`, on `eth1`.

## 2. Start Ella Core and Ella MOCN

Start the containers:

```shell
docker compose up -d
```

You should see the following output:

```shell
 ✔ Network ella-mocn-tutorial_default            Created
 ✔ Network ella-mocn-tutorial_ran                Created
 ✔ Volume ella-mocn-tutorial_ella-core-a-data    Created
 ✔ Volume ella-mocn-tutorial_ella-core-b-data    Created
 ✔ Volume ella-mocn-tutorial_ella-mocn-data      Created
 ✔ Container ella-mocn-tutorial-ella-mocn-1      Started
 ✔ Container ella-mocn-tutorial-ella-core-b-1    Started
 ✔ Container ella-mocn-tutorial-ella-core-a-1    Started
```

## 3. Configure Operator A's Core

Open your browser at `http://127.0.0.1:5002/`. The **Initialize Ella Core** page opens.

Create the first user with the following credentials, and click **Create**:

- Email: `admin@ellanetworks.com`
- Password: `admin`

The Ella Core dashboard opens.

Go to **Operator**. **Operator ID (MCC/MNC)** is `001 / 01` and **Supported TACs** is `000001(1)`.

Go to **Subscribers** and click **Create**. Create a subscriber with the following parameters:

- IMSI: `0000000001`, after the `00101` prefix.
- Key: `465b5ce8b199b49faa5f0a2ee238a6bc`
- Sequence Number: keep the default value.
- Profile: keep the default value, `default`.
- Select **Provide custom OPC** and set **OPC (optional)** to `cd63cb71954a9f4e48a5994e37a02baf`.

Click **Create**. The subscriber `001010000000001` appears with the status **Deregistered**.

## 4. Configure Operator B's Core

Open your browser at `http://127.0.0.1:5003/` and create the first user with the same credentials as in step 3.

Go to **Operator**. Next to **Operator ID (MCC/MNC)**, click the edit icon, set **MNC** to `02` and click **Update**. **Operator ID (MCC/MNC)** shows `001 / 02`.

Go to **Subscribers** and create a subscriber with the same parameters as in step 3. The IMSI prefix is now `00102`, and the subscriber `001020000000001` appears with the status **Deregistered**.

## 5. Initialize Ella MOCN

Open your browser at `http://127.0.0.1:5030/`. The **Initialize Ella MOCN** page opens.

Create the first user with the following credentials, and click **Create**:

- Email: `admin@ellanetworks.com`
- Password: `adminadmin`

The Ella MOCN dashboard opens.

## 6. Create the Operators

Go to **Operators** and click **Create**. Set:

- Name: `Operator A`
- MCC: `001`
- MNC: `01`

Click **Create**. The **Operators / Operator A** page opens.

Next to **4G Gateway Identity (Global eNB ID)**, click the edit icon. Turn on **Set a Global eNB ID** and set **eNB ID** to `1`. Keep **PLMN** at `001-01` and **eNB ID type** at **Macro (20 bits)**. Click **Save**.

**4G Gateway Identity (Global eNB ID)** shows `001-01 · Macro (20 bits) · 1`.

In **Core Network Nodes**, click **Add Core Node**. Keep **Type** at **MME (4G)**, set **Address 1** to `192.168.10.2`, Operator A's core, and click **Add**. The MME appears with the status **Connected**. It stays **Connected** until a radio connects.

Go back to **Operators** and create `Operator B` the same way, with:

- MNC: `02`
- eNB ID: `1`, with **PLMN** `001-02`
- MME address: `192.168.10.5`, Operator B's core

## 7. Connect the Radio

In your radio's configuration, set:

- IP address: `192.168.10.10/24`, on the port connected to `eth1`
- MME address: `192.168.10.3`, Ella MOCN
- PLMNs: `001-01` and `001-02`
- TAC: `1`

Restart the radio if its configuration requires it.

In the Ella MOCN UI, go to **Radios**. Your radio appears with the status **Up**.

Go to **Operators**. In **Core Network Nodes**, both operators show `1/1 up`.

## 8. Attach the Phones

Insert the SIM cards and turn on both phones. On each phone, set the APN to `internet`.

In the Ella MOCN UI, go to **Radios** and click your radio. Under **UE Connections by Operator**, `Operator A` and `Operator B` each have **1** UE connection.

Go to **Operators** and click `Operator B`. In **Core Network Nodes**, the MME has the name `ella`, the status **Up** and **1** UE connection.

Both phones are attached to their own operator's core through the same radio.

## Clean Up

Stop and remove the containers, networks and volumes:

```shell
docker compose down -v
```

## Next steps

- [Install Ella MOCN](../how_to/install.md) on your own machine.
- [Connect an Operator](../how_to/connect_an_operator.md).
- [Connect a Radio](../how_to/connect_a_radio.md).
