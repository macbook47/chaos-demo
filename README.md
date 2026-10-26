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
