#!/usr/bin/env bash

ZONE=${ZONE:-us-central1-a}
GEN=${GEN:-mocn-perf-gen}
MOCN=${MOCN:-mocn-perf-mocn}
SINK=${SINK:-mocn-perf-sink}

GEN_RAN=${GEN_RAN:-10.60.0.10}
MOCN_RAN=${MOCN_RAN:-10.60.0.20}
MOCN_RAN_IF=${MOCN_RAN_IF:-ens4}
SINK_OP=${SINK_OP:-10.61.0.30}
SINK_RAN=${SINK_RAN:-10.60.0.30}
RADIOS=${RADIOS:-10.60.0.64}
GATEWAYS=${GATEWAYS:-10.61.0.64}

vm() {
	local name=$1
	shift
	gcloud compute ssh "$name" --zone "$ZONE" --tunnel-through-iap --command "$*" -- -q -o LogLevel=ERROR 2>&1 |
		grep -v -e '^WARNING:' -e 'NumPy' -e 'please see https://cloud.google.com/iap' -e '^$' || true
}

vm_strict() {
	local name=$1
	shift
	gcloud compute ssh "$name" --zone "$ZONE" --tunnel-through-iap --command "$*" -- -q -o LogLevel=ERROR
}

push() {
	local name=$1 dest=$2
	shift 2
	gcloud compute scp --zone "$ZONE" --tunnel-through-iap "$@" "$name:$dest" >/dev/null 2>&1
}

pull() {
	local name=$1 src=$2 dest=$3
	gcloud compute scp --zone "$ZONE" --tunnel-through-iap "$name:$src" "$dest" >/dev/null 2>&1
}

unit() {
	local name=$1 unit=$2
	shift 2
	vm_strict "$name" "sudo systemctl stop $unit 2>/dev/null; sudo systemctl reset-failed $unit 2>/dev/null; sudo systemd-run --quiet --unit $unit --property=LimitMEMLOCK=infinity $*"
}

stop_units() {
	local name=$1
	shift
	vm "$name" "for u in $*; do sudo systemctl stop \$u 2>/dev/null; sudo systemctl reset-failed \$u 2>/dev/null; done; true"
}
