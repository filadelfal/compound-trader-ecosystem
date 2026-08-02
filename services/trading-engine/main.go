package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/redis/go-redis/v9"
)

type application struct {
	service string
	db      *pgxpool.Pool
	cache   *redis.Client
}

func jsonResponse(w http.ResponseWriter, status int, body map[string]any) {
	jsonValueResponse(w, status, body)
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

	app := &application{service: service, db: db, cache: cache}
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
		jsonResponse(w, http.StatusOK, map[string]any{"ready": true, "service": app.service})
	})
	mux.HandleFunc("/api/v1/ping", func(w http.ResponseWriter, r *http.Request) {
		jsonResponse(w, http.StatusOK, map[string]any{"message": "pong", "service": app.service})
	})
	executionAPIKey := getenv("EXECUTION_API_KEY", "")
	marketData := NewHTTPMarketDataClient(getenv("MARKET_DATA_URL", "http://market-data:3004"), 3*time.Second)
	orderRepository := NewPostgresOrderRepository(db, getenv("ALLOW_SHORT_SELLING", "false") == "true")
	orderService := NewOrderService(
		orderRepository,
		RiskPolicy{
			MaxOrderQuantity: getenv("MAX_ORDER_QUANTITY", "1000000"),
			MaxOrderNotional: getenv("MAX_ORDER_NOTIONAL", "10000000"),
		},
	)
	orderService.EnableMarketPretrade(marketData)
	paperAccounts, err := NewPaperAccountService(
		orderRepository, orderRepository,
		getenv("PAPER_STARTING_CASH", "100000"),
	)
	if err != nil {
		log.Fatalf("paper account configuration failed: %v", err)
	}
	registerOrderRoutes(mux, orderService)
	registerExecutionRoutes(mux, orderService, executionAPIKey)
	registerPortfolioRoutes(
		mux,
		orderService,
		executionAPIKey,
		marketData,
	)
	registerPaperAccountRoutes(mux, paperAccounts, getenv("PAPER_TRADING_ENABLED", "false") == "true")
	registerRiskRoutes(mux, NewRiskProfileService(orderRepository))
	workerCtx, stopWorker := context.WithCancel(ctx)
	defer stopWorker()
	if getenv("PAPER_TRADING_ENABLED", "false") == "true" {
		paperBroker := NewPaperBroker(
			NewPostgresPaperBrokerStore(db), marketData, orderService,
			getenvDuration("PAPER_BROKER_RETRY_BASE", 2*time.Second),
		)
		go paperBroker.Run(workerCtx, getenvDuration("PAPER_BROKER_POLL_INTERVAL", 250*time.Millisecond))
		log.Printf("paper broker execution enabled")
	}

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
	stopWorker()

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		log.Printf("graceful shutdown failed: %v", err)
	}
}

func getenvDuration(key string, fallback time.Duration) time.Duration {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	parsed, err := time.ParseDuration(value)
	if err != nil || parsed <= 0 {
		return fallback
	}
	return parsed
}

func getenv(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
