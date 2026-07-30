# Staging Deployment Readiness

## Scope

This repository provides validated staging configuration. It does not claim that a live staging cluster has been deployed.

## Required secrets

Supply these through the deployment platform or CI secret manager:

- `POSTGRES_PASSWORD`
- `JWT_ACCESS_SECRET`
- `JWT_REFRESH_SECRET`

Use distinct, randomly generated values for staging. Never commit real values or reuse production secrets.

## Compose validation

Validate the local configuration:

```powershell
docker compose -f compose.yaml config --quiet
```

Validate staging with secrets supplied through the environment:

```powershell
$env:POSTGRES_PASSWORD = "<secret>"
$env:JWT_ACCESS_SECRET = "<secret>"
$env:JWT_REFRESH_SECRET = "<different-secret>"

docker compose -f compose.yaml -f compose.staging.yaml config --quiet

Remove-Item Env:POSTGRES_PASSWORD
Remove-Item Env:JWT_ACCESS_SECRET
Remove-Item Env:JWT_REFRESH_SECRET
```

## Helm validation

Every service chart must pass `helm lint` before deployment.

## Live deployment prerequisites

- Kubernetes cluster and namespace
- Container registry and immutable image tags
- Approved PostgreSQL deployment with persistent storage
- Secret-manager integration
- DNS records and TLS certificates
- Ingress controller
- Monitoring and alert routing
- Database backup, restore, migration, and rollback procedures

## Release gate

Do not promote staging to production until:

1. CI passes Compose, Helm, builds, tests, PostgreSQL integration, and production dependency audit.
2. Images are vulnerability-scanned and referenced by immutable digest.
3. Database migrations and rollback are tested.
4. TLS, DNS, monitoring, backups, and alerting are verified.
5. Authentication smoke tests pass against the deployed staging endpoint.
