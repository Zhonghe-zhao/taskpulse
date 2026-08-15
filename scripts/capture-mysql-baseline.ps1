[CmdletBinding()]
param(
    [string]$Container = "taskpulse-mysql-1",
    [string]$User = "root",
    [string]$Password = "taskpulse_root_dev",
    [string]$Database = "taskpulse",
    [string]$Workflow = "llm_analysis",
    [string]$Label = "snapshot",
    [string]$OutputDirectory = ".\artifacts\mysql"
)

$ErrorActionPreference = "Stop"
$timestamp = [DateTimeOffset]::UtcNow.ToString("yyyyMMddTHHmmssZ")
New-Item -ItemType Directory -Force -Path $OutputDirectory | Out-Null
$outputPath = Join-Path $OutputDirectory ("{0}-{1}.txt" -f $timestamp, $Label)

function Invoke-MySQLQuery([string]$Query, [switch]$AllowFailure) {
    $result = & docker exec -e "MYSQL_PWD=$Password" $Container mysql "-u$User" "--database=$Database" --batch --raw -e $Query 2>&1
    if ($LASTEXITCODE -ne 0 -and -not $AllowFailure) {
        if ($result -match "Access denied") {
            throw "MySQL authentication failed for user '$User'. The script default is root/taskpulse_root_dev; inspect the running container's MYSQL_ROOT_PASSWORD and pass it with -Password. Existing MySQL volumes keep the password from their first initialization."
        }
        throw "MySQL query failed: $result"
    }
    return $result
}

function ConvertTo-SqlStringLiteral([string]$Value) {
    return "'" + $Value.Replace("'", "''") + "'"
}

function Add-OutputLines {
    param(
        [System.Collections.Generic.List[string]]$Lines,
        [object[]]$Values
    )

    foreach ($value in $Values) {
        if ($null -ne $value) {
            [void]$Lines.Add([string]$value)
        }
    }
}

$lines = [System.Collections.Generic.List[string]]::new()
$lines.Add("timestamp_utc=$timestamp")
$lines.Add("label=$Label")
$lines.Add("container=$Container")
$lines.Add("database=$Database")
$lines.Add("workflow=$Workflow")
$lines.Add("")
$lines.Add("[docker_stats]")
$dockerStats = & docker stats $Container --no-stream --format '{{json .}}' 2>&1
if ($LASTEXITCODE -ne 0) {
    throw "docker stats failed for ${Container}: $dockerStats"
}
$lines.Add(($dockerStats | Out-String).TrimEnd())

$lines.Add("")
$lines.Add("[mysql_global_status]")
Add-OutputLines -Lines $lines -Values @(Invoke-MySQLQuery @'
SHOW GLOBAL STATUS WHERE Variable_name IN (
  'Connections', 'Threads_connected', 'Threads_running', 'Questions',
  'Slow_queries', 'Innodb_row_lock_waits', 'Innodb_row_lock_time',
  'Innodb_buffer_pool_reads', 'Innodb_buffer_pool_read_requests'
);
'@)

$lines.Add("")
$lines.Add("[mysql_lock_waits]")
Add-OutputLines -Lines $lines -Values @(Invoke-MySQLQuery @'
SELECT EVENT_NAME, COUNT_STAR, ROUND(SUM_TIMER_WAIT / 1000000000000, 6) AS wait_seconds
FROM performance_schema.events_waits_summary_global_by_event_name
WHERE EVENT_NAME LIKE 'wait/lock/%'
ORDER BY SUM_TIMER_WAIT DESC
LIMIT 20;
'@ -AllowFailure)

$workflowLiteral = ConvertTo-SqlStringLiteral $Workflow
$lines.Add("")
$lines.Add("[tasks_indexes]")
Add-OutputLines -Lines $lines -Values @(Invoke-MySQLQuery "SHOW INDEX FROM tasks;" -AllowFailure)

$lines.Add("")
$lines.Add("[workflow_claim_explain]")
Add-OutputLines -Lines $lines -Values @(Invoke-MySQLQuery @"
EXPLAIN FORMAT=JSON
SELECT id
FROM tasks
WHERE status IN ('queued', 'retrying')
  AND workflow = $workflowLiteral
  AND available_at <= UTC_TIMESTAMP(6)
ORDER BY available_at, created_at, id
LIMIT 1
FOR UPDATE SKIP LOCKED;
"@ -AllowFailure)

$lines.Add("")
$lines.Add("[workflow_expired_lease_explain]")
Add-OutputLines -Lines $lines -Values @(Invoke-MySQLQuery @"
EXPLAIN FORMAT=JSON
SELECT id
FROM tasks
WHERE status = 'running'
  AND workflow = $workflowLiteral
  AND lease_expires_at <= UTC_TIMESTAMP(6)
  AND retry_count < max_retries
ORDER BY lease_expires_at, created_at, id
LIMIT 1
FOR UPDATE SKIP LOCKED;
"@ -AllowFailure)

$lines.Add("")
$lines.Add("[mysql_top_table_io]")
Add-OutputLines -Lines $lines -Values @(Invoke-MySQLQuery @'
SELECT OBJECT_SCHEMA, OBJECT_NAME, COUNT_READ, COUNT_WRITE,
       ROUND(SUM_TIMER_WAIT / 1000000000000, 6) AS wait_seconds
FROM performance_schema.table_io_waits_summary_by_table
WHERE OBJECT_SCHEMA = DATABASE()
ORDER BY SUM_TIMER_WAIT DESC
LIMIT 20;
'@ -AllowFailure)

$lines | Set-Content -Encoding utf8 -Path $outputPath
Write-Output "mysql snapshot=$outputPath"
