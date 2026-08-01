[CmdletBinding()]
param(
    [string]$Namespace = "compound-trader",
    [string]$ExpectedContext = "docker-desktop",
    [int]$TimeoutSeconds = 180
)

$ErrorActionPreference = "Stop"
$ProbeName = "user-management-smoke-$PID"

function Assert-LastExitCode {
    param([string]$Message)

    if ($LASTEXITCODE -ne 0) {
        throw $Message
    }
}

foreach ($Command in @("docker", "kubectl")) {
    if (-not (Get-Command $Command -ErrorAction SilentlyContinue)) {
        throw "Required command is unavailable: $Command"
    }
}

docker info *> $null
Assert-LastExitCode "Docker engine is unavailable."

$CurrentContext = kubectl config current-context
Assert-LastExitCode "Could not read the Kubernetes context."

if ($CurrentContext -ne $ExpectedContext) {
    throw "Expected context '$ExpectedContext'; found '$CurrentContext'."
}

kubectl get namespace $Namespace *> $null
Assert-LastExitCode "Namespace '$Namespace' is unavailable."

Write-Host "Kubernetes context: $CurrentContext"

foreach ($Deployment in @(
    "postgres",
    "redis",
    "user-management"
)) {
    kubectl rollout status `
        "deployment/$Deployment" `
        --namespace $Namespace `
        --timeout="${TimeoutSeconds}s"

    Assert-LastExitCode "Deployment '$Deployment' is not ready."
}

kubectl wait `
    --namespace $Namespace `
    --for=condition=complete `
    job/user-management-migrate `
    --timeout="${TimeoutSeconds}s"

Assert-LastExitCode "Migration Job is not complete."

try {
    kubectl delete pod $ProbeName `
        --namespace $Namespace `
        --ignore-not-found `
        --wait=true *> $null

    $ProbeCommand = 'for i in $(seq 1 30); do curl -fsS http://user-management:3002/ready && exit 0; sleep 2; done; exit 1'

    kubectl run $ProbeName `
        --namespace $Namespace `
        --image=curlimages/curl:8.10.1 `
        --restart=Never `
        --command -- `
        sh -c $ProbeCommand

    Assert-LastExitCode "Could not create readiness probe."

    kubectl wait `
        --namespace $Namespace `
        --for=jsonpath='{.status.phase}'=Succeeded `
        "pod/$ProbeName" `
        --timeout="${TimeoutSeconds}s"

    Assert-LastExitCode "Readiness probe did not succeed."

    $ReadyResponse = kubectl logs `
        --namespace $Namespace `
        $ProbeName

    Assert-LastExitCode "Could not read readiness response."


    $ReadyText = $ReadyResponse -join [Environment]::NewLine
    if ($ReadyText -notmatch '"ready"\s*:\s*true') {
        throw "Unexpected readiness response: $ReadyResponse"
    }

    Write-Host "Readiness response: $ReadyText"
}
finally {
    kubectl delete pod $ProbeName `
        --namespace $Namespace `
        --ignore-not-found `
        --wait=true *> $null
}

$PostgresPod = kubectl get pods `
    --namespace $Namespace `
    --selector app=postgres `
    --output jsonpath='{.items[0].metadata.name}'

Assert-LastExitCode "Could not locate the PostgreSQL pod."

if (-not $PostgresPod) {
    throw "PostgreSQL pod name is empty."
}

$MigrationResult = kubectl exec `
    --namespace $Namespace `
    $PostgresPod `
    -- psql -U compound -d compound -Atc `
    "SELECT COUNT(*) FROM schema_migrations;"

Assert-LastExitCode "Could not query migration records."

$MigrationCount = [int](
    $MigrationResult |
        Select-Object -Last 1
)

if ($MigrationCount -ne 4) {
    throw "Expected four migration records; found $MigrationCount."
}

Write-Host "Applied migrations: $MigrationCount"

kubectl get deployments,pods,pvc `
    --namespace $Namespace `
    -o wide

Assert-LastExitCode "Could not retrieve final workload state."

Write-Host "Local Kubernetes smoke test passed."
