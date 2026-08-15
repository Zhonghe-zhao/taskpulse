[CmdletBinding()]
param(
    [string]$BaseUrl = "http://127.0.0.1:8085",
    [string]$Workflow = "taskpulse.protocol_smoke",
    [string]$WorkerId = "smoke-worker-1",
    [long]$SourceItemId = 11778,
    [string]$ContentHash = "sha256:smoke",
    [string]$PromptVersion = "source_semantic_profile:v1",
    [string]$IdempotencyKey = "",
    [string]$WorkerAuthToken = ""
)

$ErrorActionPreference = "Stop"
Add-Type -AssemblyName System.Net.Http

$BaseUrl = $BaseUrl.TrimEnd('/')
if ([string]::IsNullOrWhiteSpace($IdempotencyKey)) {
    $IdempotencyKey = "smoke-semantic-profile-" + [DateTimeOffset]::UtcNow.ToUnixTimeMilliseconds()
}
if ([string]::IsNullOrWhiteSpace($WorkerAuthToken)) {
    $WorkerAuthToken = $env:TASKPULSE_WORKER_AUTH_TOKEN
}
if ([string]::IsNullOrWhiteSpace($WorkerAuthToken)) {
    $WorkerAuthToken = "taskpulse_dev_worker_token"
}

function Assert-Equal([object]$Expected, [object]$Actual, [string]$Message) {
    if ($Expected -ne $Actual) {
        throw "$Message. expected=[$Expected], actual=[$Actual]"
    }
}

function Invoke-JsonRequest {
    param(
        [string]$Method,
        [string]$Uri,
        [object]$Body,
        [hashtable]$Headers = @{},
        [switch]$WorkerAuth
    )

    $client = New-Object System.Net.Http.HttpClient
    $client.Timeout = [TimeSpan]::FromSeconds(30)
    try {
        $request = New-Object System.Net.Http.HttpRequestMessage
        $request.Method = New-Object System.Net.Http.HttpMethod($Method.ToUpperInvariant())
        $request.RequestUri = [Uri]$Uri

        if ($null -ne $Headers) {
            foreach ($entry in $Headers.GetEnumerator()) {
                [void]$request.Headers.TryAddWithoutValidation([string]$entry.Key, [string]$entry.Value)
            }
        }
        if ($WorkerAuth -and -not [string]::IsNullOrWhiteSpace($WorkerAuthToken)) {
            [void]$request.Headers.TryAddWithoutValidation("Authorization", "Bearer $WorkerAuthToken")
        }

        if ($null -ne $Body) {
            $json = $Body | ConvertTo-Json -Depth 20
            $request.Content = New-Object System.Net.Http.StringContent(
                $json,
                [System.Text.Encoding]::UTF8,
                "application/json"
            )
        }

        $response = $client.SendAsync($request).GetAwaiter().GetResult()
        $text = $response.Content.ReadAsStringAsync().GetAwaiter().GetResult()
        if (-not $response.IsSuccessStatusCode) {
            $code = [int]$response.StatusCode
            throw "HTTP $code from $Method $Uri : $text"
        }
        if ([string]::IsNullOrWhiteSpace($text)) {
            return $null
        }
        return $text | ConvertFrom-Json
    } finally {
        $client.Dispose()
    }
}

$taskInput = @{
    source_item_id = $SourceItemId
    content_hash = $ContentHash
    prompt_version = $PromptVersion
    requested_by = "taskpulse_smoke"
    batch_id = "smoke"
}
$createBody = @{
    workflow = $Workflow
    input = $taskInput
    max_retries = 2
}

Write-Host "Worker auth: Bearer configured"
Write-Host "Workflow: $Workflow (isolated from memobridge.semantic_profile queue)"

$created = Invoke-JsonRequest -Method Post -Uri "$BaseUrl/tasks" `
    -Headers @{ "Idempotency-Key" = $IdempotencyKey } -Body $createBody
$duplicate = Invoke-JsonRequest -Method Post -Uri "$BaseUrl/tasks" `
    -Headers @{ "Idempotency-Key" = $IdempotencyKey } -Body $createBody
Assert-Equal $created.id $duplicate.id "idempotency did not return the original task"

$claimBody = @{
    worker_id = $WorkerId
    workflow = $Workflow
    lease_duration = "30s"
}
$claimed = Invoke-JsonRequest -Method Post -Uri "$BaseUrl/worker/tasks/claim" -WorkerAuth -Body $claimBody
Assert-Equal $created.id $claimed.id "claimed a different task (stop other workers first)"
if ([string]::IsNullOrWhiteSpace($claimed.lease_token)) {
    throw "claim response did not contain lease_token"
}

$progressBody = @{
    worker_id = $WorkerId
    lease_token = $claimed.lease_token
    version = $claimed.version
    progress = 50
    message = "smoke progress"
}
$progressed = Invoke-JsonRequest -Method Post `
    -Uri "$BaseUrl/worker/tasks/$($claimed.id)/progress" -WorkerAuth -Body $progressBody
Assert-Equal 50 $progressed.progress "progress was not persisted"

$resultRef = @{
    source_item_id = $SourceItemId
    content_hash = $ContentHash
    prompt_version = $PromptVersion
}
$completeBody = @{
    worker_id = $WorkerId
    lease_token = $progressed.lease_token
    version = $progressed.version
    result_ref = $resultRef
}
$completed = Invoke-JsonRequest -Method Post `
    -Uri "$BaseUrl/worker/tasks/$($claimed.id)/complete" -WorkerAuth -Body $completeBody
$replayed = Invoke-JsonRequest -Method Post `
    -Uri "$BaseUrl/worker/tasks/$($claimed.id)/complete" -WorkerAuth -Body $completeBody
Assert-Equal "succeeded" $completed.status "task did not complete"
Assert-equal "succeeded" $replayed.status "replayed complete changed task status"
Assert-Equal ($completed.id) ($replayed.id) "replayed complete returned another task"

[pscustomobject]@{
    task_id = $created.id
    workflow = $Workflow
    duplicate_task_id = $duplicate.id
    lease_token_present = $true
    progress = $progressed.progress
    completed_status = $completed.status
    replayed_status = $replayed.status
    result_ref = ($replayed.result | ConvertTo-Json -Compress)
} | ConvertTo-Json -Depth 10
