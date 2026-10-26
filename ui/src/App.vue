<script setup>
import { ref, computed, onUnmounted } from 'vue'

// ---------- Continuous traffic (what you watch during a chaos experiment) ----------
const running = ref(false)
const intervalMs = ref(500)
const samples = ref([]) // { ok, ms, pod, status, at }
const MAX_SAMPLES = 120
let timer = null
let tick = 0

async function call(method, path, body) {
  const ctrl = new AbortController()
  const timeout = setTimeout(() => ctrl.abort(), 5000)
  const t0 = performance.now()
  try {
    const res = await fetch(path, {
      method,
      headers: body ? { 'Content-Type': 'application/json' } : {},
      body: body ? JSON.stringify(body) : undefined,
      signal: ctrl.signal
    })
    const ms = Math.round(performance.now() - t0)
    const text = await res.text()
    let data
    try { data = JSON.parse(text) } catch { data = text }
    return { ok: res.ok, status: res.status, ms, pod: res.headers.get('X-Served-By') || '-', data }
  } catch (e) {
    const ms = Math.round(performance.now() - t0)
    return { ok: false, status: e.name === 'AbortError' ? 'timeout' : 'network', ms, pod: '-', data: String(e) }
  } finally {
    clearTimeout(timeout)
  }
}

async function fireOne() {
  tick++
  const r = tick % 2
    ? await call('GET', '/api/info')
    : await call('POST', '/api/transfer', { from: '0001', to: '0002', amount: 10000 })
  samples.value.push({ ok: r.ok, ms: r.ms, pod: r.pod, status: r.status, at: new Date() })
  if (samples.value.length > MAX_SAMPLES) samples.value.shift()
}

function start() {
  if (running.value) return
  running.value = true
  timer = setInterval(fireOne, intervalMs.value)
}
function stop() {
  running.value = false
  clearInterval(timer)
}
function reset() { samples.value = [] }
onUnmounted(stop)

const stats = computed(() => {
  const s = samples.value
  if (!s.length) return null
  const ok = s.filter(x => x.ok)
  const sorted = [...s.map(x => x.ms)].sort((a, b) => a - b)
  const p95 = sorted[Math.min(sorted.length - 1, Math.floor(sorted.length * 0.95))]
  const pods = {}
  for (const x of ok) pods[x.pod] = (pods[x.pod] || 0) + 1
  return {
    total: s.length,
    successPct: ((ok.length / s.length) * 100).toFixed(1),
    avgMs: Math.round(s.reduce((a, x) => a + x.ms, 0) / s.length),
    p95,
    failures: s.length - ok.length,
    pods
  }
})

const maxMs = computed(() => Math.max(300, ...samples.value.map(x => x.ms)))
function barClass(x) {
  if (!x.ok) return 'fail'
  return x.ms > 1000 ? 'slow' : 'ok'
}

// ---------- Manual calls (one request at a time, full response shown) ----------
const accountId = ref('0001234567')
const last = ref(null)
const endpoints = [
  { label: 'Check pod info', run: () => call('GET', '/api/info') },
  { label: 'Check balance', run: () => call('GET', `/api/accounts/${accountId.value}`) },
  { label: 'Send transfer', run: () => call('POST', '/api/transfer', { from: accountId.value, to: '0009876543', amount: 250000 }) },
  { label: 'List transactions', run: () => call('GET', '/api/transactions') }
]
async function runManual(ep) {
  last.value = { label: ep.label, pending: true }
  const r = await ep.run()
  last.value = { label: ep.label, ...r }
}
</script>

<template>
  <main>
    <header>
      <h1>Chaos teller console</h1>
      <p>Keep traffic running, break something in OpenShift, and watch what the customer would see.</p>
    </header>

    <section class="panel">
      <div class="controls">
        <button v-if="!running" class="primary" @click="start">Start traffic</button>
        <button v-else class="primary stop" @click="stop">Stop traffic</button>
        <label>
          Every
          <input type="number" min="100" step="100" v-model.number="intervalMs" :disabled="running" /> ms
        </label>
        <button @click="reset" :disabled="!samples.length">Clear results</button>
      </div>

      <div class="strip" aria-label="Recent requests, height shows latency">
        <div
          v-for="(x, i) in samples"
          :key="i"
          class="bar"
          :class="barClass(x)"
          :style="{ height: Math.max(6, (x.ms / maxMs) * 100) + '%' }"
          :title="`${x.status} · ${x.ms} ms · ${x.pod}`"
        />
        <p v-if="!samples.length" class="empty">Start traffic to see each request here. Green is OK, amber is slower than 1 s, red is failed.</p>
      </div>

      <dl v-if="stats" class="stats">
        <div><dt>Requests</dt><dd>{{ stats.total }}</dd></div>
        <div><dt>Success</dt><dd :class="{ bad: stats.failures }">{{ stats.successPct }}%</dd></div>
        <div><dt>Avg latency</dt><dd>{{ stats.avgMs }} ms</dd></div>
        <div><dt>p95 latency</dt><dd>{{ stats.p95 }} ms</dd></div>
      </dl>

      <div v-if="stats" class="pods">
        <h2>Served by</h2>
        <ul>
          <li v-for="(n, pod) in stats.pods" :key="pod"><code>{{ pod }}</code> {{ n }} requests</li>
        </ul>
      </div>
    </section>

    <section class="panel">
      <h2>Call one endpoint</h2>
      <label class="acct">Account number <input v-model="accountId" /></label>
      <div class="controls">
        <button v-for="ep in endpoints" :key="ep.label" @click="runManual(ep)">{{ ep.label }}</button>
      </div>
      <div v-if="last" class="result">
        <p>
          <strong>{{ last.label }}</strong>
          <span v-if="last.pending"> waiting…</span>
          <span v-else :class="last.ok ? 'okText' : 'bad'"> {{ last.status }} in {{ last.ms }} ms from {{ last.pod }}</span>
        </p>
        <pre v-if="!last.pending">{{ JSON.stringify(last.data, null, 2) }}</pre>
      </div>
    </section>
  </main>
</template>

<style>
:root {
  --navy: #12355b;
  --paper: #f5f7fa;
  --ink: #1d2733;
  --muted: #5b6878;
  --ok: #1f8a5b;
  --slow: #c48a00;
  --fail: #c2362b;
  --line: #d9e0e8;
  font-family: system-ui, -apple-system, "Segoe UI", Roboto, sans-serif;
  color: var(--ink);
  background: var(--paper);
}
body { margin: 0; }
main { max-width: 900px; margin: 0 auto; padding: 32px 20px 64px; }
header h1 { margin: 0 0 4px; font-size: 1.75rem; color: var(--navy); }
header p { margin: 0 0 24px; color: var(--muted); max-width: 60ch; }
h2 { font-size: 1rem; margin: 0 0 12px; color: var(--navy); }

.panel { background: #fff; border: 1px solid var(--line); border-radius: 10px; padding: 20px; margin-bottom: 20px; }
.controls { display: flex; flex-wrap: wrap; gap: 10px; align-items: center; margin-bottom: 16px; }
button {
  font: inherit; padding: 8px 14px; border-radius: 6px; cursor: pointer;
  border: 1px solid var(--line); background: #fff; color: var(--ink);
}
button:hover:not(:disabled) { border-color: var(--navy); }
button:disabled { opacity: .5; cursor: default; }
button:focus-visible, input:focus-visible { outline: 2px solid var(--navy); outline-offset: 2px; }
button.primary { background: var(--navy); color: #fff; border-color: var(--navy); }
button.primary.stop { background: var(--fail); border-color: var(--fail); }
input { font: inherit; padding: 6px 8px; border: 1px solid var(--line); border-radius: 6px; width: 90px; }
.acct input { width: 160px; margin-left: 6px; }
.acct { display: block; margin-bottom: 12px; }

.strip {
  height: 140px; display: flex; align-items: flex-end; gap: 2px;
  border-bottom: 2px solid var(--line); margin-bottom: 16px; position: relative;
}
.bar { flex: 1 1 0; min-width: 3px; border-radius: 2px 2px 0 0; }
.bar.ok { background: var(--ok); }
.bar.slow { background: var(--slow); }
.bar.fail { background: var(--fail); height: 100% !important; opacity: .85; }
.empty { color: var(--muted); margin: auto; text-align: center; max-width: 46ch; }

.stats { display: grid; grid-template-columns: repeat(4, 1fr); gap: 12px; margin: 0 0 16px; }
.stats div { border-left: 3px solid var(--line); padding-left: 10px; }
.stats dt { color: var(--muted); font-size: .85rem; }
.stats dd { margin: 2px 0 0; font-size: 1.4rem; font-weight: 600; font-variant-numeric: tabular-nums; }
.bad { color: var(--fail); }
.okText { color: var(--ok); }
.pods ul { margin: 0; padding-left: 18px; }
.pods li { margin: 2px 0; }

.result pre {
  background: var(--paper); border: 1px solid var(--line); border-radius: 6px;
  padding: 12px; overflow-x: auto; font-size: .85rem;
}
@media (max-width: 600px) { .stats { grid-template-columns: repeat(2, 1fr); } }
@media (prefers-reduced-motion: reduce) { * { transition: none !important; } }
</style>
