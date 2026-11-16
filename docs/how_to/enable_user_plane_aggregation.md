---
description: Step-by-step instructions to enable user-plane aggregation, so an operator's subscriber traffic goes through Ella MOCN.
---

# Enable User-Plane Aggregation

This guide carries an operator's subscriber traffic through Ella MOCN, so the operator sees Ella MOCN's addresses instead of the radios'.

## Prerequisites

- **Operator**: connected over IPsec, as in [Connect an Operator over IPsec](connect_an_operator_over_ipsec.md), with its S-GWs and UPFs in the networks routed through the tunnel.
- **User-plane interface**: the interface of the Ella MOCN host that radios' user traffic arrives on.

## Prepare the Host

1. Create `/etc/ella-mocn.nft`, with the interface radios' user traffic arrives on, `eth1` here:

    ```
    table inet ella-mocn
    delete table inet ella-mocn

    table inet ella-mocn {
        chain forward {
            type filter hook forward priority filter; policy accept;
            iifname "eth1" drop
            oifname "eth1" drop
            iifname "xfrm-*" oifname "xfrm-*" drop
        }
    }
    ```

2. Load it at every boot:

    ```shell
    echo 'include "/etc/ella-mocn.nft"' | sudo tee -a /etc/nftables.conf
    sudo systemctl enable nftables
    ```

3. Load it now:

    ```shell
    sudo nft -f /etc/ella-mocn.nft
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
