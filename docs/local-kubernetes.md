# Local Kubernetes Operations

## Scope

This runbook operates user-management on Docker Desktop Kubernetes for local verification. It does not represent a public staging or production deployment.

The stack contains PostgreSQL, Redis, persistent volumes, an idempotent migration Job, and two user-management replicas pinned to an immutable GHCR digest.

## Prerequisites

- Docker Desktop with Kubernetes enabled
- `docker` and `kubectl`
- Windows PowerShell 5.1 or later

Confirm the local cluster:

```powershell
docker info
kubectl config use-context docker-desktop
kubectl cluster-info
kubectl get nodes
```

The control-plane node must report `Ready`.

## Required Secret keys

Create `compound-runtime-secrets` outside source control with these keys:

- `database-url`
- `redis-url`
- `postgres-password`
- `jwt-access-secret`
- `jwt-refresh-secret`

Generate unique random values. Never commit, print, or reuse them in staging or production.

## Validate manifests

```powershell
$RepositoryRoot = (Resolve-Path ".").Path

docker run --rm `
    --volume "${RepositoryRoot}:/work:ro" `
    ghcr.io/yannh/kubeconform:v0.8.0@sha256:faffaf43f95aa6425306e1ab8d6fcad72acb9049158f38e574c085ea1ec0f64e `
    -strict `
    -summary `
    /work/services/user-management/k8s/namespace.yaml `
    /work/services/user-management/k8s/dependencies.yaml `
    /work/services/user-management/k8s/migration-job.yaml `
    /work/services/user-management/k8s/deployment.yaml
```

Expected result: 10 valid resources and zero errors.

## Deployment order

Apply the namespace first, create `compound-runtime-secrets`, and then continue with dependencies and migrations.

```powershell
kubectl apply -f services\user-management\k8s\namespace.yaml
# Create compound-runtime-secrets before continuing.
kubectl apply -f services\user-management\k8s\dependencies.yaml

kubectl rollout status deployment/postgres `
    --namespace compound-trader --timeout=180s

kubectl rollout status deployment/redis `
    --namespace compound-trader --timeout=180s

kubectl delete job user-management-migrate `
    --namespace compound-trader --ignore-not-found

kubectl apply -f services\user-management\k8s\migration-job.yaml

kubectl wait --namespace compound-trader `
    --for=condition=complete `
    job/user-management-migrate --timeout=180s

kubectl apply -f services\user-management\k8s\deployment.yaml

kubectl rollout status deployment/user-management `
    --namespace compound-trader --timeout=180s
```

Create `compound-runtime-secrets` after applying the namespace and before starting dependencies or migrations.

## Smoke test

```powershell
.\scripts\test-local-kubernetes.ps1
```

The script verifies rollouts, migration completion, `/ready`, four migration records, PVC state, and temporary-probe cleanup.

## Inspect resources

```powershell
kubectl get deployments,pods,jobs,services,pvc `
    --namespace compound-trader -o wide
```

## Restart recovery

Docker Desktop restarts stop the local cluster. Bound PVC data should remain available.

After Docker Desktop restarts:

```powershell
docker info
kubectl get nodes
kubectl get pods --namespace compound-trader
.\scripts\test-local-kubernetes.ps1
```

Wait until the node reports `Ready` before running the smoke test.

## Security boundaries

- Never commit Kubernetes Secret values.
- Never expose local PostgreSQL or Redis publicly.
- Keep application images pinned by digest.
- Use managed secrets, TLS, ingress controls, monitoring, and backups for real deployments.
- Docker Desktop is a local verification environment, not production.
