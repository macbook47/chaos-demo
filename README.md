# chaos-demo

Dummy teller app for practicing chaos engineering on OpenShift.

- `api/`: Go API, stdlib only, no database. Endpoints: `GET /api/info`, `GET /api/accounts/{id}`, `POST /api/transfer`, `GET /api/transactions`, plus `/healthz` and `/readyz` probes.
- `ui/`: Vue 3 console. Runs continuous traffic and shows success rate, latency and which pod answered. nginx proxies `/api` to the `chaos-api` Service.

Chaos knobs on the API: `LATENCY_MS` (extra delay per call) and `ERROR_RATE` (0.0 to 1.0, fraction of calls returning 500).

## Deploy (Developer Sandbox)

```bash
oc new-app https://github.com/<you>/chaos-demo --context-dir=api --strategy=docker --name=chaos-api
oc new-app https://github.com/<you>/chaos-demo --context-dir=ui  --strategy=docker --name=chaos-ui
oc create route edge --service=chaos-ui
oc set probe deployment/chaos-api --readiness --get-url=http://:8080/readyz
oc set probe deployment/chaos-api --liveness  --get-url=http://:8080/healthz --initial-delay-seconds=5
oc scale deployment/chaos-api --replicas=2
oc get route chaos-ui
```

## Local run

```bash
cd api && go run .            # :8080
cd ui && npm install && npm run dev   # :5173, proxies /api to :8080
```

## Krkn for namespace-only users (`krkn-patch/`)

Stock Krkn pod scenarios list pods cluster-wide and fail with 403 for users without cluster-admin.
`patch_krkn_lib.py` makes krkn-lib use namespaced API calls when the namespace is a plain name
(regex patterns keep the original behaviour). Tested against krkn-lib 7.0.3.

```bash
./krkn-patch/run-krkn.sh                       # current oc project, label deployment=chaos-api
NAMESPACE=qa-nds POD_LABEL=app=nds ./krkn-patch/run-krkn.sh
REPATCH=1 ./krkn-patch/run-krkn.sh             # re-extract after the Krkn image updates
```

## Monitoring without cluster access (`monitoring/`)

- The API exposes Prometheus metrics at `/metrics` (request counts and latency per endpoint).
- `monitoring/prometheus.yaml` runs Prometheus inside your namespace (pod discovery limited to it).
- `monitoring/slo.yaml` holds app-level SLOs; `run-krkn.sh` mounts it and points Krkn at the
  `chaos-prometheus` route automatically, which enables the resiliency score.

```bash
oc apply -f monitoring/prometheus.yaml
oc start-build chaos-api          # rebuild the API with /metrics
./krkn-patch/run-krkn.sh
```
