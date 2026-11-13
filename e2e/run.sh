#!/bin/bash
set -euo pipefail

cd "$(dirname "$0")"

export E2E_API_PORT=${E2E_API_PORT:-5032}
export E2E_API=http://127.0.0.1:$E2E_API_PORT
artifacts=${E2E_ARTIFACTS:-$PWD/.artifacts}

finish() {
	local rc=$?
	if [ "$rc" -ne 0 ]; then
		mkdir -p "$artifacts"
		docker compose ps -a || true
		for svc in mocn open5gs open5gs-b srsenb srsue srsenb-b srsue-b; do
			docker compose logs --no-color "$svc" >"$artifacts/$svc.log" 2>&1 || true
		done
		docker compose cp open5gs:/var/log/open5gs "$artifacts/open5gs" >/dev/null 2>&1 || true
		docker compose cp open5gs-b:/var/log/open5gs "$artifacts/open5gs-b" >/dev/null 2>&1 || true
		docker compose exec -T mocn pkill -INT tcpdump >/dev/null 2>&1 || true
		sleep 1
		docker compose cp mocn:/data/s1.pcap "$artifacts/s1.pcap" >/dev/null 2>&1 || true
		echo "--- mocn"
		tail -n 200 "$artifacts/mocn.log" || true
		echo "--- open5gs mme"
		tail -n 100 "$artifacts/open5gs/mme.log" || true
		echo "--- open5gs-b mme"
		tail -n 100 "$artifacts/open5gs-b/mme.log" || true
		echo "--- srsenb"
		tail -n 100 "$artifacts/srsenb.log" || true
		echo "logs and S1 pcap in $artifacts"
	fi
	if [ -z "${E2E_KEEP:-}" ]; then
		docker compose down -v --remove-orphans >/dev/null 2>&1 || true
	fi
	exit "$rc"
}
trap finish EXIT

docker compose build mocn
docker compose up -d --wait mocn open5gs open5gs-b
docker compose exec -d mocn tcpdump -i any -U -w /data/s1.pcap sctp
docker compose up -d srsenb srsue srsenb-b srsue-b

go test -tags e2e -count=1 -v -timeout 10m . "$@"
