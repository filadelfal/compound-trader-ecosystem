package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/redis/go-redis/v9"
)

type application struct {
	service                  string
	db                       *pgxpool.Pool
	cache                    *redis.Client
	paperStore               paperOrderStore
	lifecycleStore           paperLifecycleStore
	automationStore          paperAutomationStore
	automationMu             *sync.Mutex
	automationFinalState     func(paperAutomationRequest) positionSizeRequest
	automationAcceptanceGate func(context.Context, paperAutomationRequest) string
	operationsAcceptanceGate func(context.Context, paperAutomationRequest) string
	operationsPaperGate      func(context.Context) string
	operationsRecordAccepted func(context.Context, paperAutomationRequest, automationSetup, paperOrder, paperPosition) error
	operationsRecordCycle    func(context.Context, cycleRecord) error
	now                      func() time.Time
}

func jsonResponse(w http.ResponseWriter, status int, body map[string]any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func (app *application) positionSizeHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		jsonResponse(w, http.StatusMethodNotAllowed, map[string]any{"error": "method_not_allowed"})
		return
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	decoder.DisallowUnknownFields()
	var input positionSizeRequest
	if err := decoder.Decode(&input); err != nil {
		jsonResponse(w, http.StatusBadRequest, map[string]any{"outcome": "NO_TRADE", "reasons": []string{"INVALID_INPUT"}})
		return
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		jsonResponse(w, http.StatusBadRequest, map[string]any{"outcome": "NO_TRADE", "reasons": []string{"INVALID_INPUT"}})
		return
	}
	result := calculatePositionSize(input)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(result)
}

func main() {
	ctx := context.Background()
	service := getenv("SERVICE_NAME", "trading-engine")
	port := getenv("PORT", "3003")
	databaseURL := getenv("DATABASE_URL", "postgresql://compound:compound_dev_password@postgres:5432/compound")
	redisURL := getenv("REDIS_URL", "redis://redis:6379/0")

	db, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		log.Fatalf("database configuration failed: %v", err)
	}
	defer db.Close()

	redisOptions, err := redis.ParseURL(redisURL)
	if err != nil {
		log.Fatalf("redis configuration failed: %v", err)
	}
	cache := redis.NewClient(redisOptions)
	defer cache.Close()

	app := &application{
		service:         service,
		db:              db,
		cache:           cache,
		paperStore:      redisPaperOrderStore{client: cache, retention: paperOrderRetention},
		lifecycleStore:  redisPaperLifecycleStore{client: cache, retention: paperLifecycleRetention},
		automationStore: redisPaperAutomationStore{client: cache, retention: paperLifecycleRetention},
		automationMu:    &sync.Mutex{},
		automationFinalState: func(request paperAutomationRequest) positionSizeRequest {
			return request.Risk
		},
		now: time.Now,
	}
	runtimeConfig, err := envRuntimeConfig()
	if err != nil {
		log.Fatalf("paper automation runtime configuration failed: %v", err)
	}
	runtimeProvider, err := envRuntimeProvider(runtimeConfig)
	if err != nil {
		log.Fatalf("paper automation runtime provider failed: %v", err)
	}
	runtime, err := newPaperRuntime(runtimeConfig, app, redisRuntimeStore{client: cache, prefix: "trading-engine:paper-runtime:"}, runtimeProvider)
	if err != nil {
		log.Fatalf("paper automation runtime startup failed: %v", err)
	}
	operationsStore := redisPaperOperationsStore{client: cache, retention: operationsRetention}
	operations := newPaperOperations(operationsStore, app.currentTime, func(evidenceCtx context.Context) (operationsEvidence, error) {
		return collectRedisOperationsEvidence(evidenceCtx, app, operationsStore, 10000)
	})
	securityConfig, err := envOperatorSecurityConfig()
	if err != nil {
		log.Fatalf("operator security configuration failed")
	}
	operatorAuth := &operatorAuthorizer{cfg: securityConfig, replay: redisOperatorReplayStore{client: cache}, now: app.currentTime}
	operatorAPI := &operatorAPI{auth: operatorAuth, operations: operations, limiter: redisOperatorRateLimiter{client: cache}, commands: redisOperatorCommandStore{client: cache, retention: operationsRetention}}
	app.operationsAcceptanceGate = operations.acceptanceGate
	app.operationsPaperGate = func(gateCtx context.Context) string {
		return operations.acceptanceGate(gateCtx, paperAutomationRequest{})
	}
	app.operationsRecordAccepted = operations.recordAccepted
	app.operationsRecordCycle = operations.recordCycle
	startupCtx, startupCancel := context.WithTimeout(ctx, 15*time.Second)
	if _, _, err := operations.reconcile(startupCtx, "startup"); err != nil {
		log.Printf("paper operations startup reconciliation failed closed: %v", err)
	}
	startupCancel()
	operations.Start(ctx, 5*time.Minute)
	runtime.Start(ctx)
	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.Handler())
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		jsonResponse(w, http.StatusOK, map[string]any{"status": "ok", "service": app.service})
	})
	mux.HandleFunc("/ready", func(w http.ResponseWriter, r *http.Request) {
		if err := app.db.Ping(r.Context()); err != nil {
			jsonResponse(w, http.StatusServiceUnavailable, map[string]any{"ready": false, "dependency": "postgres"})
			return
		}
		if err := app.cache.Ping(r.Context()).Err(); err != nil {
			jsonResponse(w, http.StatusServiceUnavailable, map[string]any{"ready": false, "dependency": "redis"})
			return
		}
		if ok, reason := operatorAuth.readiness(r.Context()); !ok {
			operatorSecurityReady.Set(0)
			jsonResponse(w, http.StatusServiceUnavailable, map[string]any{"ready": false, "dependency": "operator-security", "reason": reason})
			return
		}
		operatorSecurityReady.Set(1)
		runtimeState := runtime.Status()
		if runtimeState.Enabled && !runtimeState.Ready {
			jsonResponse(w, http.StatusServiceUnavailable, map[string]any{"ready": false, "dependency": "paper-automation-runtime", "state": runtimeState.State, "reasons": runtimeState.Reasons})
			return
		}
		operationsState := operations.status()
		if ready, _ := operationsState["ready"].(bool); !ready {
			jsonResponse(w, http.StatusServiceUnavailable, map[string]any{"ready": false, "dependency": "paper-operations", "state": operationsState})
			return
		}
		jsonResponse(w, http.StatusOK, map[string]any{"ready": true, "service": app.service})
	})
	mux.HandleFunc("/api/v1/ping", func(w http.ResponseWriter, r *http.Request) {
		jsonResponse(w, http.StatusOK, map[string]any{"message": "pong", "service": app.service})
	})
	mux.HandleFunc("/api/v1/risk/position-size", app.positionSizeHandler)
	mux.HandleFunc("/api/v1/paper/orders", app.paperOrderHandler)
	mux.HandleFunc("/api/v1/paper/positions", app.openPaperPositionHandler)
	mux.HandleFunc("/api/v1/paper/positions/quotes", app.paperQuoteHandler)
	mux.HandleFunc("/api/v1/paper/journal", operatorAPI.protect(permissionRead, app.paperJournalHandler))
	mux.HandleFunc("/api/v1/backtests", app.backtestHandler)
	mux.HandleFunc("/api/v1/backtests/performance", app.performanceHandler)
	mux.HandleFunc("/api/v1/backtests/walk-forward", app.walkForwardHandler)
	mux.HandleFunc("/api/v1/paper-automation/evaluate", app.paperAutomationHandler)
	mux.HandleFunc("/api/v1/paper-automation/runtime", operatorAPI.protect(permissionRead, runtime.statusHandler))
	mux.HandleFunc("/api/v1/paper-automation/runtime/cycles", operatorAPI.protect(permissionRead, runtime.cyclesHandler))
	mux.HandleFunc("/api/v1/paper-operations/status", operatorAPI.handler(permissionRead, "STATUS", false))
	mux.HandleFunc("/api/v1/paper-operations/findings", operatorAPI.handler(permissionRead, "FINDINGS", false))
	mux.HandleFunc("/api/v1/paper-operations/ledger", operatorAPI.handler(permissionRead, "LEDGER", false))
	mux.HandleFunc("/api/v1/paper-operations/rollup", operatorAPI.handler(permissionRead, "ROLLUP", false))
	mux.HandleFunc("/api/v1/paper-operations/pause", operatorAPI.handler(permissionPause, "PAUSE", true))
	mux.HandleFunc("/api/v1/paper-operations/resume", operatorAPI.handler(permissionResume, "RESUME", true))
	mux.HandleFunc("/api/v1/paper-operations/kill-switch/activate", operatorAPI.handler(permissionKill, "KILL_SWITCH_ACTIVATE", true))
	mux.HandleFunc("/api/v1/paper-operations/kill-switch/release-request", operatorAPI.handler(permissionRelease, "KILL_SWITCH_RELEASE_REQUEST", true))
	mux.HandleFunc("/api/v1/paper-operations/reconciliation/run", operatorAPI.handler(permissionReconcile, "RECONCILE", true))
	mux.HandleFunc("/api/v1/paper-operations/repairs/preview", operatorAPI.handler(permissionRepairView, "REPAIR_PREVIEW", true))
	mux.HandleFunc("/api/v1/paper-operations/repairs/apply", operatorAPI.handler(permissionRepairApply, "REPAIR_APPLY", true))

	server := &http.Server{
		Addr:              "0.0.0.0:" + port,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	go func() {
		log.Printf("%s listening on %s", service, port)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("server failed: %v", err)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := runtime.Stop(shutdownCtx); err != nil {
		log.Printf("paper automation runtime shutdown failed: %v", err)
	}
	operations.Stop()
	if err := server.Shutdown(shutdownCtx); err != nil {
		log.Printf("graceful shutdown failed: %v", err)
	}
}

func getenv(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
