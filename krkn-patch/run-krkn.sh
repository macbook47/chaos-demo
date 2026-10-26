#!/usr/bin/env bash
# Run Krkn (krkn-hub) scenarios as a namespace-only user (no cluster-admin).
# Extracts krkn-lib from the scenario image, patches it, mounts the patched files.
#
#   SCENARIO=pod-scenarios        ./run-krkn.sh   (default)
#   SCENARIO=container-scenarios  ./run-krkn.sh
#   SCENARIO=application-outages  ./run-krkn.sh
# Any scenario variable set in your shell (FORCE, EXECUTION, ACTION, DURATION...)
# is passed through to the container.
set -euo pipefail

SCENARIO="${SCENARIO:-pod-scenarios}"
IMAGE="quay.io/krkn-chaos/krkn-hub:$SCENARIO"
PLATFORM="linux/amd64"
SITE="/usr/local/lib/python3.11/site-packages/krkn_lib/k8s"
DIR="$(cd "$(dirname "$0")" && pwd)"
PATCHED="$DIR/patched/$SCENARIO"
KUBECONFIG_FILE="$HOME/.krkn-kubeconfig"   # outside the repo: never commit it
export NAMESPACE="${NAMESPACE:-$(oc project -q)}"

# Defaults per scenario (all overridable from the shell)
case "$SCENARIO" in
  pod-scenarios)
    export POD_LABEL="${POD_LABEL:-deployment=chaos-api}" ;;
  container-scenarios)
    export LABEL_SELECTOR="${LABEL_SELECTOR:-deployment=chaos-api}"
    export CONTAINER_NAME="${CONTAINER_NAME:-chaos-api}" ;;
  application-outages)
    export POD_SELECTOR="${POD_SELECTOR:-{deployment: chaos-api}}"
    export DURATION="${DURATION:-30}" ;;
esac

# 1. Extract and patch krkn-lib once per scenario image
mkdir -p "$PATCHED" "$DIR/reports"
if [ ! -f "$PATCHED/pod_monitor.py" ] || [ "${REPATCH:-0}" = "1" ]; then
  echo ">> Extracting krkn-lib from $IMAGE"
  docker pull --platform "$PLATFORM" "$IMAGE" >/dev/null
  docker rm -f krkn-src >/dev/null 2>&1 || true
  docker create --platform "$PLATFORM" --name krkn-src "$IMAGE" >/dev/null
  docker cp "krkn-src:$SITE/pod_monitor/pod_monitor.py" "$PATCHED/pod_monitor.py"
  docker cp "krkn-src:$SITE/krkn_kubernetes.py" "$PATCHED/krkn_kubernetes.py"
  docker rm krkn-src >/dev/null
  python3 "$DIR/patch_krkn_lib.py" "$PATCHED/pod_monitor.py" "$PATCHED/krkn_kubernetes.py"
fi

# 2. Fresh kubeconfig from your current oc login
rm -f "$KUBECONFIG_FILE"
oc config view --flatten > "$KUBECONFIG_FILE"
chmod 644 "$KUBECONFIG_FILE"

# 3. Use the namespace Prometheus (monitoring/prometheus.yaml) if it is deployed
SLO_FILE="$DIR/../monitoring/slo.yaml"
SLO_ARGS=()
PROM_HOST="$(oc get route chaos-prometheus -o jsonpath='{.spec.host}' 2>/dev/null || true)"
if [ -n "$PROM_HOST" ] && [ -f "$SLO_FILE" ]; then
  export PROMETHEUS_URL="https://$PROM_HOST"
  SLO_ARGS=(-v "$SLO_FILE:/home/krkn/kraken/config/alerts.yaml:ro")
  echo ">> Resiliency scoring with $PROMETHEUS_URL and $(grep -c '^- expr' "$SLO_FILE") SLOs"
else
  echo ">> No chaos-prometheus route found: running without resiliency scoring"
fi

# 4. Pass through scenario variables that are set
ENV_ARGS=()
for v in NAMESPACE POD_LABEL NAME_PATTERN EXCLUDE_LABEL DISRUPTION_COUNT EXECUTION FORCE \
         KILL_TIMEOUT EXPECTED_RECOVERY_TIME LABEL_SELECTOR CONTAINER_NAME ACTION \
         DURATION POD_SELECTOR BLOCK_TRAFFIC_TYPE PROMETHEUS_URL; do
  if [ -n "${!v:-}" ]; then ENV_ARGS+=(-e "$v=${!v}"); fi
done

echo ">> Scenario: $SCENARIO"
printf '   %s\n' "${ENV_ARGS[@]}" | grep -v '^   -e$' || true
docker rm -f krkn-pod >/dev/null 2>&1 || true
docker run --platform "$PLATFORM" --name=krkn-pod \
  -v "$KUBECONFIG_FILE:/home/krkn/.kube/config" \
  -v "$PATCHED/pod_monitor.py:$SITE/pod_monitor/pod_monitor.py:ro" \
  -v "$PATCHED/krkn_kubernetes.py:$SITE/krkn_kubernetes.py:ro" \
  ${SLO_ARGS[@]+"${SLO_ARGS[@]}"} \
  "${ENV_ARGS[@]}" \
  -d "$IMAGE" >/dev/null

docker logs -f krkn-pod || true

# 5. Save the report and print the verdict
STAMP="$(date +%Y%m%d-%H%M%S)"
docker cp krkn-pod:/home/krkn/kraken/kraken.report.html "$DIR/reports/krkn-$SCENARIO-$STAMP.html" 2>/dev/null \
  && echo ">> Report: $DIR/reports/krkn-$SCENARIO-$STAMP.html" || echo ">> No report found"
CODE="$(docker inspect krkn-pod --format '{{.State.ExitCode}}')"
echo ">> Krkn exit code: $CODE (0 = pass)"
