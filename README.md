# Compound Trader Ecosystem

This repository contains 25 runnable service foundations with Docker, tests,
health checks, readiness checks, metrics, PostgreSQL/Redis integration,
migrations, Kubernetes manifests, Helm charts, and CI.

## Generate and open

```powershell
python compound_trader_enterprise_generator.py
cd .\compound-trader-ecosystem
code .
```

## Validate

```powershell
docker compose -f compose.yaml config
docker compose -f compose.yaml build
```

## Start infrastructure and services

```powershell
docker compose -f compose.yaml up -d
docker compose -f compose.yaml ps
```

## Run migrations

```powershell
powershell -ExecutionPolicy Bypass -File .\scriptsun-migrations.ps1
```

## Health check

```powershell
Invoke-RestMethod http://localhost:3022/health
```

## Monitoring

- Prometheus: http://localhost:9090
- Grafana: http://localhost:3030

## Engineering status

The generated repository has no ellipsis placeholders, missing source imports,
or intentionally empty database/cache client variables. It is a runnable
service foundation.

A complete trading product still requires real domain implementation, including
broker adapters, market-data providers, order lifecycle logic, authentication,
authorization, strategy execution, risk controls, billing, regulatory review,
security approval, load testing, failover testing, and production cloud
configuration.
