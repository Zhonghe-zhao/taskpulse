[CmdletBinding()]
param(
    [Parameter(Mandatory)]
    [string]$TaskID,
    [string]$TaskPulseURL = "http://127.0.0.1:18080",
    [string]$Namespace = "taskpulse",
    [string]$Label = "kubernetes-task-evidence",
    [string]$OutputDirectory = ".\artifacts\evidence",
    [string]$WorkerSelector = "app=llm-worker",
    [string]$TaskPulseSelector = "app=taskpulse"
)

$ErrorActionPreference = "Stop"
$base = $TaskPulseURL.TrimEnd('/')
$timestamp = [DateTimeOffset]::UtcNow.ToString("yyyyMMddTHHmmssZ")
$safeLabel = ($Label -replace "[^a-zA-Z0-9._-]", "-").Trim('-')
if ([string]::IsNullOrWhiteSpace($safeLabel)) {
    $safeLabel = "kubernetes-task-evidence"
}
$prefix = "{0}-{1}" -f $timestamp, $safeLabel

New-Item -ItemType Directory -Force -Path $OutputDirectory | Out-Null

$task = Invoke-RestMethod -Method Get -Uri "$base/tasks/$TaskID"
$events = Invoke-RestMethod -Method Get -Uri "$base/tasks/$TaskID/events"
$metrics = (Invoke-WebRequest -UseBasicParsing -Method Get -Uri "$base/metrics").Content
$pods = kubectl get pods -n $Namespace -o wide
$deployments = kubectl get deployments -n $Namespace
$workerLogs = kubectl logs -n $Namespace -l $WorkerSelector --all-containers --prefix --timestamps --since=10m
$taskPulseLogs = kubectl logs -n $Namespace -l $TaskPulseSelector --all-containers --prefix --timestamps --since=10m

$metadata = [ordered]@{
    captured_at_utc = [DateTimeOffset]::UtcNow.ToString("o")
    taskpulse_url   = $base
    namespace       = $Namespace
    task_id         = $TaskID
    label           = $Label
    task_status     = $task.status
    task_workflow   = $task.workflow
    task_version    = $task.version
    retry_count     = $task.retry_count
}

$paths = [ordered]@{
    task              = Join-Path $OutputDirectory "$prefix-task.json"
    events            = Join-Path $OutputDirectory "$prefix-events.json"
    metrics           = Join-Path $OutputDirectory "$prefix-metrics.prom"
    pods              = Join-Path $OutputDirectory "$prefix-pods.txt"
    deployments       = Join-Path $OutputDirectory "$prefix-deployments.txt"
    worker_logs       = Join-Path $OutputDirectory "$prefix-worker-logs.txt"
    taskpulse_logs    = Join-Path $OutputDirectory "$prefix-taskpulse-logs.txt"
    metadata          = Join-Path $OutputDirectory "$prefix-metadata.json"
}

$task | ConvertTo-Json -Depth 20 | Set-Content -Encoding utf8 -Path $paths.task
$events | ConvertTo-Json -Depth 20 | Set-Content -Encoding utf8 -Path $paths.events
$metrics | Set-Content -Encoding utf8 -Path $paths.metrics
$pods | Set-Content -Encoding utf8 -Path $paths.pods
$deployments | Set-Content -Encoding utf8 -Path $paths.deployments
$workerLogs | Set-Content -Encoding utf8 -Path $paths.worker_logs
$taskPulseLogs | Set-Content -Encoding utf8 -Path $paths.taskpulse_logs
$metadata | ConvertTo-Json -Depth 10 | Set-Content -Encoding utf8 -Path $paths.metadata

[pscustomobject]@{
    status       = $task.status
    workflow     = $task.workflow
    retry_count  = $task.retry_count
    output_files = $paths
} | ConvertTo-Json -Depth 10
