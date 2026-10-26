// chaos-api: a dummy "teller" API for chaos engineering practice.
// No database: every response is generated in memory.
//
// Chaos knobs (set with `oc set env deployment/chaos-api KEY=value`):
//
//	LATENCY_MS  extra delay added to every /api call (default 0)
//	ERROR_RATE  fraction of /api calls that return HTTP 500, 0.0-1.0 (default 0)
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"math/rand"
	"net/http"
	"os"
	"os/signal"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

var (
	podName   = getenv("HOSTNAME", "local")
	startedAt = time.Now()
	ready     atomic.Bool
	txCounter atomic.Int64
)

func getenv(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Served-By", podName)
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

// ---------- Prometheus metrics (stdlib only, text exposition format) ----------

var buckets = []float64{0.05, 0.1, 0.25, 0.5, 1, 2.5, 5}

type histo struct {
	counts []uint64 // cumulative per bucket
	sum    float64
	count  uint64
}

var (
	mu       sync.Mutex
	requests = map[[2]string]uint64{} // {endpoint, code} -> count
	latency  = map[string]*histo{}    // endpoint -> histogram
)

func observe(endpoint string, code int, secs float64) {
	mu.Lock()
	defer mu.Unlock()
	requests[[2]string{endpoint, strconv.Itoa(code)}]++
	h := latency[endpoint]
	if h == nil {
		h = &histo{counts: make([]uint64, len(buckets))}
		latency[endpoint] = h
	}
	for i, b := range buckets {
		if secs <= b {
			h.counts[i]++
		}
	}
	h.sum += secs
	h.count++
}

func metricsHandler(w http.ResponseWriter, r *http.Request) {
	mu.Lock()
	defer mu.Unlock()
	var b strings.Builder
	b.WriteString("# TYPE chaos_http_requests_total counter\n")
	keys := make([][2]string, 0, len(requests))
	for k := range requests {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i][0]+keys[i][1] < keys[j][0]+keys[j][1] })
	for _, k := range keys {
		fmt.Fprintf(&b, "chaos_http_requests_total{endpoint=%q,code=%q} %d\n", k[0], k[1], requests[k])
	}
	b.WriteString("# TYPE chaos_http_request_duration_seconds histogram\n")
	for ep, h := range latency {
		for i, le := range buckets {
			fmt.Fprintf(&b, "chaos_http_request_duration_seconds_bucket{endpoint=%q,le=%q} %d\n", ep, strconv.FormatFloat(le, 'f', -1, 64), h.counts[i])
		}
		fmt.Fprintf(&b, "chaos_http_request_duration_seconds_bucket{endpoint=%q,le=\"+Inf\"} %d\n", ep, h.count)
		fmt.Fprintf(&b, "chaos_http_request_duration_seconds_sum{endpoint=%q} %g\n", ep, h.sum)
		fmt.Fprintf(&b, "chaos_http_request_duration_seconds_count{endpoint=%q} %d\n", ep, h.count)
	}
	w.Header().Set("Content-Type", "text/plain; version=0.0.4")
	w.Write([]byte(b.String()))
}

type statusRecorder struct {
	http.ResponseWriter
	code int
}

func (s *statusRecorder) WriteHeader(code int) {
	s.code = code
	s.ResponseWriter.WriteHeader(code)
}

// chaos wraps /api handlers with configurable latency and error injection,
// and records request count and latency per endpoint.
// Knobs: LATENCY_MS (extra delay), ERROR_RATE (0.0-1.0 fraction of HTTP 500s).
func chaos(endpoint string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, code: 200}
		defer func() {
			observe(endpoint, rec.code, time.Since(start).Seconds())
			log.Printf("%s %s -> %d (%s)", r.Method, r.URL.Path, rec.code, time.Since(start))
		}()
		if ms, _ := strconv.Atoi(os.Getenv("LATENCY_MS")); ms > 0 {
			time.Sleep(time.Duration(ms) * time.Millisecond)
		}
		if rate, _ := strconv.ParseFloat(os.Getenv("ERROR_RATE"), 64); rate > 0 && rand.Float64() < rate {
			writeJSON(rec, http.StatusInternalServerError, map[string]string{
				"error": "injected failure (ERROR_RATE)", "pod": podName,
			})
			return
		}
		next(rec, r)
	}
}

func main() {
	mux := http.NewServeMux()

	// Probes for OpenShift. Readiness flips to false on shutdown so the
	// Service stops sending traffic before the pod exits.
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]string{"status": "alive"})
	})
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
		if !ready.Load() {
			writeJSON(w, 503, map[string]string{"status": "not ready"})
			return
		}
		writeJSON(w, 200, map[string]string{"status": "ready"})
	})

	mux.HandleFunc("GET /metrics", metricsHandler)

	// 1. Which pod answered? Key signal when you kill pods.
	mux.HandleFunc("GET /api/info", chaos("info", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]any{
			"pod":        podName,
			"version":    getenv("APP_VERSION", "v1"),
			"uptime_sec": int(time.Since(startedAt).Seconds()),
		})
	}))

	// 2. Balance inquiry (dummy).
	mux.HandleFunc("GET /api/accounts/{id}", chaos("accounts", func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		writeJSON(w, 200, map[string]any{
			"account_no": id,
			"name":       "Nasabah Dummy " + id,
			"balance":    1_000_000 + rand.Intn(9_000_000),
			"currency":   "IDR",
			"pod":        podName,
		})
	}))

	// 3. Transfer (dummy). Echoes the request and returns a transaction ID.
	mux.HandleFunc("POST /api/transfer", chaos("transfer", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			From   string `json:"from"`
			To     string `json:"to"`
			Amount int    `json:"amount"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Amount <= 0 {
			writeJSON(w, 400, map[string]string{"error": "body must be {from, to, amount>0}"})
			return
		}
		n := txCounter.Add(1)
		writeJSON(w, 200, map[string]any{
			"tx_id":  fmt.Sprintf("TX-%s-%06d", podName[max(0, len(podName)-5):], n),
			"status": "SUCCESS",
			"from":   req.From, "to": req.To, "amount": req.Amount,
			"pod": podName,
		})
	}))

	// 4. Recent transactions (dummy).
	mux.HandleFunc("GET /api/transactions", chaos("transactions", func(w http.ResponseWriter, r *http.Request) {
		items := make([]map[string]any, 5)
		for i := range items {
			items[i] = map[string]any{
				"tx_id":  fmt.Sprintf("TX-HIST-%03d", i+1),
				"amount": (i + 1) * 50_000,
				"time":   time.Now().Add(-time.Duration(i) * time.Hour).Format(time.RFC3339),
			}
		}
		writeJSON(w, 200, map[string]any{"items": items, "pod": podName})
	}))

	srv := &http.Server{Addr: ":" + getenv("PORT", "8080"), Handler: mux}

	// Graceful shutdown on SIGTERM (what OpenShift sends when a pod is deleted).
	go func() {
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, syscall.SIGTERM, syscall.SIGINT)
		<-sig
		log.Println("SIGTERM received: marking not ready, draining")
		ready.Store(false)
		time.Sleep(5 * time.Second) // let the Service/router notice
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		srv.Shutdown(ctx)
	}()

	ready.Store(true)
	log.Printf("chaos-api listening on %s (pod %s)", srv.Addr, podName)
	if err := srv.ListenAndServe(); err != http.ErrServerClosed {
		log.Fatal(err)
	}
	log.Println("stopped cleanly")
}
