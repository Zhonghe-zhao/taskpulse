[CmdletBinding()]
param(
    [string]$ComposeFile = "compose.integration.yaml",
    [string]$MemoBridgeUrl = "http://127.0.0.1:8081",
    [string]$TaskPulseUrl = "http://127.0.0.1:8085",
    [int]$TimeoutSeconds = 150,
    [int]$PollIntervalSeconds = 1,
    [string]$EvidenceDirectory = ".\artifacts\evidence"
)

$ErrorActionPreference = "Stop"
$MemoBridgeUrl = $MemoBridgeUrl.TrimEnd('/')
$TaskPulseUrl = $TaskPulseUrl.TrimEnd('/')

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

function Get-ContainerInspect([string]$ContainerID) {
    $raw = & docker inspect $ContainerID
    if ($LASTEXITCODE -ne 0) {
        throw "could not inspect container $ContainerID"
    }
    return ($raw | ConvertFrom-Json)[0]
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

$workers = Get-WorkerContainers
Assert-True ($workers.Count -ge 1) "start at least one memobridge-worker before this test"
foreach ($worker in $workers) {
    $inspect = Get-ContainerInspect $worker
    $delay = @($inspect.Config.Env | Where-Object { $_ -like "MEMOBRIDGE_TASKPULSE_EXECUTION_DELAY=*" } | Select-Object -First 1)
    Assert-True ($delay.Count -eq 1 -and $delay[0] -notin @("MEMOBRIDGE_TASKPULSE_EXECUTION_DELAY=", "MEMOBRIDGE_TASKPULSE_EXECUTION_DELAY=0s")) `
        "memobridge-worker $worker has no execution delay; set MEMOBRIDGE_TASKPULSE_EXECUTION_DELAY above the lease duration before starting the test"
    $delaySeconds = ConvertFrom-GoDurationSeconds ($delay[0].Substring("MEMOBRIDGE_TASKPULSE_EXECUTION_DELAY=".Length))
    Assert-True ($delaySeconds -gt (2 * $PollIntervalSeconds)) `
        "memobridge-worker $worker execution delay must exceed two poll intervals, got $delaySeconds seconds"
}

$suffix = [DateTimeOffset]::UtcNow.ToUnixTimeMilliseconds()
$subject = Invoke-JsonRequest -Method Post -Uri "$MemoBridgeUrl/subjects" -Body @{
    title = "TaskPulse crash recovery $suffix"
    description = "Disposable evidence for Worker crash recovery."
    status = "open"
    source_type = "project"
    goal = "Verify TaskPulse lease-expiration recovery."
    labels = "taskpulse,integration,crash-recovery"
}
$source = Invoke-JsonRequest -Method Post -Uri "$MemoBridgeUrl/subjects/$($subject.id)/source-items" -Body @{
    role = "material"
    type = "note"
    title = "TaskPulse crash recovery source $suffix"
    content = "This disposable source verifies TaskPulse Worker crash recovery through MemoBridge."
    source = "taskpulse-crash-recovery-smoke"
    labels = "taskpulse,semantic-profile"
}
$created = Invoke-JsonRequest -Method Post -Uri "$MemoBridgeUrl/source-items/$($source.id)/semantic-profile/tasks" -Body @{
    requested_by = "taskpulse_crash_recovery_smoke"
    batch_id = "crash-recovery-$suffix"
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

$restartCountBefore = [int](Get-ContainerInspect $claimHolder).RestartCount
& docker kill $claimHolder | Out-Null
if ($LASTEXITCODE -ne 0) {
    throw "could not kill claimed Worker $claimHolder"
}

$deadline = (Get-Date).AddSeconds($TimeoutSeconds)
while ((Get-Date) -lt $deadline) {
    $inspect = Get-ContainerInspect $claimHolder
    if ($inspect.State.Running -and [int]$inspect.RestartCount -gt $restartCountBefore) {
        break
    }
    Start-Sleep -Seconds $PollIntervalSeconds
}
$restarted = Get-ContainerInspect $claimHolder
Assert-True ($restarted.State.Running -and [int]$restarted.RestartCount -gt $restartCountBefore) `
    "Worker container did not restart after docker kill; check restart policy"

$completed = Wait-ForTask $taskID { param($task) $task.status -in @("succeeded", "failed", "canceled") } "terminal task state after crash recovery"
Assert-True ($completed.status -eq "succeeded") ("task did not succeed after recovery: status={0}, error={1}" -f $completed.status, $completed.error_message)
Assert-True ([int]$completed.retry_count -eq 1) ("recovery should consume one retry budget slot: retry_count={0}" -f $completed.retry_count)

$events = @(Invoke-JsonRequest -Method Get -Uri "$TaskPulseUrl/tasks/$taskID/events" -Body $null)
$eventTypes = @($events | ForEach-Object { $_.type })
$startedIndex = [array]::IndexOf($eventTypes, "task_started")
$recoveredIndex = [array]::IndexOf($eventTypes, "task_recovered")
$succeededIndex = [array]::IndexOf($eventTypes, "task_succeeded")
Assert-True ($startedIndex -ge 0) "missing initial task_started event"
Assert-True ($recoveredIndex -gt $startedIndex) "missing task_recovered event after initial claim"
Assert-True ($succeededIndex -gt $recoveredIndex) "task did not succeed after task_recovered"

& (Join-Path $PSScriptRoot "capture-task-evidence.ps1") `
    -TaskID $taskID `
    -BaseURL $TaskPulseUrl `
    -Label "memobridge-crash-recovery" `
    -OutputDirectory $EvidenceDirectory

[pscustomobject]@{
    task_id = $taskID
    source_item_id = $source.id
    crashed_worker = $claimHolder
    restart_count_before = $restartCountBefore
    restart_count_after = [int]$restarted.RestartCount
    task_status = $completed.status
    retry_count = $completed.retry_count
    event_types = $eventTypes
    crash_recovery = $true
} | ConvertTo-Json -Depth 10
