# Connect an Operator over IPsec

This guide connects Ella MOCN to an operator's security gateway through a route-based IPsec tunnel, using strongSwan.

### Prerequisites

- **Host**: Ubuntu Server 26.04, with a certificate and private key signed by a CA the operator trusts.
- **Operator's security gateway**: its address and its CA certificate.
- **Operator's networks**: the networks that hold the operator's MMEs, AMFs, S-GWs and UPFs.

### 1\. Install strongSwan

```
sudo apt update
sudo apt install -y strongswan-swanctl charon-systemd
```

### 2\. Create the xfrm Interface

Create one xfrm interface per operator. Set its MTU to the MTU of the interface that reaches the operator's security gateway, minus 100 bytes for IPsec.

Create `/etc/systemd/network/50-xfrm-opa.netdev`:

```
[NetDev]
Name=xfrm-opa
Kind=xfrm
MTUBytes=1400

[Xfrm]
InterfaceId=1
Independent=yes
```

Create `/etc/systemd/network/50-xfrm-opa.network`, with Ella MOCN's address toward the operator and a route to the operator's networks from that address:

```
[Match]
Name=xfrm-opa

[Network]
Address=192.0.2.10/32

[Route]
Destination=10.45.0.0/16
PreferredSource=192.0.2.10
```

Apply the configuration:

```
sudo networkctl reload
```

Check that the operator's networks are routed through the xfrm interface:

```
ip route get 10.45.0.1
```

### 3\. Configure the Tunnel

Install the host's certificate and key, and the operator's CA certificate:

```
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

### 4\. Start the Tunnel

```
sudo swanctl --load-all
```

Check that the tunnel is up:

```
sudo swanctl --list-sas
```

Then [connect the operator](<https://docs.ellanetworks.com/mocn/how_to/connect_an_operator/index.md>). Its MMEs and AMFs are reached through the tunnel.

### Prerequisites

- **Ella MOCN**: installed with the Helm chart, in a namespace that allows privileged pods.
- **Operator's security gateway**: its address, its identity and the credentials it accepts.
- **Operator's networks**: the networks that hold the operator's MMEs, AMFs, S-GWs and UPFs.

### 1\. Create the swanctl Configuration

Create `operator-a.conf`, with an `if_id_in` and `if_id_out` per operator:

```
connections {
    operator-a {
        version = 2
        remote_addrs = 203.0.113.1
        local {
            auth = psk
            id = mocn.example
        }
        remote {
            auth = psk
            id = segw.operator-a.example
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
secrets {
    ike-operator-a {
        id-1 = mocn.example
        id-2 = segw.operator-a.example
        secret = "<pre-shared key>"
    }
}
```

To authenticate with certificates instead, add them to the Secret below and reference them by their path in `/etc/swanctl/conf.d`.

### 2\. Create the Secret

```
kubectl -n ella create secret generic ella-mocn-ipsec --from-file=operator-a.conf
```

### 3\. Turn On IPsec

1. In `values.yaml`, add one xfrm interface per operator. Set `id` to the connection's `if_id_in` and `if_id_out`, `address` to Ella MOCN's address toward the operator, `mtu` to the MTU toward the operator's security gateway minus 100, and `routes` to the operator's networks:

   ```
   ipsec:
     enabled: true
     secretName: ella-mocn-ipsec
     interfaces:
       - name: xfrm-opa
         id: 1
         address: 192.0.2.10/32
         mtu: 1400
         routes:
           - 10.45.0.0/16
   ```
2. Upgrade the chart:

   ```
   helm upgrade ella-mocn oci://ghcr.io/ellanetworks/charts/ella-mocn --version <version> --namespace ella -f values.yaml
   ```
3. Check that the tunnel is up:

   ```
   kubectl -n ella exec ella-mocn-0 -c ipsec -- swanctl --list-sas
   ```

Then [connect the operator](<https://docs.ellanetworks.com/mocn/how_to/connect_an_operator/index.md>). Its MMEs and AMFs are reached through the tunnel.
