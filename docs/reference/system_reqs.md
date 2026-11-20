---
description: System requirements for running Ella MOCN.
---

# System Requirements

Ella MOCN runs on any Linux distribution, on amd64 and arm64.

|  | Up to 10,000 subscribers | Up to 100,000 subscribers |
|------------|-------------|-------------|
| **Operating System** | Linux, kernel 6.6 or later with SCTP | Linux, kernel 6.6 or later with SCTP |
| **CPU** | 2 cores, amd64 or arm64 | 4 cores, amd64 or arm64 |
| **Memory** | 1 GB RAM | 2 GB RAM |
| **Disk** | 10 GB | 10 GB |
| **Capabilities** | `CAP_BPF`, `CAP_PERFMON`, `CAP_NET_ADMIN` and `CAP_NET_RAW` (if user-plane aggregation is enabled) | `CAP_BPF`, `CAP_PERFMON`, `CAP_NET_ADMIN` and `CAP_NET_RAW` (if user-plane aggregation is enabled) |

## Kubernetes

| Requirement | Value |
|-------------|-------|
| **Nodes** | Meet the host requirements above |
| **Kubernetes** | 1.30 or later, with SCTP Services |
| **RAN network** | Radios reach Ella MOCN's S1 and NG ports, either through a Service on the pod network, or at the pod's address on a [Multus](https://github.com/k8snetworkplumbingwg/multus-cni) network. On a Multus network, the Helm chart uses the `macvlan` and `static` [CNI plugins](https://github.com/containernetworking/plugins). |
| **Pod Security** | `restricted` |
| **Storage** | A default StorageClass with `ReadWriteOnce` volumes |
| **User-plane aggregation** | Not supported |
