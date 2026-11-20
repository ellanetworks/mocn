---
description: Step-by-step instructions to enable user-plane aggregation, so an operator's subscriber traffic goes through Ella MOCN.
---

# Enable User-Plane Aggregation

This guide carries an operator's subscriber traffic through Ella MOCN, so the operator sees Ella MOCN's addresses instead of the radios'.

## Prerequisites

- **Operator**: connected over IPsec, as in [Connect an Operator over IPsec](connect_an_operator_over_ipsec.md), with its S-GWs and UPFs in the networks routed through the tunnel.
- **User-plane interface**: the interface of the Ella MOCN host that radios' user traffic arrives on.

## Prepare the Host

=== "Host"

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

=== "Kubernetes"

    Allow privileged pods in Ella MOCN's namespace:

    ```shell
    kubectl label namespace ella pod-security.kubernetes.io/enforce=privileged pod-security.kubernetes.io/warn=privileged --overwrite
    ```

## Turn It On in Ella MOCN

=== "Host"

    1. With the snap, connect the `network-control` and `mount-observe` interfaces:

        ```shell
        sudo snap connect ella-mocn:network-control
        sudo snap connect ella-mocn:mount-observe
        ```

    2. In the [configuration file](../reference/config_file.md), set `ran.user_plane.enabled` to `true`, and set `ran.user_plane.name` to the interface radios' user traffic arrives on:

        ```yaml
        ran:
          user_plane:
            enabled: true
            name: eth1
        ```

    3. Restart Ella MOCN.

=== "Kubernetes"

    1. In `values.yaml`, set `ran.address` to Ella MOCN's address on the radios' network, and turn on `userPlane.enabled`:

        ```yaml
        ran:
          address: 192.168.252.7/24
          networkAttachment:
            master: eth1
        userPlane:
          enabled: true
        ```

    2. Upgrade the chart:

        ```shell
        helm upgrade ella-mocn oci://ghcr.io/ellanetworks/charts/ella-mocn --version <version> --namespace ella -f values.yaml
        ```

    3. Wait for the rollout:

        ```shell
        kubectl -n ella rollout status statefulset/ella-mocn
        ```

## Turn It On for an Operator

1. In **Operators**, open the operator.
2. Next to **User-Plane Aggregation**, click the edit icon.
3. Turn on **User-plane aggregation** and click **Save**.

## Check the Operator's User Plane

1. Check that **Status** in **User Plane** is **Ready**, and that **Interface** is the operator's xfrm interface.
2. Ask the operator to accept the **Addresses** shown in **User Plane** on its MMEs, AMFs and gateways.
