---
description: System requirements for running Ella MOCN.
---

# System Requirements

Ella MOCN runs on any Linux distribution.

|  | Up to 10,000 subscribers | Up to 100,000 subscribers |
|------------|-------------|-------------|
| **Operating System** | Linux, kernel 6.6 or later with SCTP | Linux, kernel 6.6 or later with SCTP |
| **CPU** | 2 cores, amd64 or arm64 | 4 cores, amd64 or arm64 |
| **Memory** | 1 GB RAM | 2 GB RAM |
| **Disk** | 10 GB | 10 GB |
| **Capabilities** | `CAP_BPF`, `CAP_PERFMON`, `CAP_NET_ADMIN` and `CAP_NET_RAW` (if user-plane aggregation is enabled) | `CAP_BPF`, `CAP_PERFMON`, `CAP_NET_ADMIN` and `CAP_NET_RAW` (if user-plane aggregation is enabled) |
