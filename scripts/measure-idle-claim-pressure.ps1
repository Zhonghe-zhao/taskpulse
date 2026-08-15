[CmdletBinding()]
param(
    [string]$BaseURL = "http://127.0.0.1:8085",
    [string]$Workflow = "llm_analysis",
    [int]$DurationSeconds = 60,
    [int]$WorkerCount = 0,
    [string]$Label = "idle-claim-pressure",
    [string]$OutputDirectory = ".\artifacts\benchmarks"
)

$ErrorActionPreference = "Stop"
$BaseURL = $BaseURL.TrimEnd('/')
if ($DurationSeconds -le 0) {
    throw "DurationSeconds must be positive"
}
if ($WorkerCount -lt 0) {
    throw "WorkerCount cannot be negative"
}

function Get-MetricValue([string]$MetricsText, [string]$Metric) {
    $line = $MetricsText -split "`r?`n" | Where-Object { $_ -like "$Metric *" } | Select-Object -First 1
    if ([string]::IsNullOrWhiteSpace($line)) {
        return [double]0
    }
    return [double](($line -split '\s+')[-1])
}

function Get-Snapshot {
    $metricsText = (Invoke-WebRequest -UseBasicParsing -Uri "$BaseURL/metrics").Content
    $active = [int64](
        (Get-MetricValue $metricsText 'taskpulse_tasks_current{status="queued"}') +
        (Get-MetricValue $metricsText 'taskpulse_tasks_current{status="retrying"}') +
        (Get-MetricValue $metricsText 'taskpulse_tasks_current{status="running"}')
    )
    return [pscustomobject]@{
        captured_at_utc = [DateTimeOffset]::UtcNow.ToString("o")
        active_tasks = $active
        claim_attempts = Get-MetricValue $metricsText "taskpulse_claim_attempts_total{workflow=`"$Workflow`"}"
        claim_misses = Get-MetricValue $metricsText "taskpulse_claim_misses_total{workflow=`"$Workflow`"}"
        tasks_claimed = Get-MetricValue $metricsText "taskpulse_tasks_claimed_total{workflow=`"$Workflow`"}"
    }
}

$before = Get-Snapshot
if ($before.active_tasks -ne 0) {
    throw "idle measurement requires no queued, retrying, or running tasks; active_tasks=$($before.active_tasks)"
}

Start-Sleep -Seconds $DurationSeconds

$after = Get-Snapshot
if ($after.active_tasks -ne 0) {
    throw "tasks appeared during the idle measurement; active_tasks=$($after.active_tasks)"
}

$attempts = $after.claim_attempts - $before.claim_attempts
$misses = $after.claim_misses - $before.claim_misses
$claimed = $after.tasks_claimed - $before.tasks_claimed
if ($attempts -lt 0 -or $misses -lt 0 -or $claimed -lt 0) {
    throw "Prometheus counters decreased during the measurement; TaskPulse likely restarted"
}

$result = [ordered]@{
    generated_at_utc = [DateTimeOffset]::UtcNow.ToString("o")
    base_url = $BaseURL
    workflow = $Workflow
    duration_seconds = $DurationSeconds
    worker_count = $WorkerCount
    claim_attempts = $attempts
    claim_misses = $misses
    tasks_claimed = $claimed
    claim_attempts_per_second = $attempts / $DurationSeconds
    claim_misses_per_second = $misses / $DurationSeconds
    empty_claim_ratio = if ($attempts -gt 0) { $misses / $attempts } else { $null }
}

New-Item -ItemType Directory -Force -Path $OutputDirectory | Out-Null
$timestamp = [DateTimeOffset]::UtcNow.ToString("yyyyMMddTHHmmssZ")
$safeLabel = ($Label -replace "[^a-zA-Z0-9._-]", "-").Trim('-')
if ([string]::IsNullOrWhiteSpace($safeLabel)) {
    $safeLabel = "idle-claim-pressure"
}
$outputPath = Join-Path $OutputDirectory ("{0}-{1}.json" -f $timestamp, $safeLabel)
$result | ConvertTo-Json -Depth 10 | Set-Content -Encoding utf8 -Path $outputPath

[pscustomobject]@{
    output = $outputPath
    claim_attempts = $attempts
    claim_misses = $misses
    tasks_claimed = $claimed
    claim_attempts_per_second = [math]::Round($result.claim_attempts_per_second, 3)
    empty_claim_ratio = $result.empty_claim_ratio
} | ConvertTo-Json -Depth 10
