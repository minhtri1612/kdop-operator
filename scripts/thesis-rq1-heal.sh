#!/usr/bin/env bash
# RQ1 measurement: self-healing after an out-of-band container delete.
#
# Run on the CONTROL PLANE (the machine whose kubectl points at the kdop cluster).
# Fault injection and observation both travel Path A (the reverse tunnel), so every
# timestamp comes from one clock and no SSH to the data plane is needed.
#
#   CR_NAME=nginx-demo TRIALS=3 bash scripts/thesis-rq1-heal.sh
#
# Writes docs/final_thesis/results/rq1_heal.{csv,tex}, rq1_macros.tex and rq1_raw.log.
# Copy that results/ folder next to first.tex; the chapter \inputs it.
set -euo pipefail

NS="${NS:-system}"
CR_NAME="${CR_NAME:-nginx-demo}"
TRIALS="${TRIALS:-3}"
TIMEOUT="${TIMEOUT:-180}"
POLL="${POLL:-1}"
SETTLE="${SETTLE:-20}"
DOCKER_HOST_URL="${DOCKER_HOST_URL:-tcp://remote-docker-tunnel.system.svc:2375}"
HELPER="${HELPER:-kdop-eval}"
HELPER_IMAGE="${HELPER_IMAGE:-docker:cli}"
OUT_DIR="${OUT_DIR:-docs/final_thesis/results}"

mkdir -p "${OUT_DIR}"
CSV="${OUT_DIR}/rq1_heal.csv"
TEX="${OUT_DIR}/rq1_heal.tex"
MACROS="${OUT_DIR}/rq1_macros.tex"
LOG="${OUT_DIR}/rq1_raw.log"

now() { date +%s.%N; }
delta() { awk -v a="$1" -v b="$2" 'BEGIN{printf "%.1f", b-a}'; }
log() { echo "[$(date -Is)] $*" | tee -a "${LOG}"; }

k() { kubectl -n "${NS}" "$@"; }
cr() { k get dockercontainer "${CR_NAME}" -o jsonpath="{$1}" 2>/dev/null || true; }
# docker CLI on the data plane's Engine, reached through the tunnel
d() { k exec "${HELPER}" -- docker "$@"; }

ensure_helper() {
  if k get pod "${HELPER}" >/dev/null 2>&1; then return; fi
  log "Creating helper pod ${HELPER} (${HELPER_IMAGE}, DOCKER_HOST=${DOCKER_HOST_URL})"
  k run "${HELPER}" --image="${HELPER_IMAGE}" --restart=Never \
    --env="DOCKER_HOST=${DOCKER_HOST_URL}" --command -- sleep infinity
  k wait --for=condition=Ready "pod/${HELPER}" --timeout=120s
}

: >"${LOG}"
ensure_helper

CONTAINER_NAME="$(cr .spec.containerName)"
[ -n "${CONTAINER_NAME}" ] || CONTAINER_NAME="${CR_NAME}"
MODE="$(cr .spec.managementMode)"
[ -n "${MODE}" ] || MODE="Enforce (default: field unset)"
HOST_REF="$(cr .spec.dockerHostRef)"
HOST_PHASE="$(k get dockerhost "${HOST_REF}" -o jsonpath='{.status.phase}' 2>/dev/null || true)"

log "CR=${CR_NAME} container=${CONTAINER_NAME} mode=${MODE} host=${HOST_REF} phase=${HOST_PHASE}"
case "${MODE}" in
  Observe) echo "REFUSING: managementMode is Observe. RQ1 trials must use Enforce." >&2; exit 1 ;;
esac
[ "${HOST_PHASE}" = "Connected" ] || { echo "REFUSING: DockerHost ${HOST_REF} is '${HOST_PHASE}', not Connected." >&2; exit 1; }

# Path A sanity check: the Engine is reachable and the baseline container is up.
log "Engine version through the tunnel: $(d version --format '{{.Server.Version}}' 2>&1 | tr -d '\r')"

echo "trial,old_id,new_id,t_engine_s,t_status_s" >"${CSV}"
: >"${TEX}"

for i in $(seq 1 "${TRIALS}"); do
  log "--- trial ${i}: waiting for a steady running baseline"
  for _ in $(seq 1 60); do
    if [ "$(cr .status.state)" = "running" ] && [ -n "$(cr .status.id)" ]; then break; fi
    sleep "${POLL}"
  done
  old_id="$(cr .status.id)"
  [ -n "${old_id}" ] || { echo "trial ${i}: no status.id, baseline never came up" >&2; exit 1; }
  log "trial ${i}: baseline id=${old_id:0:12}"

  d rm -f "${CONTAINER_NAME}" >>"${LOG}" 2>&1
  t0="$(now)"
  log "trial ${i}: docker rm -f ${CONTAINER_NAME} returned at ${t0}"

  t_engine=""; t_status=""; new_id=""
  deadline="$(awk -v t="${t0}" -v s="${TIMEOUT}" 'BEGIN{print t+s}')"
  while awk -v n="$(now)" -v d="${deadline}" 'BEGIN{exit !(n<d)}'; do
    if [ -z "${t_engine}" ] && out="$(d inspect -f '{{.Id}} {{.State.Running}}' "${CONTAINER_NAME}" 2>/dev/null | tr -d '\r')"; then
      cand="${out%% *}"; running="${out##* }"
      if [ -n "${cand}" ] && [ "${cand}" != "${old_id}" ] && [ "${running}" = "true" ]; then
        t_engine="$(delta "${t0}" "$(now)")"; new_id="${cand}"
        log "trial ${i}: Engine has a new running id=${new_id:0:12} after ${t_engine}s"
      fi
    fi
    if [ -z "${t_status}" ]; then
      sid="$(cr .status.id)"
      if [ -n "${sid}" ] && [ "${sid}" != "${old_id}" ] && [ "$(cr .status.state)" = "running" ]; then
        t_status="$(delta "${t0}" "$(now)")"
        log "trial ${i}: CR status converged after ${t_status}s"
      fi
    fi
    if [ -n "${t_engine}" ] && [ -n "${t_status}" ]; then break; fi
    sleep "${POLL}"
  done

  [ -n "${t_engine}" ] || { echo "trial ${i}: FAILED - no recreate within ${TIMEOUT}s" >&2; exit 1; }
  [ -n "${t_status}" ] || t_status="n/a"

  echo "${i},${old_id:0:12},${new_id:0:12},${t_engine},${t_status}" >>"${CSV}"
  printf '%d & \\texttt{%s} & \\texttt{%s} & %s & %s \\\\\n' \
    "${i}" "${old_id:0:12}" "${new_id:0:12}" "${t_engine}" "${t_status}" >>"${TEX}"

  if [ "${i}" -lt "${TRIALS}" ]; then
    log "settling ${SETTLE}s before the next trial"
    sleep "${SETTLE}"
  fi
done

stats="$(awk -F, 'NR>1{s+=$4; v[c++]=$4} END{
  for(i=0;i<c;i++)for(j=i+1;j<c;j++)if(v[j]<v[i]){t=v[i];v[i]=v[j];v[j]=t}
  printf "%d %.1f %.1f %.1f", c, v[0], v[c-1], s/c }' "${CSV}")"
set -- ${stats}
{
  echo "% generated by scripts/thesis-rq1-heal.sh on $(date -Is)"
  echo "\\newcommand{\\rqOneTrials}{$1}"
  echo "\\newcommand{\\rqOneMinHeal}{$2}"
  echo "\\newcommand{\\rqOneMaxHeal}{$3}"
  echo "\\newcommand{\\rqOneMeanHeal}{$4}"
  echo "\\newcommand{\\rqOneContainer}{\\texttt{${CONTAINER_NAME}}}"
  echo "\\newcommand{\\rqOneEngine}{$(d version --format '{{.Server.Version}}' 2>/dev/null | tr -d '\r')}"
} >"${MACROS}"

log "done: ${1} trials, heal ${2}-${3}s (mean ${4}s)"
column -s, -t <"${CSV}" 2>/dev/null || cat "${CSV}"
