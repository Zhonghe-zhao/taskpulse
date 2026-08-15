[CmdletBinding()]
param(
    [string]$MemoBridgeUrl = "http://127.0.0.1:8081",
    [string]$TaskPulseUrl = "http://127.0.0.1:8085",
    [int]$TimeoutSeconds = 90,
    [int]$PollIntervalSeconds = 1
)

$ErrorActionPreference = "Stop"
$MemoBridgeUrl = $MemoBridgeUrl.TrimEnd('/')
$TaskPulseUrl = $TaskPulseUrl.TrimEnd('/')
$claimedMetric = 'taskpulse_tasks_claimed_total{workflow="memobridge.semantic_profile"}'
$completedMetric = 'taskpulse_tasks_completed_total{workflow="memobridge.semantic_profile",status="succeeded"}'

function Get-PrometheusCounter([string]$Metric) {
    $metricsText = (Invoke-WebRequest -UseBasicParsing -Uri "$TaskPulseUrl/metrics").Content
    $line = $metricsText -split "`r?`n" | Where-Object { $_ -like "$Metric *" } | Select-Object -First 1
    if ([string]::IsNullOrWhiteSpace($line)) {
        return [double]0
    }
    return [double](($line -split '\s+')[-1])
}

function Invoke-JsonRequest {
    param(
        [string]$Method,
        [string]$Uri,
        [object]$Body
    )
    $params = @{ Method = $Method; Uri = $Uri }
    if ($null -ne $Body) {
        $params.ContentType = "application/json"
        $params.Body = $Body | ConvertTo-Json -Depth 20
    }
    Invoke-RestMethod @params
}

function Assert-True([bool]$Condition, [string]$Message) {
    if (-not $Condition) {
        throw $Message
    }
}

$claimedBefore = Get-PrometheusCounter $claimedMetric
$completedBefore = Get-PrometheusCounter $completedMetric

$suffix = [DateTimeOffset]::UtcNow.ToUnixTimeMilliseconds()
$subject = Invoke-JsonRequest -Method Post -Uri "$MemoBridgeUrl/subjects" -Body @{
    title = "TaskPulse integration smoke $suffix"
    description = "Disposable SemanticProfile integration evidence."
    status = "open"
    source_type = "project"
    goal = "Verify reliable async SemanticProfile execution."
    labels = "taskpulse,integration,smoke"
}

$source = Invoke-JsonRequest -Method Post -Uri "$MemoBridgeUrl/subjects/$($subject.id)/source-items" -Body @{
    role = "material"
    type = "note"
    title = "TaskPulse integration source $suffix"
    content = "This disposable source verifies MemoBridge SemanticProfile execution through TaskPulse."
    source = "taskpulse-integration-smoke"
    labels = "taskpulse,semantic-profile"
}

$taskRequest = @{ requested_by = "taskpulse_integration_smoke"; batch_id = "smoke-$suffix" }
$created = Invoke-JsonRequest -Method Post -Uri "$MemoBridgeUrl/source-items/$($source.id)/semantic-profile/tasks" -Body $taskRequest
$duplicate = Invoke-JsonRequest -Method Post -Uri "$MemoBridgeUrl/source-items/$($source.id)/semantic-profile/tasks" -Body $taskRequest
Assert-True ($created.task_id -eq $duplicate.task_id) "duplicate request created another TaskPulse task"

$deadline = (Get-Date).AddSeconds($TimeoutSeconds)
$task = $null
while ((Get-Date) -lt $deadline) {
    $task = Invoke-JsonRequest -Method Get -Uri "$TaskPulseUrl/tasks/$($created.task_id)" -Body $null
    if ($task.status -in @("succeeded", "failed", "canceled")) {
        break
    }
    Start-Sleep -Seconds $PollIntervalSeconds
}

if ($null -eq $task) {
    throw "TaskPulse task was never readable: $($created.task_id)"
}
Assert-True ($task.status -eq "succeeded") ("task did not succeed: status={0}, error={1}" -f $task.status, $task.error_message)
Assert-True ([int64]$task.result.source_item_id -eq [int64]$source.id) "TaskPulse result_ref has the wrong source_item_id"
Assert-True ($task.result.content_hash -eq $created.content_hash) "TaskPulse result_ref has the wrong content_hash"
foreach ($forbiddenField in @("content", "prompt", "response", "summary")) {
    Assert-True (-not ($task.result.PSObject.Properties.Name -contains $forbiddenField)) "TaskPulse result_ref leaked $forbiddenField"
}

$events = Invoke-JsonRequest -Method Get -Uri "$TaskPulseUrl/tasks/$($created.task_id)/events" -Body $null
$eventTypes = @($events | ForEach-Object { $_.type })
foreach ($expectedType in @("task_created", "task_started", "task_progress", "task_succeeded")) {
    Assert-True ($eventTypes -contains $expectedType) "missing TaskPulse event: $expectedType"
}

$profile = Invoke-JsonRequest -Method Get -Uri "$MemoBridgeUrl/source-items/$($source.id)/semantic-profile" -Body $null
Assert-True ($null -ne $profile) "MemoBridge did not persist a SemanticProfile"

$claimedAfter = Get-PrometheusCounter $claimedMetric
$completedAfter = Get-PrometheusCounter $completedMetric
Assert-True (($claimedAfter - $claimedBefore) -ge 1) "Prometheus did not record the external Worker claim"
Assert-True (($completedAfter - $completedBefore) -ge 1) "Prometheus did not record the external Worker completion"

[pscustomobject]@{
    subject_id = $subject.id
    source_item_id = $source.id
    task_id = $created.task_id
    workflow = $created.workflow
    idempotency_key = $created.idempotency_key
    task_status = $task.status
    event_types = $eventTypes
    semantic_profile_written = $true
    claimed_metric_delta = $claimedAfter - $claimedBefore
    completed_metric_delta = $completedAfter - $completedBefore
} | ConvertTo-Json -Depth 10
