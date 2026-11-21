# Ella MOCN Helm Chart

[Ella MOCN](https://docs.ellanetworks.com/mocn/) connects a neutral-host radio network to multiple mobile operators. This chart deploys one Ella MOCN instance.

## Prerequisites

The cluster must meet the [Kubernetes requirements](https://docs.ellanetworks.com/mocn/reference/system_reqs/#kubernetes).

## Install

```bash
helm install ella-mocn oci://ghcr.io/ellanetworks/charts/ella-mocn --namespace ella --create-namespace -f values.yaml
```

Radios reach S1 and NG through a Service on the pod network:

```yaml
ran:
  service:
    type: LoadBalancer
```

Radios reach S1 and NG on a Multus interface:

```yaml
ran:
  address: 192.168.252.7/24
  networkAttachment:
    master: eth1
```

With user-plane aggregation:

```yaml
ran:
  address: 192.168.252.7/24
  networkAttachment:
    master: eth1
userPlane:
  enabled: true
```

See [Install](https://docs.ellanetworks.com/mocn/how_to/install/).

## Values

| Key | Default | Description |
|-----|---------|-------------|
| `ran.address` | | S1 and NG address of the pod on a Multus network, in CIDR notation. Empty listens on the pod network and exposes S1 and NG through the `<release>-ran` Service. |
| `ran.interface` | `ran` | Name of the Multus interface in the pod. |
| `ran.networkAttachment.create` | `true` | Create a macvlan NetworkAttachmentDefinition, when `ran.address` is set. Set to `false` to use your own, which must set `net.ipv4.ip_forward` and `net.ipv6.conf.all.forwarding` to `1` in the pod when `userPlane.enabled` is `true`. |
| `ran.networkAttachment.name` | `<release>-ran` | Name of the NetworkAttachmentDefinition. |
| `ran.networkAttachment.master` | `eth1` | Host interface that connects to the radios. |
| `ran.networkAttachment.mode` | `bridge` | macvlan mode. |
| `ran.networkAttachment.routes` | `[]` | Routes through the Multus interface, e.g. `{dst: 10.20.0.0/16, gw: 192.168.252.1}` for radios behind a router. |
| `ran.service.type` | `ClusterIP` | Type of the S1 and NG Service, when `ran.address` is empty. |
| `ran.service.s1NodePort` | | Node port of S1, when `ran.service.type` is `NodePort` or `LoadBalancer`. |
| `ran.service.ngNodePort` | | Node port of NG, when `ran.service.type` is `NodePort` or `LoadBalancer`. |
| `ran.service.annotations` | `{}` | Annotations of the S1 and NG Service. |
| `userPlane.enabled` | `false` | Enable [user-plane aggregation](https://docs.ellanetworks.com/mocn/how_to/enable_user_plane_aggregation/) on `ran.address`. Requires `ran.address`. The pod runs as root with the `BPF`, `PERFMON`, `NET_ADMIN` and `NET_RAW` capabilities, which needs the `privileged` Pod Security level. |
| `ipsec.enabled` | `false` | Run a strongSwan sidecar that terminates [route-based IPsec tunnels](https://docs.ellanetworks.com/mocn/how_to/connect_an_operator_over_ipsec/) to the operators in the pod. Needs the `privileged` Pod Security level. |
| `ipsec.secretName` | | Secret with the swanctl configuration (`*.conf` files with connections and secrets), mounted at `/etc/swanctl/conf.d`. Required when IPsec is enabled. |
| `ipsec.interfaces` | `[]` | xfrm interfaces, one per operator, each with `name`, `id` (the connection's `if_id_in` and `if_id_out`), `address` (Ella MOCN's address toward the operator, in CIDR notation), `mtu` and `routes` (the operator's networks). |
| `ipsec.image.repository` | `docker.io/strongx509/strongswan` | strongSwan image. The default image is amd64 only. |
| `ipsec.image.tag` | `6.1.0` | strongSwan image tag. |
| `ipsec.image.digest` | digest of `6.1.0` | strongSwan image digest. Takes precedence over `ipsec.image.tag`. |
| `ipsec.image.pullPolicy` | `IfNotPresent` | strongSwan image pull policy. |
| `config` | `logging.level: info` | [Configuration file](https://docs.ellanetworks.com/mocn/reference/config_file/) content, merged over the database, API and signalling settings that the chart sets. |
| `image.repository` | `ghcr.io/ellanetworks/ella-mocn` | Image repository. |
| `image.tag` | chart `appVersion` | Image tag. |
| `image.digest` | | Image digest. Takes precedence over `image.tag`. |
| `image.pullPolicy` | `IfNotPresent` | Image pull policy. |
| `imagePullSecrets` | `[]` | Image pull secrets. |
| `service.type` | `ClusterIP` | Type of the API service. |
| `service.port` | `5030` | Port of the API service. |
| `service.nodePort` | | Node port, when `service.type` is `NodePort`. |
| `service.annotations` | `{}` | Annotations of the API service. |
| `api.tls.secretName` | | `kubernetes.io/tls` Secret for HTTPS served by the pod itself. The probes and `helm test` then use HTTPS. With an Ingress, set the controller's annotation for HTTPS to the backend. |
| `ingress.enabled` | `false` | Create an Ingress that exposes the API and UI. |
| `ingress.className` | | IngressClass name. Empty uses the cluster default. |
| `ingress.host` | | Host name of the API and UI. Required when the Ingress is enabled. |
| `ingress.annotations` | `{}` | Ingress annotations, e.g. for cert-manager. |
| `ingress.tls` | `[]` | Ingress TLS entries, each with `secretName` and `hosts`. |
| `persistence.size` | `10Gi` | Size of the data volume. |
| `persistence.storageClassName` | default StorageClass | StorageClass of the data volume. |
| `resources` | 500m CPU, 256 MiB requested; 2 CPU, 1 GiB limit | Container resources. |
| `serviceAccount.create` | `true` | Create a service account. |
| `serviceAccount.name` | `<release>` | Service account name. |
| `serviceAccount.annotations` | `{}` | Service account annotations. |
| `openshift.securityContextConstraints.create` | `auto` | Create a SecurityContextConstraints that grants the pod its security context. `auto` creates it on OpenShift with `userPlane.enabled` or `ipsec.enabled`. |
| `seLinuxOptions` | `type: spc_t` on OpenShift with `userPlane.enabled` | SELinux options of the Ella MOCN container. Loading eBPF programs on an SELinux-enforcing node needs a type such as `spc_t`. |
| `serviceMonitor.enabled` | `false` | Create a Prometheus Operator ServiceMonitor for `/api/v1/metrics`. |
| `serviceMonitor.interval` | `30s` | Scrape interval. |
| `serviceMonitor.labels` | `{}` | Extra ServiceMonitor labels. |
| `nodeSelector` | `{}` | Node selector. |
| `tolerations` | `[]` | Tolerations. |
| `affinity` | `{}` | Affinity. |
| `priorityClassName` | | Priority class. |
| `podAnnotations` | `{}` | Extra pod annotations. |
| `podLabels` | `{}` | Extra pod labels. |
| `nameOverride` | | Overrides the chart name. |
| `fullnameOverride` | | Overrides the resource name prefix. |
| `tests.image.repository` | `registry.access.redhat.com/ubi10/ubi-minimal` | Image of the `helm test` pod. |
| `tests.image.tag` | `10.2` | Tag of the `helm test` image. |
