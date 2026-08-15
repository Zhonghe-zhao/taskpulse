[CmdletBinding()]
param(
    [string]$ComposeFile = "compose.integration.yaml",
    [string]$MemoBridgeUrl = "http://127.0.0.1:8081",
    [string]$TaskPulseUrl = "http://127.0.0.1:8085",
    [int]$TimeoutSeconds = 120,
    [int]$PollIntervalSeconds = 1,
    [int]$StopTimeoutSeconds = 10,
    [string]$EvidenceDirectory = ".\artifacts\evidence"
)

$ErrorActionPreference = "Stop"
$MemoBridgeUrl = $MemoBridgeUrl.TrimEnd('/')
$TaskPulseUrl = $TaskPulseUrl.TrimEnd('/')
$stoppedContainer = $null
$releasedMetric = 'taskpulse_tasks_released_total{workflow="memobridge.semantic_profile"}'

function Assert-True([bool]$Condition, [string]$Message) {
    if (-not $Condition) {
        throw $Message
    }
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

function Get-PrometheusCounter([string]$Metric) {
    $metricsText = (Invoke-WebRequest -UseBasicParsing -Uri "$TaskPulseUrl/metrics").Content
    $line = $metricsText -split "`r?`n" | Where-Object { $_ -like "$Metric *" } | Select-Object -First 1
    if ([string]::IsNullOrWhiteSpace($line)) {
        return [double]0
    }
    return [double](($line -split '\s+')[-1])
}

function ConvertFrom-GoDurationSeconds([string]$Value) {
    $remaining = $Value.Trim()
    if ([string]::IsNullOrWhiteSpace($remaining)) {
        throw "duration is empty"
    }
    $total = [double]0
    while ($remaining.Length -gt 0) {
        $match = [regex]::Match($remaining, '^(?<value>\d+(?:\.\d+)?)(?<unit>ns|us|µs|ms|s|m|h)')
        if (-not $match.Success) {
            throw "invalid Go duration: $Value"
        }
        $factor = switch ($match.Groups['unit'].Value) {
            'ns' { 0.000000001 }
            'us' { 0.000001 }
            'µs' { 0.000001 }
            'ms' { 0.001 }
            's'  { 1 }
            'm'  { 60 }
            'h'  { 3600 }
        }
        $total += [double]$match.Groups['value'].Value * $factor
        $remaining = $remaining.Substring($match.Length)
    }
    return $total
}

function Get-WorkerContainers {
    $containers = @(& docker compose -f $ComposeFile ps -q memobridge-worker)
    if ($LASTEXITCODE -ne 0) {
        throw "could not list memobridge-worker containers"
    }
    return @($containers | Where-Object { -not [string]::IsNullOrWhiteSpace($_) })
}

function Get-ContainerEnvironment([string]$ContainerID) {
    $raw = & docker inspect $ContainerID
    if ($LASTEXITCODE -ne 0) {
        throw "could not inspect container $ContainerID"
    }
    return (($raw | ConvertFrom-Json)[0].Config.Env)
}

function Find-ClaimHolder([string]$TaskID, [string[]]$ContainerIDs) {
    foreach ($containerID in $ContainerIDs) {
        $logs = & docker logs $containerID 2>&1
        if ($LASTEXITCODE -eq 0 -and ($logs -match [regex]::Escape($TaskID))) {
            return $containerID
        }
    }
    return $null
}

function Wait-ForTask([string]$TaskID, [scriptblock]$Condition, [string]$Description) {
    $deadline = (Get-Date).AddSeconds($TimeoutSeconds)
    while ((Get-Date) -lt $deadline) {
        $task = Invoke-JsonRequest -Method Get -Uri "$TaskPulseUrl/tasks/$TaskID" -Body $null
        if (& $Condition $task) {
            return $task
        }
        Start-Sleep -Seconds $PollIntervalSeconds
    }
    throw "timed out waiting for $Description on task $TaskID"
}

try {
	$releasedBefore = Get-PrometheusCounter $releasedMetric
    $workers = Get-WorkerContainers
    Assert-True ($workers.Count -ge 2) "start at least two memobridge-worker replicas before this test"
    foreach ($worker in $workers) {
        $environment = Get-ContainerEnvironment $worker
        $delay = @($environment | Where-Object { $_ -like "MEMOBRIDGE_TASKPULSE_EXECUTION_DELAY=*" } | Select-Object -First 1)
        Assert-True ($delay.Count -eq 1 -and $delay[0] -notin @("MEMOBRIDGE_TASKPULSE_EXECUTION_DELAY=", "MEMOBRIDGE_TASKPULSE_EXECUTION_DELAY=0s")) `
            "memobridge-worker $worker has no execution delay; set MEMOBRIDGE_TASKPULSE_EXECUTION_DELAY above the shutdown window before starting the test"
        $delaySeconds = ConvertFrom-GoDurationSeconds ($delay[0].Substring("MEMOBRIDGE_TASKPULSE_EXECUTION_DELAY=".Length))
        Assert-True ($delaySeconds -gt $StopTimeoutSeconds) `
            "memobridge-worker $worker execution delay must exceed StopTimeoutSeconds=$StopTimeoutSeconds, got $delaySeconds seconds"
    }

    $suffix = [DateTimeOffset]::UtcNow.ToUnixTimeMilliseconds()
    $subject = Invoke-JsonRequest -Method Post -Uri "$MemoBridgeUrl/subjects" -Body @{
        title = "TaskPulse graceful handoff $suffix"
        description = "Disposable evidence for graceful Worker lease release."
        status = "open"
        source_type = "project"
        goal = "Verify TaskPulse graceful handoff."
        labels = "taskpulse,integration,graceful-handoff"
    }
    $source = Invoke-JsonRequest -Method Post -Uri "$MemoBridgeUrl/subjects/$($subject.id)/source-items" -Body @{
        role = "material"
        type = "note"
        title = "TaskPulse graceful handoff source $suffix"
        content = "This disposable source verifies TaskPulse graceful Worker handoff through MemoBridge."
        source = "taskpulse-graceful-handoff-smoke"
        labels = "taskpulse,semantic-profile"
    }
    $created = Invoke-JsonRequest -Method Post -Uri "$MemoBridgeUrl/source-items/$($source.id)/semantic-profile/tasks" -Body @{
        requested_by = "taskpulse_graceful_handoff_smoke"
        batch_id = "graceful-handoff-$suffix"
    }
    $taskID = [string]$created.task_id
    Assert-True (-not [string]::IsNullOrWhiteSpace($taskID)) "MemoBridge did not return a TaskPulse task_id"

    $null = Wait-ForTask $taskID { param($task) $task.status -eq "running" } "initial task claim"
    $deadline = (Get-Date).AddSeconds($TimeoutSeconds)
    $claimHolder = $null
    while ((Get-Date) -lt $deadline) {
        $claimHolder = Find-ClaimHolder $taskID $workers
        if ($null -ne $claimHolder) {
            break
        }
        Start-Sleep -Seconds $PollIntervalSeconds
    }
    Assert-True ($null -ne $claimHolder) "could not find the Worker container that claimed task $taskID"

    & docker stop --time $StopTimeoutSeconds $claimHolder | Out-Null
    if ($LASTEXITCODE -ne 0) {
        throw "could not gracefully stop claimed Worker $claimHolder"
    }
    $stoppedContainer = $claimHolder

    $completed = Wait-ForTask $taskID { param($task) $task.status -in @("succeeded", "failed", "canceled") } "terminal task state after graceful handoff"
    Assert-True ($completed.status -eq "succeeded") ("task did not succeed after handoff: status={0}, error={1}" -f $completed.status, $completed.error_message)
    Assert-True ([int]$completed.retry_count -eq 0) ("graceful handoff consumed retry budget: retry_count={0}" -f $completed.retry_count)

    $events = @(Invoke-JsonRequest -Method Get -Uri "$TaskPulseUrl/tasks/$taskID/events" -Body $null)
    $eventTypes = @($events | ForEach-Object { $_.type })
    $releasedIndex = [array]::IndexOf($eventTypes, "task_released")
    $secondStartedIndex = -1
    if ($releasedIndex -ge 0) {
        $secondStartedIndex = [array]::IndexOf($eventTypes, "task_started", $releasedIndex + 1)
    }
    Assert-True ($releasedIndex -ge 0) "missing task_released event"
    Assert-True ($secondStartedIndex -gt $releasedIndex) "task was not claimed again after task_released"
    Assert-True (-not ($eventTypes -contains "task_recovered")) "graceful handoff unexpectedly used lease-expiration recovery"
	$releasedAfter = Get-PrometheusCounter $releasedMetric
	Assert-True (($releasedAfter - $releasedBefore) -ge 1) "Prometheus did not record the graceful task release"

    $replacementWorkers = @($workers | Where-Object { $_ -ne $claimHolder })
    $replacementHolder = Find-ClaimHolder $taskID $replacementWorkers
    Assert-True ($null -ne $replacementHolder) "no surviving Worker logged the replacement claim"

    & (Join-Path $PSScriptRoot "capture-task-evidence.ps1") `
        -TaskID $taskID `
        -BaseURL $TaskPulseUrl `
        -Label "memobridge-graceful-handoff" `
        -OutputDirectory $EvidenceDirectory

    [pscustomobject]@{
        task_id = $taskID
        source_item_id = $source.id
        initial_claim_worker = $claimHolder
        replacement_claim_worker = $replacementHolder
        task_status = $completed.status
        retry_count = $completed.retry_count
        event_types = $eventTypes
        released_metric_delta = $releasedAfter - $releasedBefore
        graceful_handoff = $true
    } | ConvertTo-Json -Depth 10
}
finally {
    if ($null -ne $stoppedContainer) {
        & docker start $stoppedContainer 2>$null | Out-Null
        if ($LASTEXITCODE -ne 0) {
            Write-Warning "could not restart stopped Worker $stoppedContainer; run docker compose -f $ComposeFile up -d --scale memobridge-worker=2"
        }
    }
}
