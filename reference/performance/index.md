# Performance

This reference document contains performance test results of Ella MOCN's datapath with [user-plane aggregation](<https://docs.ellanetworks.com/mocn/explanation/user_plane_aggregation_with_ebpf/index.md>), and of its signalling.

On a 16-vCPU virtual machine, Ella MOCN forwards 15 Gbps of subscriber traffic at 0% loss using about 3 vCPUs, adds under 0.1 ms of round-trip latency, and uses about 480 MiB of memory with 100,000 sessions. On a 20-core host, it relays 17,000 4G attaches or 11,000 5G registrations per second with user-plane aggregation.

## Datapath

### Throughput

The following table outlines Ella MOCN's user-plane throughput at 0% loss, in millions of packets per second (Mpps) and gigabits per second (Gbps), for subscriber IP packets of 46, 494 and 1456 bytes:

Table 1. User-plane throughput at 0% loss

<table> <thead>     <tr>       <th rowspan="2">Network</th>       <th rowspan="2">Direction</th>       <th colspan="3">Throughput (Mpps)</th>       <th colspan="3">Throughput (Gbps)</th>     </tr>     <tr>       <th>46 B</th>       <th>494 B</th>       <th>1456 B</th>       <th>46 B</th>       <th>494 B</th>       <th>1456 B</th>     </tr>   </thead> <tbody>     <tr><td rowspan="2">4G</td><td>Uplink</td><td>1.95</td><td>1.87</td><td>1.29</td><td>0.72</td><td>7.41</td><td>15.0</td></tr>     <tr><td>Downlink</td><td>1.64</td><td>1.92</td><td>1.38</td><td>0.60</td><td>7.60</td><td>16.1</td></tr>   </tbody> <tbody>     <tr><td rowspan="2">5G</td><td>Uplink</td><td>1.76</td><td>1.36</td><td>1.20</td><td>0.65</td><td>5.37</td><td>14.0</td></tr>     <tr><td>Downlink</td><td>1.85</td><td>1.38</td><td>1.32</td><td>0.68</td><td>5.46</td><td>15.3</td></tr>   </tbody> </table>

The packet size represents only the IP packet for the subscribers and ignores Ethernet and GTP-U encapsulation.

All packets through Ella MOCN were relayed by the eBPF fast path: `ellamocn_user_plane_slow_path_packets_total` and `ellamocn_user_plane_dropped_packets_total` stayed at 0.

### Latency (Round-trip)

The following table outlines the user-plane round-trip latency:

Table 2. User-plane round-trip latency

| Path | Average (ms) | Best (ms) | Worst (ms) | Mean Deviation (ms) | p99 (ms) |
| --- | --- | --- | --- | --- | --- |
| Through Ella MOCN | 0.264 | 0.204 | 1.135 | 0.028 | 0.432 |
| Direct | 0.179 | 0.114 | 0.637 | 0.022 | 0.276 |

The value represents the round-trip time from a radio to the operator's S-GW and back.

### CPU

The following table outlines the CPU used on the Ella MOCN host at the throughput above, in vCPUs:

Table 3. CPU on the Ella MOCN host (vCPUs)

| Direction | 46 B | 494 B | 1456 B |
| --- | --- | --- | --- |
| Uplink | 2.8 | 2.9 | 2.6 |
| Downlink | 2.3 | 3.0 | 2.7 |

No vCPU exceeded 77%.

### Memory

The following table outlines the memory of the user plane by number of sessions, with one E-RAB and two GTP-U tunnels per session, on a host with 16 vCPUs:

Table 4. User-plane memory

| Sessions | Tunnels | eBPF maps (MiB) | Ella MOCN process RSS (MiB) |
| --- | --- | --- | --- |
| 0 | 0 | 36.3 | 42.8 |
| 10,000 | 20,000 | 40.7 | 101.6 |
| 100,000 | 200,000 | 80.5 | 399.2 |

Table 5. eBPF map memory

| eBPF map | Fixed | Per tunnel |
| --- | --- | --- |
| `tunnels` | 16.0 MiB | 136 B |
| `tunnel_stats` | 16.0 MiB | 96 B |
| `path_stats` | 264 KiB per CPU |  |
| Others | 143 KiB |  |

eBPF map memory is kernel memory and is not part of the process RSS.

### Methodology

We performed performance tests with Ella MOCN on Google Cloud, with three virtual machines in the same zone, of the following specifications:

- **Machine type**: n2-standard-16 (16 vCPUs, 64 GB RAM)
- **OS**: Ubuntu 26.04 LTS, kernel 7.0.0-1017-gcp
- **NICs**: 2 x gVNIC, 4 receive and 4 transmit queues each, MTU 1500
- **Networks**: one for the radios and one for the operator

The Ella MOCN host had `ran.user_plane.enabled` set to `true`, the `forward` chain policy of nftables set to drop, its NIC interrupts spread over all vCPUs, and the `nohz=off` kernel parameter.

The tests used `mocnperf`, in the `perf/` directory of the Ella MOCN repository: fake eNBs and a fake MME (4G), or fake gNBs and a fake AMF (5G), set up the sessions through Ella MOCN, a generator sent GTP-U from `AF_PACKET` sockets, and an XDP program counted the packets received.

Test parameters:

- **Sessions**: 1024, one E-RAB each
- **Radios**: 64, each at its own address
- **S-GWs**: 16 addresses
- **Search**: highest rate at 0% loss (RFC 2544), binary search with 5-second trials and 2% resolution
- **Confirmation**: 10 seconds at 0% loss
- **Runs**: 1

Latency test parameters:

- **Probes**: 1000 per round, one every 10 ms
- **Rounds**: 5 through Ella MOCN and 5 direct, alternating, median
- **Packet size**: 84 B

## Signalling

### Procedure Rate and Latency

The following table outlines the highest rate of procedures Ella MOCN relayed, with 160 procedures in flight, and the time each took:

Table 6. Signalling procedure rate and latency

| Procedure | User-plane aggregation | Rate (/s) | p50 (ms) | p99 (ms) |
| --- | --- | --- | --- | --- |
| 4G attach | Off | 43,981 | 2.27 | 3.74 |
| 4G attach | On | 17,102 | 6.18 | 9.23 |
| 5G registration | Off | 28,214 | 2.20 | 3.64 |
| 5G registration | On | 11,763 | 4.06 | 8.63 |
| 5G PDU session establishment | Off | 28,214 | 2.05 | 3.47 |
| 5G PDU session establishment | On | 11,763 | 6.72 | 10.97 |
| 4G paging |  | 87,961 | 1.76 | 3.77 |
| 5G paging |  | 103,679 | 1.55 | 3.15 |

The time of a 4G attach or 5G registration runs from the radio's Initial UE Message to the core's Initial Context Setup Request. The time of a PDU session establishment runs from the gNB's Uplink NAS Transport to the AMF's PDU Session Resource Setup Request. The time of a paging runs from the core's Paging to its arrival at every radio in the paging area. In 5G, each UE registers, then establishes one PDU session.

### Memory

The following table outlines the memory of 100,000 connected UEs, with user-plane aggregation:

Table 7. Signalling memory with 100,000 connected UEs

| Procedure | Connection rate (/s) | Memory per UE |
| --- | --- | --- |
| 4G attach | 20,783 | 4.7 KB |
| 5G registration and PDU session establishment | 12,463 | 5.2 KB |

Memory per UE is the process RSS growth from 0 to 100,000 connected UEs, divided by 100,000, and includes the simulated radios and cores. eBPF map memory is in the [datapath memory](<#memory>).

### Methodology

We performed signalling tests on one host, with the following specifications:

- **CPU**: Intel Core Ultra 7 265K, 20 cores
- **Memory**: 64 GB
- **OS**: Ubuntu, kernel 7.0.0

The tests used the end-to-end benchmarks in `internal/e2ebench` of the Ella MOCN repository: Ella MOCN, simulated eNBs or gNBs, and simulated MMEs or AMFs of two operators ran in one process on the host, over SCTP. Each UE was released after its procedure.

Test parameters:

- **Procedures in flight**: 160, one radio per procedure
- **Paging area**: 8 radios
- **Duration**: 5 seconds per run
- **Runs**: 3, median
- **Memory test**: 100,000 UEs on 100 radios, 256 procedures in flight
