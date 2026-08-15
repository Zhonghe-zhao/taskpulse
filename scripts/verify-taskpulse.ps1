[CmdletBinding()]
param(
    [switch]$SkipMemoBridge,
    [switch]$SkipKubernetes,
    [switch]$BuildIntegration
)

$ErrorActionPreference = "Stop"
$taskPulseRoot = Split-Path -Parent $PSScriptRoot
$memoBridgeRoot = Join-Path (Split-Path -Parent $taskPulseRoot) "memobridge"

Push-Location $taskPulseRoot
try {
    Write-Host "[1/5] TaskPulse tests"
    go test ./...

    Write-Host "[2/5] TaskPulse Compose config"
    docker compose -f compose.yaml config --quiet
    docker compose -f compose.integration.yaml config --quiet

    if (-not $SkipMemoBridge) {
        if (-not (Test-Path (Join-Path $memoBridgeRoot "go.mod"))) {
            throw "MemoBridge sibling repository not found: $memoBridgeRoot"
        }
        Write-Host "[3/5] MemoBridge tests"
        Push-Location $memoBridgeRoot
        try {
            go test ./...
        }
        finally {
            Pop-Location
        }
    }
    else {
        Write-Host "[3/5] MemoBridge tests skipped"
    }

    if (-not $SkipKubernetes) {
        Write-Host "[4/5] Kubernetes manifest render"
        kubectl kustomize (Join-Path $taskPulseRoot "deploy/k8s") | Out-Null
    }
    else {
        Write-Host "[4/5] Kubernetes manifest render skipped"
    }

    if ($BuildIntegration) {
        Write-Host "[5/5] Integration image build"
        docker compose -f compose.integration.yaml build
    }
    else {
        Write-Host "[5/5] Integration image build skipped; use -BuildIntegration when Docker Desktop is running"
    }

    Write-Host "Verification completed successfully." -ForegroundColor Green
}
finally {
    Pop-Location
}
