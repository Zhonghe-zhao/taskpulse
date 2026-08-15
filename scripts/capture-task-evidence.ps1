[CmdletBinding()]
param(
    [Parameter(Mandatory)]
    [string]$TaskID,
    [string]$BaseURL = "http://127.0.0.1:8085",
    [string]$Label = "task-evidence",
    [string]$OutputDirectory = ".\artifacts\evidence"
)

$ErrorActionPreference = "Stop"
$base = $BaseURL.TrimEnd('/')
$timestamp = [DateTimeOffset]::UtcNow.ToString("yyyyMMddTHHmmssZ")
$safeLabel = ($Label -replace "[^a-zA-Z0-9._-]", "-").Trim('-')
if ([string]::IsNullOrWhiteSpace($safeLabel)) {
    $safeLabel = "task-evidence"
}
$prefix = "{0}-{1}" -f $timestamp, $safeLabel

New-Item -ItemType Directory -Force -Path $OutputDirectory | Out-Null

$task = Invoke-RestMethod -Method Get -Uri "$base/tasks/$TaskID"
$events = Invoke-RestMethod -Method Get -Uri "$base/tasks/$TaskID/events"
$metricsResponse = Invoke-WebRequest -UseBasicParsing -Method Get -Uri "$base/metrics"

$metadata = [ordered]@{
    captured_at_utc = [DateTimeOffset]::UtcNow.ToString("o")
    base_url        = $base
    task_id         = $TaskID
    label           = $Label
    task_status     = $task.status
    task_workflow   = $task.workflow
    task_version    = $task.version
}

$taskPath = Join-Path $OutputDirectory "$prefix-task.json"
$eventsPath = Join-Path $OutputDirectory "$prefix-events.json"
$metricsPath = Join-Path $OutputDirectory "$prefix-metrics.prom"
$metadataPath = Join-Path $OutputDirectory "$prefix-metadata.json"

$task | ConvertTo-Json -Depth 20 | Set-Content -Encoding utf8 -Path $taskPath
$events | ConvertTo-Json -Depth 20 | Set-Content -Encoding utf8 -Path $eventsPath
$metricsResponse.Content | Set-Content -Encoding utf8 -Path $metricsPath
$metadata | ConvertTo-Json -Depth 10 | Set-Content -Encoding utf8 -Path $metadataPath

[pscustomobject]@{
    task     = $taskPath
    events   = $eventsPath
    metrics  = $metricsPath
    metadata = $metadataPath
    status   = $task.status
    workflow = $task.workflow
} | Format-List
