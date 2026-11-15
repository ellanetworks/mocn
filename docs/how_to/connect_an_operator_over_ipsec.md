---
description: Reach an operator through a route-based IPsec tunnel on the Ella MOCN host.
---

# Connect an Operator over IPsec

This guide connects the Ella MOCN host to an operator's security gateway through a route-based IPsec tunnel, using strongSwan.

## Pre-requisites

To follow this guide, you will need:

- Ubuntu Server 26.04
- The address of the operator's security gateway and its CA certificate
- A certificate and private key for the host, signed by a CA the operator trusts
- The operator's networks that hold its MMEs, AMFs, S-GWs and UPFs

## 1. Install strongSwan

```shell
sudo apt update
sudo apt install -y strongswan-swanctl charon-systemd
```

## 2. Create the xfrm Interface

Create one xfrm interface per operator. Set its MTU to the MTU of the interface that reaches the operator's security gateway, minus 100 bytes for IPsec.

Create `/etc/systemd/network/50-xfrm-opa.netdev`:

```ini
[NetDev]
Name=xfrm-opa
Kind=xfrm
MTUBytes=1400

[Xfrm]
InterfaceId=1
Independent=yes
```

Create `/etc/systemd/network/50-xfrm-opa.network`, with Ella MOCN's address toward the operator and a route to the operator's networks from that address:

```ini
[Match]
Name=xfrm-opa

[Network]
Address=192.0.2.10/32

[Route]
Destination=10.45.0.0/16
PreferredSource=192.0.2.10
```

Apply the configuration:

```shell
sudo networkctl reload
```

Check that the operator's networks are routed through the xfrm interface:

```shell
ip route get 10.45.0.1
```

## 3. Configure the Tunnel

Install the host's certificate and key, and the operator's CA certificate:

```shell
sudo install -m 644 mocn.pem /etc/swanctl/x509/
sudo install -m 600 mocn.key /etc/swanctl/private/
sudo install -m 644 operator-a-ca.pem /etc/swanctl/x509ca/
```

Create `/etc/swanctl/conf.d/operator-a.conf`:

```
connections {
    operator-a {
        version = 2
        local_addrs = 198.51.100.10
        remote_addrs = 203.0.113.1
        local {
            auth = pubkey
            certs = mocn.pem
        }
        remote {
            auth = pubkey
            id = "CN=segw.operator-a.example"
        }
        if_id_in = 1
        if_id_out = 1
        children {
            operator-a {
                local_ts = 192.0.2.10/32
                remote_ts = 10.45.0.0/16
                start_action = start
                dpd_action = restart
            }
        }
    }
}
```

- `local_addrs` and `remote_addrs`: the host's address and the operator's security gateway.
- `remote.id`: the identity in the operator's security gateway certificate.
- `local_ts`: Ella MOCN's address toward the operator.
- `remote_ts`: the operator's networks.
- `if_id_in` and `if_id_out`: the `InterfaceId` of the xfrm interface.

## 4. Start the Tunnel

```shell
sudo swanctl --load-all
```

Check that the tunnel is up:

```shell
sudo swanctl --list-sas
```

Then [connect the operator](connect_an_operator.md). Its MMEs and AMFs are reached through the tunnel.
