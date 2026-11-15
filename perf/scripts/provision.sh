#!/usr/bin/env bash
set -euo pipefail

HERE=$(cd "$(dirname "$0")" && pwd)
. "$HERE/common.sh"

ROOT=$(git -C "$HERE" rev-parse --show-toplevel)
WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT

REV=${REV:-HEAD}

echo "building Ella MOCN at $REV ($(git -C "$ROOT" rev-parse --short "$REV"))"
mkdir -p "$WORK/src"
git -C "$ROOT" archive "$REV" | tar -x -C "$WORK/src"
(cd "$WORK/src" && CGO_ENABLED=1 go build -trimpath -o "$WORK/mocn" ./cmd/mocn)
git -C "$ROOT" rev-parse "$REV" >"$WORK/mocn.rev"

echo "building mocnperf from the working tree"
(cd "$ROOT" && CGO_ENABLED=1 go build -trimpath -o "$WORK/mocnperf" ./perf/cmd/mocnperf)

cat >"$WORK/mocn.yaml" <<EOF
logging:
  level: info
db:
  path: /var/lib/mocn-perf/mocn.db
api:
  address: 0.0.0.0
  port: 5030
ran:
  signalling:
    address: $MOCN_RAN
  user_plane:
    enabled: true
    name: $MOCN_RAN_IF
EOF

cat >"$WORK/nftables.conf" <<'EOF'
flush ruleset
table inet filter {
	chain input {
		type filter hook input priority filter;
	}
	chain forward {
		type filter hook forward priority filter; policy drop;
	}
	chain output {
		type filter hook output priority filter;
	}
}
EOF

gzip -k "$WORK/mocn" "$WORK/mocnperf"

for name in "$GEN" "$MOCN" "$SINK"; do
	echo "provisioning $name"
	push "$name" /tmp "$WORK/mocnperf.gz"
	vm_strict "$name" "gunzip -f /tmp/mocnperf.gz && sudo install -m 0755 /tmp/mocnperf /usr/local/bin/mocnperf && sudo modprobe sctp && echo sctp | sudo tee /etc/modules-load.d/sctp.conf >/dev/null"
done

push "$MOCN" /tmp "$WORK/mocn.gz" "$WORK/mocn.yaml" "$WORK/nftables.conf" "$WORK/mocn.rev"
vm_strict "$MOCN" "gunzip -f /tmp/mocn.gz && sudo install -m 0755 /tmp/mocn /usr/local/bin/mocn && sudo install -d /etc/mocn-perf /var/lib/mocn-perf && sudo install -m 0644 /tmp/mocn.yaml /tmp/mocn.rev /etc/mocn-perf/ && sudo install -m 0644 /tmp/nftables.conf /etc/nftables.conf && sudo systemctl enable --now nftables >/dev/null 2>&1 && sudo nft -f /etc/nftables.conf"

if ! vm_strict "$MOCN" "grep -qw nohz=off /proc/cmdline"; then
	echo "booting $MOCN with nohz=off, so that /proc/stat accounts softirq time"
	vm_strict "$MOCN" "echo 'GRUB_CMDLINE_LINUX_DEFAULT=\"\$GRUB_CMDLINE_LINUX_DEFAULT nohz=off\"' | sudo tee /etc/default/grub.d/99-mocn-perf.cfg >/dev/null && sudo update-grub >/dev/null 2>&1 && (sudo systemctl reboot >/dev/null 2>&1 &)" || true
	sleep 30
	until vm_strict "$MOCN" "grep -qw nohz=off /proc/cmdline" 2>/dev/null; do
		sleep 10
	done
fi

echo "done"
