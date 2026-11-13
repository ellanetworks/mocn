#!/bin/bash
set -euo pipefail

ip tuntap add name ogstun mode tun 2>/dev/null || true
ip addr replace 10.45.0.1/16 dev ogstun
ip link set ogstun up
sysctl -qw net.ipv4.ip_forward=1

mkdir -p /var/log/open5gs
for nf in nrfd scpd hssd pcrfd upfd sgwud smfd sgwcd mmed; do
	name=${nf%d}
	: >"/var/log/open5gs/$name.log"
	"open5gs-$nf" -c "/etc/open5gs/$name.yaml" >/dev/null 2>&1 &
	sleep 0.3
done

exec tail -n +1 -F /var/log/open5gs/*.log
