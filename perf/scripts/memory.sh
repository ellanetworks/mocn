#!/usr/bin/env bash
set -euo pipefail

HERE=$(cd "$(dirname "$0")" && pwd)
. "$HERE/common.sh"

usage() {
	cat <<EOF2
usage: $0 [-p s1|ng] [-o dir] [sessions ...]

Sets up Ella MOCN with the given numbers of UE sessions, one bearer or PDU
session each, so two tunnels per session, and records the user-plane BPF map
memory, entries and capacity, and the RSS of the Ella MOCN process.

  -p proto  s1 or ng (default s1)
  -o dir    output directory (default perf/results/<timestamp>-memory)
  sessions  default: 0 10000 100000
EOF2
}

PROTO=s1
OUT=""

while getopts "p:o:h" opt; do
	case $opt in
	p) PROTO=$OPTARG ;;
	o) OUT=$OPTARG ;;
	*)
		usage
		exit 2
		;;
	esac
done
shift $((OPTIND - 1))

COUNTS=("$@")
[ ${#COUNTS[@]} -gt 0 ] || COUNTS=(0 10000 100000)

ROOT=$(git -C "$HERE" rev-parse --show-toplevel)
OUT=${OUT:-$ROOT/perf/results/$(date -u +%Y%m%dT%H%M%SZ)-memory}
mkdir -p "$OUT"

for n in "${COUNTS[@]}"; do
	echo "== $n sessions"
	"$HERE/run.sh" -r 0 -p "$PROTO" -n "$n" -w 2 -o "$OUT/setup-$n" >/dev/null
	sleep 10
	vm_strict "$MOCN" "curl -sf localhost:5030/api/v1/metrics | grep -E '^ellamocn_user_plane_(bpf_map_|tunnels)'; echo \"mocn_rss_bytes \$((\$(grep VmRSS /proc/\$(pgrep -x mocn)/status | awk '{print \$2}') * 1024))\"; echo \"cpus \$(nproc)\"" | tee "$OUT/memory-$n.txt"
done
