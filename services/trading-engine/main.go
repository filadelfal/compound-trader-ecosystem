package main

import (
    "context"
    "encoding/json"
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
    w.Header().Set("Content-Type", "application/json")
    w.WriteHeader(status)
    _ = json.NewEncoder(w).Encode(body)
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
