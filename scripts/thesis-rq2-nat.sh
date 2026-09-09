#!/usr/bin/env bash
# RQ2 measurement: Docker API over a dial-out tunnel while inbound 2375 stays closed,
# and recovery of DockerHost phase after the outbound session is lost.
#
# Run on the CONTROL PLANE. Part 1 collects inbound-closed evidence; part 2 kills the
# edge tunnel client (Docker's restart=always brings it back) and times the phase flip.
#
#   DATA_PLANE_IP=203.0.113.10 TRIALS=3 bash scripts/thesis-rq2-nat.sh
#
# Writes docs/final_thesis/results/rq2_{inbound.log,reconnect.csv,reconnect.tex,macros.tex}.
# Copy that results/ folder next to first.tex; the chapter \inputs it.
set -euo pipefail

NS="${NS:-system}"
HOST_REF="${HOST_REF:-friend-vps}"
CLIENT_NAME="${CLIENT_NAME:-kdop-tunnel-client}"
DATA_PLANE_IP="${DATA_PLANE_IP:-}"
TRIALS="${TRIALS:-3}"
TIMEOUT="${TIMEOUT:-180}"
POLL="${POLL:-1}"
SETTLE="${SETTLE:-30}"
DOCKER_HOST_URL="${DOCKER_HOST_URL:-tcp://remote-docker-tunnel.system.svc:2375}"
HELPER="${HELPER:-kdop-eval}"
HELPER_IMAGE="${HELPER_IMAGE:-docker:cli}"
OUT_DIR="${OUT_DIR:-docs/final_thesis/results}"

mkdir -p "${OUT_DIR}"
INBOUND="${OUT_DIR}/rq2_inbound.log"
CSV="${OUT_DIR}/rq2_reconnect.csv"
TEX="${OUT_DIR}/rq2_reconnect.tex"
MACROS="${OUT_DIR}/rq2_macros.tex"

now() { date +%s.%N; }
delta() { awk -v a="$1" -v b="$2" 'BEGIN{printf "%.1f", b-a}'; }
k() { kubectl -n "${NS}" "$@"; }
d() { k exec "${HELPER}" -- docker "$@"; }
phase() { k get dockerhost "${HOST_REF}" -o jsonpath='{.status.phase}' 2>/dev/null || true; }

ensure_helper() {
  if k get pod "${HELPER}" >/dev/null 2>&1; then return; fi
  k run "${HELPER}" --image="${HELPER_IMAGE}" --restart=Never \
    --env="DOCKER_HOST=${DOCKER_HOST_URL}" --command -- sleep infinity
  k wait --for=condition=Ready "pod/${HELPER}" --timeout=120s
}

# /proc/net/tcp stores the listen address as little-endian hex. Port 2375 = 0x0947.
# 0100007F:0947 is 127.0.0.1:2375 (loopback only); 00000000:0947 is 0.0.0.0:2375 (public).
decode_listeners() {
  awk '$4=="0A" && $2 ~ /:0947$/ {print $2}' | sort -u | while read -r hexaddr; do
    hex="${hexaddr%%:*}"
    a=$((0x${hex:6:2})).$((0x${hex:4:2})).$((0x${hex:2:2})).$((0x${hex:0:2}))
    echo "${a}:2375"
  done
}

ensure_helper
: >"${INBOUND}"
{
  echo "# RQ2 inbound-closed evidence, collected $(date -Is)"
  echo
  echo "## Engine reached through Path A (dial-out tunnel)"
  d version --format 'Server {{.Server.Version}} / API {{.Server.APIVersion}}' 2>&1 | tr -d '\r'
  echo
  echo "## Listen addresses for port 2375 on the data plane"
  echo "# host-network container reading /proc/net/tcp on the edge, via the tunnel"
} >>"${INBOUND}"

raw_tcp="$(d run --rm --network host busybox cat /proc/net/tcp 2>/dev/null | tr -d '\r' || true)"
listeners="$(printf '%s\n' "${raw_tcp}" | decode_listeners || true)"
if [ -z "${listeners}" ]; then
  echo "!! could not read /proc/net/tcp through the tunnel; run 'ss -lntp | grep 2375' on the data plane" >>"${INBOUND}"
  inbound_verdict="unverified"
else
  printf '%s\n' "${listeners}" >>"${INBOUND}"
  if printf '%s\n' "${listeners}" | grep -qv '^127\.0\.0\.1:'; then
    inbound_verdict="VIOLATED (2375 bound beyond loopback)"
  else
    inbound_verdict="loopback-only"
  fi
fi
echo "verdict: ${inbound_verdict}" >>"${INBOUND}"

{
  echo
  echo "## External probe to the data plane's Docker API"
} >>"${INBOUND}"
probe_verdict="skipped (set DATA_PLANE_IP)"
if [ -n "${DATA_PLANE_IP}" ]; then
  if timeout 5 bash -c "exec 3<>/dev/tcp/${DATA_PLANE_IP}/2375" 2>/dev/null; then
    probe_verdict="REACHABLE - RQ2 assumption violated"
  else
    probe_verdict="refused or filtered"
  fi
  echo "tcp connect ${DATA_PLANE_IP}:2375 -> ${probe_verdict}" >>"${INBOUND}"
fi

{
  echo
  echo "## Control-plane view at the same moment"
  k get dockerhost "${HOST_REF}" -o wide 2>&1
  k get dockercontainer 2>&1
} >>"${INBOUND}"

echo "trial,t_error_s,t_connected_s" >"${CSV}"
: >"${TEX}"

for i in $(seq 1 "${TRIALS}"); do
  for _ in $(seq 1 60); do
    if [ "$(phase)" = "Connected" ]; then break; fi
    sleep "${POLL}"
  done
  [ "$(phase)" = "Connected" ] || { echo "trial ${i}: host not Connected before the fault" >&2; exit 1; }

  # Hold the client DOWN long enough for the Host reconciler (30 s ping) to observe
  # Error. A plain "docker kill" + --restart=always reconnects in ~5-15 s, which is
  # faster than the Host requeue, so Error is never seen and the old loop hung until
  # TIMEOUT. Schedule a delayed restart ON THE EDGE (via docker.sock) before we cut
  # the session; after that Path A is down and we can only watch kubectl.
  HOLD_DOWN="${HOLD_DOWN:-45}"
  echo "trial ${i}: scheduling edge restart in ${HOLD_DOWN}s, then stopping ${CLIENT_NAME}"
  d run -d --rm --name "kdop-eval-rearm-${i}" \
    -v /var/run/docker.sock:/var/run/docker.sock \
    "${HELPER_IMAGE}" \
    sh -c "sleep ${HOLD_DOWN}; docker update --restart=always ${CLIENT_NAME}; docker start ${CLIENT_NAME}" \
    >/dev/null
  d update --restart=no "${CLIENT_NAME}" >/dev/null
  # stop drops the tunnel; the API response is usually lost — that is expected
  timeout 20 kubectl -n "${NS}" exec "${HELPER}" -- docker stop "${CLIENT_NAME}" >/dev/null 2>&1 || true
  t0="$(now)"
  echo "trial ${i}: stopped ${CLIENT_NAME} at ${t0} (rearm in ${HOLD_DOWN}s)"

  t_err=""; t_conn=""
  deadline="$(awk -v t="${t0}" -v s="${TIMEOUT}" 'BEGIN{print t+s}')"
  while awk -v n="$(now)" -v d="${deadline}" 'BEGIN{exit !(n<d)}'; do
    p="$(phase)"
    if [ -z "${t_err}" ] && [ "${p}" = "Error" ]; then
      t_err="$(delta "${t0}" "$(now)")"
      echo "trial ${i}: phase=Error after ${t_err}s"
    fi
    if [ -n "${t_err}" ] && [ "${p}" = "Connected" ]; then
      t_conn="$(delta "${t0}" "$(now)")"
      break
    fi
    sleep "${POLL}"
  done

  [ -n "${t_err}" ] || { echo "trial ${i}: FAILED - never saw phase=Error in ${TIMEOUT}s (is Host pinging?)" >&2; exit 1; }
  [ -n "${t_conn}" ] || { echo "trial ${i}: FAILED - phase never returned to Connected in ${TIMEOUT}s" >&2; exit 1; }

  echo "${i},${t_err},${t_conn}" >>"${CSV}"
  printf '%d & %s & %s \\\\\n' "${i}" "${t_err}" "${t_conn}" >>"${TEX}"
  echo "trial ${i}: Error after ${t_err}s, Connected after ${t_conn}s"
  if [ "${i}" -lt "${TRIALS}" ]; then sleep "${SETTLE}"; fi
done

stats="$(awk -F, 'NR>1{s+=$3; v[c++]=$3} END{
  for(i=0;i<c;i++)for(j=i+1;j<c;j++)if(v[j]<v[i]){t=v[i];v[i]=v[j];v[j]=t}
  printf "%d %.1f %.1f %.1f", c, v[0], v[c-1], s/c }' "${CSV}")"
set -- ${stats}
{
  echo "% generated by scripts/thesis-rq2-nat.sh on $(date -Is)"
  echo "\\newcommand{\\rqTwoTrials}{$1}"
  echo "\\newcommand{\\rqTwoMinReconnect}{$2}"
  echo "\\newcommand{\\rqTwoMaxReconnect}{$3}"
  echo "\\newcommand{\\rqTwoMeanReconnect}{$4}"
  echo "\\newcommand{\\rqTwoInboundVerdict}{${inbound_verdict}}"
  echo "\\newcommand{\\rqTwoProbeVerdict}{${probe_verdict}}"
} >"${MACROS}"

echo "inbound: ${inbound_verdict}; probe: ${probe_verdict}"
column -s, -t <"${CSV}" 2>/dev/null || cat "${CSV}"
