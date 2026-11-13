#!/usr/bin/env bash
set -euo pipefail

MODE="${MODE:-multus}"
IMAGE="${IMAGE:-ella-mocn:latest}"
PREVIOUS_IMAGE="${PREVIOUS_IMAGE:-}"

CLUSTER=mocn-smoke
NODE="${CLUSTER}-control-plane"
NETWORK=mocn-s1
NET="${S1_NET:-192.168.252}"
SUBNET="${NET}.0/24"
IP_RANGE="${NET}.128/25"
GATEWAY="${NET}.1"
SHIPPED_IP=192.168.252.7
MOCN_IP="${NET}.7"
NS=ella

MULTUS_VERSION=v4.3.1
CNI_PLUGINS_VERSION=v1.9.1

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
WORK="$(mktemp -d)"
WATCH_PID=""
S1_PID=""

k() { kubectl -n "${NS}" "$@"; }

step() { echo "::group::$*"; }
endstep() { echo "::endgroup::"; }

fail() {
  echo "::error::$*" >&2
  exit 1
}

cleanup() {
  status=$?
  [ -n "${WATCH_PID}" ] && kill "${WATCH_PID}" 2>/dev/null || true
  [ -n "${S1_PID}" ] && kill "${S1_PID}" 2>/dev/null || true
  if [ "${status}" -ne 0 ]; then
    echo "== Diagnostics"
    k get all,pvc,network-attachment-definitions -o wide || true
    for pod in $(k get pods -o name 2>/dev/null); do
      k describe "${pod}" || true
      k logs "${pod}" --tail=100 || true
      k logs "${pod}" --previous --tail=100 2>/dev/null || true
    done
    kubectl -n kube-system logs -l app=multus --tail=50 || true
  fi
  if [ -z "${KEEP:-}" ]; then
    kind delete cluster --name "${CLUSTER}" >/dev/null 2>&1 || true
    docker network rm "${NETWORK}" >/dev/null 2>&1 || true
  fi
  rm -rf "${WORK}"
  exit "${status}"
}
trap cleanup EXIT

case "${MODE}" in
multus | pod-network) ;;
*) fail "MODE must be multus or pod-network, not ${MODE}" ;;
esac

MOCN_ADDR=""
S1_ADDR=""
S1_PORT=36412
API_PORT=5030

ADMIN_EMAIL=admin@example.com
ADMIN_PASSWORD="smoke test password"
TOKEN=""
LOGINS=0

api() {
  local auth=()
  [ -z "${TOKEN}" ] || auth=(-H "Authorization: Bearer ${TOKEN}")
  curl -fsS --max-time 10 "${auth[@]}" "http://${MOCN_ADDR}:${API_PORT}/api/v1$1" "${@:2}"
}

log_in() {
  local credentials jwt
  credentials="$(jq -n --arg email "${ADMIN_EMAIL}" --arg password "${ADMIN_PASSWORD}" '{email: $email, password: $password}')"
  TOKEN=""
  api /init -X POST -H 'Content-Type: application/json' -d "${credentials}" >/dev/null 2>&1 || true
  jwt="$(api /auth/login -X POST -H 'Content-Type: application/json' -d "${credentials}" | jq -er .result.token)" \
    || fail "cannot log in as ${ADMIN_EMAIL}"
  TOKEN="${jwt}"
  LOGINS=$((LOGINS + 1))
  TOKEN="$(api /users/me/api-tokens -X POST -H 'Content-Type: application/json' -d "{\"name\": \"smoke test ${LOGINS}\"}" \
    | jq -er .result.token)" || fail "cannot create an API token"
}

node_port() { k get service "$1" -o jsonpath="{.spec.ports[?(@.name==\"$2\")].nodePort}"; }

wait_ready() {
  k rollout status statefulset/ella-mocn --timeout=180s
  local deadline=$((SECONDS + 120)) uid deleting ready
  while [ "${SECONDS}" -lt "${deadline}" ]; do
    IFS='|' read -r uid deleting ready < <(k get pod ella-mocn-0 \
      -o jsonpath='{.metadata.uid}|{.metadata.deletionTimestamp}|{.status.conditions[?(@.type=="Ready")].status}{"\n"}' 2>/dev/null || echo '||')
    if [ -n "${uid}" ] && [ "${uid}" != "${1:-}" ] && [ -z "${deleting}" ] && [ "${ready}" = True ]; then
      return 0
    fi
    sleep 1
  done
  fail "ella-mocn-0 did not become ready"
}

wait_api() {
  for _ in $(seq 30); do
    api /status >/dev/null 2>&1 && return 0
    sleep 1
  done
  fail "the API does not answer at ${MOCN_ADDR}:${API_PORT}"
}

deploy() {
  docker tag "$1" ella-mocn:ci
  kind load docker-image ella-mocn:ci --name "${CLUSTER}"
  kubectl kustomize "${ROOT}/ci/k8s/${MODE}" | sed "s/${SHIPPED_IP//./\\.}/${MOCN_IP}/g" | kubectl apply -f -
  local old=""
  if [ "${2:-}" = restart ]; then
    old="$(k get pod ella-mocn-0 -o jsonpath='{.metadata.uid}')"
    k rollout restart statefulset/ella-mocn
  fi
  wait_ready "${old}"

  MOCN_ADDR="$(kubectl get node "${NODE}" -o jsonpath='{.status.addresses[?(@.type=="InternalIP")].address}')"
  API_PORT="$(node_port ella-mocn http)"
  if [ "${MODE}" = multus ]; then
    S1_ADDR="${MOCN_IP}"
  else
    S1_ADDR="${MOCN_ADDR}"
    S1_PORT="$(node_port ella-mocn-s1 s1)"
  fi
  wait_api
}

radios() { api /radios | jq -c '[.result.items[] | {state, tnl_addresses}]'; }

step "Lint the manifests"
version="$(cat "${ROOT}/version/VERSION")"
for dir in k8s k8s/examples/multus ci/k8s/multus ci/k8s/pod-network; do
  kubectl kustomize "${ROOT}/${dir}" | kubeconform -strict -summary -skip NetworkAttachmentDefinition
done
endstep

step "Create the cluster"
if [ "${MODE}" = multus ]; then
  if ip -4 route show to match "${GATEWAY}" | grep -qv '^default'; then
    fail "${SUBNET} is already routed on this host; set S1_NET to a free /24"
  fi
  docker network create "${NETWORK}" --subnet "${SUBNET}" --ip-range "${IP_RANGE}" --gateway "${GATEWAY}" >/dev/null
fi
kind create cluster --name "${CLUSTER}" --wait 120s
if [ "${MODE}" = multus ]; then
  docker network connect "${NETWORK}" "${NODE}"
  docker exec "${NODE}" ip -br addr show eth1
fi
endstep

if [ "${MODE}" = multus ]; then
  step "Install the CNI plugins and Multus"
  curl -fsSL "https://github.com/containernetworking/plugins/releases/download/${CNI_PLUGINS_VERSION}/cni-plugins-linux-amd64-${CNI_PLUGINS_VERSION}.tgz" \
    | tar -xz -C "${WORK}" ./macvlan ./static
  docker cp "${WORK}/macvlan" "${NODE}:/opt/cni/bin/macvlan"
  docker cp "${WORK}/static" "${NODE}:/opt/cni/bin/static"
  curl -fsSL "https://raw.githubusercontent.com/k8snetworkplumbingwg/multus-cni/${MULTUS_VERSION}/deployments/multus-daemonset-thick.yml" \
    | sed "s|multus-cni:snapshot-thick|multus-cni:${MULTUS_VERSION}-thick|" \
    | kubectl apply -f -
  kubectl -n kube-system rollout status daemonset/kube-multus-ds --timeout=180s
  endstep
fi

if [ -n "${PREVIOUS_IMAGE}" ]; then
  step "Deploy the previous release, ${PREVIOUS_IMAGE}"
  docker image inspect "${PREVIOUS_IMAGE}" >/dev/null 2>&1 || docker pull "${PREVIOUS_IMAGE}"
  deploy "${PREVIOUS_IMAGE}"
  log_in
  api /status
  echo
  endstep

  step "Upgrade to ${IMAGE}"
  deploy "${IMAGE}" restart
  api /status | jq -e '.result.initialized' >/dev/null || fail "the first admin is gone after the upgrade"
  log_in
  endstep
else
  step "Deploy ${IMAGE}"
  deploy "${IMAGE}"
  log_in
  endstep
fi

step "Check the pod"
kubectl get namespace "${NS}" -o jsonpath='{.metadata.labels.pod-security\.kubernetes\.io/enforce}' | grep -qx restricted \
  || fail "the namespace does not enforce the restricted Pod Security Standard"
sleep 30
restarts="$(k get pod ella-mocn-0 -o jsonpath='{.status.containerStatuses[0].restartCount}')"
[ "${restarts}" = 0 ] || fail "Ella MOCN restarted ${restarts} times"
k get pod ella-mocn-0 -o wide
endstep

step "Check the API through the Service"
running="$(api /status | jq -r .result.version)"
[ "${running}" = "${version}" ] || fail "Ella MOCN reports version ${running}, want ${version}"
echo "version ${running} at ${MOCN_ADDR}:${API_PORT}"
endstep

step "Check the S1 address"
want="${MOCN_IP}"
if [ "${MODE}" = pod-network ]; then
  want="$(k get pod ella-mocn-0 -o jsonpath='{.status.podIP}')"
fi
s1="$(k logs ella-mocn-0 | grep '"msg":"mocn started"' | jq -r .s1)"
if [ "${MODE}" = multus ]; then
  [ "${s1}" = "${want}:36412" ] || fail "Ella MOCN listens for S1 on ${s1}, want ${want}:36412"
else
  tr '/' '\n' <<<"${s1%:36412}" | grep -qxF "${want}" || fail "Ella MOCN listens for S1 on ${s1}, without ${want}"
fi
echo "S1 on ${s1}"
endstep

step "Connect a radio over SCTP at ${S1_ADDR}:${S1_PORT}"
[ "$(radios)" = "[]" ] || fail "radios are connected before the test"
python3 -I -c '
import socket, sys, time
s = socket.socket(socket.AF_INET, socket.SOCK_STREAM, socket.IPPROTO_SCTP)
s.settimeout(10)
s.connect((sys.argv[1], int(sys.argv[2])))
print("SCTP association up", flush=True)
time.sleep(300)
' "${S1_ADDR}" "${S1_PORT}" >"${WORK}/s1.log" 2>&1 &
S1_PID=$!
connected=""
for _ in $(seq 30); do
  if radios | jq -e 'length == 1 and .[0].state == "connected"' >/dev/null; then
    connected=yes
    break
  fi
  kill -0 "${S1_PID}" 2>/dev/null || { cat "${WORK}/s1.log"; fail "the SCTP association to ${S1_ADDR}:${S1_PORT} failed"; }
  sleep 1
done
[ -n "${connected}" ] || { cat "${WORK}/s1.log"; fail "Ella MOCN does not list the radio"; }
radios
kill "${S1_PID}"
wait "${S1_PID}" 2>/dev/null || true
S1_PID=""
for _ in $(seq 30); do
  [ "$(radios)" = "[]" ] && break
  sleep 1
done
[ "$(radios)" = "[]" ] || fail "Ella MOCN still lists the radio after it disconnected"
echo "the radio connected and disconnected"
endstep

step "Restart the pod"
uid="$(k get pod ella-mocn-0 -o jsonpath='{.metadata.uid}')"
k logs -f ella-mocn-0 >"${WORK}/stopped.log" 2>&1 &
kubectl -n "${NS}" get pod ella-mocn-0 -w -o jsonpath='{.metadata.uid} {.status.containerStatuses[0].state.terminated.exitCode}{"\n"}' \
  >"${WORK}/watch.txt" 2>&1 &
WATCH_PID=$!
k delete pod ella-mocn-0 --wait=false
wait_ready "${uid}"
wait_api
kill "${WATCH_PID}"
WATCH_PID=""

grep -q '"msg":"mocn stopped"' "${WORK}/stopped.log" || fail "Ella MOCN did not log its shutdown"
codes="$(awk -v uid="${uid}" '$1 == uid && NF == 2 { print $2 }' "${WORK}/watch.txt" | sort -u)"
[ "${codes}" = 0 ] || fail "Ella MOCN exited with code '${codes}', want 0"
tail -n 3 "${WORK}/stopped.log"

api /users/me | jq -e --arg email "${ADMIN_EMAIL}" '.result.email == $email' >/dev/null \
  || fail "the API token does not work after the restart"
echo "the database survived the restart"
endstep

echo "Kubernetes smoke test passed"
