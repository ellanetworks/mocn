---
description: Enable user-plane aggregation, so operators' subscriber traffic goes through Ella MOCN.
---

# Enable User-Plane Aggregation

## Pre-requisites

To follow this guide, you will need:

- An operator [connected over IPsec](connect_an_operator_over_ipsec.md), with its S-GWs and UPFs in the networks routed through the tunnel
- The interface that radios' user traffic arrives on

## Prepare the Host

1. In `/etc/nftables.conf`, set the policy of the `forward` chain to drop:

    ```
    chain forward {
        type filter hook forward priority filter; policy drop;
    }
    ```

2. Load the firewall now and at every boot:

    ```shell
    sudo systemctl enable --now nftables
    ```

## Turn It On in Ella MOCN

1. In the [configuration file](../reference/config_file.md), set `ran.user_plane.enabled` to `true`, and set `ran.user_plane.name` to the interface radios' user traffic arrives on:

    ```yaml
    ran:
      user_plane:
        enabled: true
        name: eth1
    ```

2. Restart Ella MOCN.

## Turn It On for an Operator

1. In **Operators**, open the operator.
2. Next to **User-Plane Aggregation**, click the edit icon.
3. Turn on **User-plane aggregation** and click **Save**.

## Check the Operator's User Plane

1. Check that **Status** in **User Plane** is **Ready**, and that **Interface** is the operator's xfrm interface.
2. Ask the operator to accept the **Addresses** shown in **User Plane** on its MMEs, AMFs and gateways.
