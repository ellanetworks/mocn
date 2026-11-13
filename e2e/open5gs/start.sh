#!/bin/bash
set -euo pipefail

mkdir -p /etc/open5gs
for f in /config/*.yaml; do
	sed -e "s|\${S1_ADDR}|$S1_ADDR|g" -e "s|\${GTPU_ADDR}|$GTPU_ADDR|g" \
		-e "s|\${MCC}|$MCC|g" -e "s|\${MNC}|$MNC|g" -e "s|\${MME_GID}|$MME_GID|g" -e "s|\${MME_CODE}|$MME_CODE|g" \
		-e "s|\${TAC}|$TAC|g" -e "s|\${MME_NAME}|$MME_NAME|g" -e "s|\${UE_NET}|$UE_NET|g" "$f" >"/etc/open5gs/$(basename "$f")"
done

ip tuntap add name ogstun mode tun 2>/dev/null || true
ip addr replace "$UE_NET.0.1/16" dev ogstun
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
