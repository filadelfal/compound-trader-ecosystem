# Market Data Deployment Guide

## Docker

```bash
docker build -t compoundtrader/market-data:1.0.0 .
```

## Kubernetes

Apply in order:

```bash
kubectl apply -f k8s/configmap.yaml
kubectl apply -f k8s/deployment.yaml
kubectl apply -f k8s/service.yaml
kubectl apply -f k8s/hpa.yaml
```

## Required Secrets

Secret: `compound-runtime-secrets`

Keys:
- `database-url`
- `redis-url`
- `jwt-secret`
- `api-keys`
- `twelvedata-api-key`
- `alphavantage-api-key`
- `polygon-api-key`
- `mt5-base-url`
