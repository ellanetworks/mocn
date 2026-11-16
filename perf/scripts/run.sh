#!/usr/bin/env bash
set -euo pipefail

HERE=$(cd "$(dirname "$0")" && pwd)
. "$HERE/common.sh"

usage() {
	cat <<EOF
usage: $0 [-r runs] [-p s1|ng] [-n sessions] [-m radios] [-g gateways] [-o dir] [-- bench flags]

Sets up Ella MOCN with user-plane aggregation, fake core and fake radios on the
perf VMs, runs mocnperf bench the given number of times, and copies the results
and a Markdown summary to the output directory.

  -r runs      bench runs (default 3)
  -p proto     s1 (eNBs, MMEs) or ng (gNBs, AMFs) (default s1)
  -n sessions  UE sessions (default 1024)
  -m radios    radios, from $RADIOS (default 64)
  -g gateways  S-GW or UPF addresses, from $GATEWAYS (default 16)
  -w window    session setups in flight per radio (default 16)
  -i irqs      spread: spread the NIC interrupts of the Ella MOCN host over
               all its vCPUs; default: keep the image's interrupt affinity
               (default spread)
  -o dir       output directory (default perf/results/<timestamp>)
EOF
}

RUNS=3
PROTO=s1
SESSIONS=1024
NRADIOS=64
NGATEWAYS=16
IRQS=spread
WINDOW=16
OUT=""

while getopts "r:p:n:m:g:i:w:o:h" opt; do
	case $opt in
	r) RUNS=$OPTARG ;;
	p) PROTO=$OPTARG ;;
	n) SESSIONS=$OPTARG ;;
	m) NRADIOS=$OPTARG ;;
	g) NGATEWAYS=$OPTARG ;;
	i) IRQS=$OPTARG ;;
	w) WINDOW=$OPTARG ;;
	o) OUT=$OPTARG ;;
	*)
		usage
		exit 2
		;;
	esac
done
shift $((OPTIND - 1))

ROOT=$(git -C "$HERE" rev-parse --show-toplevel)
OUT=${OUT:-$ROOT/perf/results/$(date -u +%Y%m%dT%H%M%SZ)-$PROTO-$IRQS}
mkdir -p "$OUT"

echo "stopping previous runs"
stop_units "$GEN" mocnperf-ran mocnperf-agent
stop_units "$SINK" mocnperf-core mocnperf-agent
stop_units "$MOCN" mocn-perf mocnperf-agent

if [ "$IRQS" = spread ]; then
	echo "spreading NIC interrupts on $MOCN"
	vm_strict "$MOCN" 'cpus=$(nproc); c=0; for nic in /sys/class/net/*/device; do pci=$(basename "$(readlink -f "$nic")"); for irq in $(grep "gve-ntfy-blk.*@pci:$pci" /proc/interrupts | cut -d: -f1); do echo $((c % cpus)) | sudo tee /proc/irq/$irq/smp_affinity_list >/dev/null; c=$((c + 1)); done; done'
fi
vm_strict "$MOCN" 'for irq in $(grep gve-ntfy /proc/interrupts | cut -d: -f1); do echo "$(grep "^ *$irq:" /proc/interrupts | awk "{print \$NF}") cpu $(cat /proc/irq/$irq/effective_affinity_list)"; done' >"$OUT/irqs.txt"

echo "starting agents"
for name in "$GEN" "$MOCN" "$SINK"; do
	unit "$name" mocnperf-agent /usr/local/bin/mocnperf agent -listen :7000
done

echo "starting the fake core on $SINK"
unit "$SINK" mocnperf-core /usr/local/bin/mocnperf core -sctp "$SINK_OP" -gateways "$GATEWAYS" -count "$NGATEWAYS"

echo "starting Ella MOCN on $MOCN"
vm_strict "$MOCN" "sudo rm -f /var/lib/mocn-perf/mocn.db*"
unit "$MOCN" mocn-perf /usr/local/bin/mocn -config /etc/mocn-perf/mocn.yaml

vm_strict "$MOCN" "bash -s" <<EOF
set -euo pipefail
for i in \$(seq 50); do curl -sf localhost:5030/api/v1/livez >/dev/null && break; sleep 0.2; done
PASS=\$(head -c 24 /dev/urandom | base64 | tr -d '/+=')
TOKEN=\$(curl -sf -X POST localhost:5030/api/v1/init -H 'Content-Type: application/json' \
	-d "{\"email\":\"perf@example.com\",\"password\":\"\$PASS\"}" | jq -r .result.token)
PLMN='{"mcc":"001","mnc":"01"}'
curl -sf -X POST localhost:5030/api/v1/operators -H "Authorization: Bearer \$TOKEN" -H 'Content-Type: application/json' -d "{
	\"name\": \"perf\",
	\"plmns\": [\$PLMN],
	\"global_enb_id\": {\"plmn\": \$PLMN, \"enb_id_type\": \"macro\", \"enb_id\": 1},
	\"global_gnb_id\": {\"plmn\": \$PLMN, \"gnb_id\": 1, \"bit_length\": 22},
	\"mmes\": [{\"tnl_addresses\": [\"$SINK_OP\"]}],
	\"amfs\": [{\"tnl_addresses\": [\"$SINK_OP\"]}],
	\"user_plane\": {\"aggregation\": true}
}" >/dev/null
for i in \$(seq 100); do
	STATE=\$(curl -sf localhost:5030/api/v1/operators/perf -H "Authorization: Bearer \$TOKEN" | jq -r .result.user_plane.status.state)
	[ "\$STATE" = ready ] && break
	sleep 0.2
done
echo "operator user plane: \$STATE"
[ "\$STATE" = ready ]
EOF

if [ "$SESSIONS" -gt 0 ]; then
	echo "starting $NRADIOS fake radios with $SESSIONS sessions on $GEN"
	unit "$GEN" mocnperf-ran /usr/local/bin/mocnperf ran -proto "$PROTO" -mocn "$MOCN_RAN" -radios "$RADIOS" \
		-count "$NRADIOS" -sessions "$SESSIONS" -window "$WINDOW"
	vm_strict "$GEN" "for i in \$(seq 300); do n=\$(curl -sf localhost:7001/sessions | jq length 2>/dev/null || echo 0); [ \"\$n\" = $SESSIONS ] && exit 0; sleep 1; done; sudo journalctl -u mocnperf-ran --no-pager | tail -20; exit 1"
	echo "sessions established"
fi

vm_strict "$MOCN" "curl -sf localhost:5030/api/v1/metrics | grep -E '^ellamocn_user_plane_(tunnels|datapath_loaded)'" | tee "$OUT/tunnels.txt"
vm_strict "$MOCN" "cat /etc/mocn-perf/mocn.rev; uname -r" >"$OUT/host.txt"

for i in $(seq "$RUNS"); do
	echo "run $i of $RUNS"
	vm_strict "$GEN" "sudo rm -f /tmp/run-$i.json"
	unit "$GEN" mocnperf-bench /usr/local/bin/mocnperf bench -proto "$PROTO" -direct-sink "$SINK_RAN" -out "/tmp/run-$i.json" "$@"
	while vm "$GEN" "systemctl is-active mocnperf-bench" | grep -qxE 'active|activating'; do
		sleep 30
	done
	vm_strict "$GEN" "sudo journalctl -u mocnperf-bench --no-pager -o cat" >"$OUT/run-$i.log"
	grep -E '"msg":"(result|latency)"|"level":"ERROR"' "$OUT/run-$i.log" || true
	vm_strict "$GEN" "sudo chmod 0644 /tmp/run-$i.json"
	pull "$GEN" "/tmp/run-$i.json" "$OUT/run-$i.json"
done

vm_strict "$MOCN" "curl -sf localhost:5030/api/v1/metrics | grep -E '^ellamocn_user_plane_(dropped|slow_path|tunnel_setup_failures|error_indications)'" >"$OUT/mocn-metrics.txt" || true
vm_strict "$MOCN" "sudo journalctl -u mocn-perf --no-pager -o cat | grep -v '\"level\":\"INFO\"' | tail -50" >"$OUT/mocn-warnings.txt" || true

if [ "$RUNS" -gt 0 ]; then
	(cd "$ROOT" && go run ./perf/cmd/mocnperf report "$OUT"/run-*.json) | tee "$OUT/summary.md"
fi
echo "results in $OUT"
