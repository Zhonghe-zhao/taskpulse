# MemoBridge + TaskPulse Compose 联调手册

以下命令均在 `E:\CS\TaskPulse` 执行。

## 前置条件

- Docker Desktop 的 Linux engine 正在运行。
- `E:\CS\TaskPulse` 与 `E:\CS\memobridge` 是同级目录。
- MemoBridge 工作区已包含公共 SDK 接入代码与两个联调用 Dockerfile。

Compose 通过 `additional_contexts` 把本地 TaskPulse Go Module 暴露给 MemoBridge 镜像构建；两套服务依然使用独立数据库，只通过 HTTP 通信。联调 Postgres 使用 `pgvector/pgvector:pg15`，因为 MemoBridge 迁移会 `CREATE EXTENSION vector`。

## 启动

先进行不构建镜像的静态检查：

```powershell
.\scripts\verify-taskpulse.ps1
```

镜像构建由本机执行：

```powershell
.\scripts\verify-taskpulse.ps1 -BuildIntegration
```

```powershell
docker compose -f compose.integration.yaml build
docker compose -f compose.integration.yaml up -d
docker compose -f compose.integration.yaml ps
```

`compose.integration.yaml` 会将同一个 `TASKPULSE_WORKER_AUTH_TOKEN` 注入 TaskPulse 与 MemoBridge Worker。默认值只用于本地联调；共享环境必须在启动前以环境变量替换为随机密钥。

宿主机端口：

```text
MemoBridge API: http://127.0.0.1:8081
TaskPulse:     http://127.0.0.1:8085
```

在 Compose 网络内部，MemoBridge 使用 `http://taskpulse:8080`，PostgreSQL 服务名为 `postgres`。`8085` 只属于宿主机访问 TaskPulse，`8081` 只属于宿主机访问 MemoBridge；Worker 本身不额外监听端口。

## 验证真实联调

脚本会创建临时 Subject 和 SourceItem，两次提交同一个 SemanticProfile 请求，等待 TaskPulse 终态，并检查 TaskPulse 事件与 MemoBridge 的 SemanticProfile 写回：

```powershell
.\scripts\smoke-memobridge-integration.ps1
```

预期输出：

```text
task_status              : succeeded
semantic_profile_written : True
event_types              : task_created, task_started, task_progress, task_succeeded
claimed_metric_delta     : 1 or more
completed_metric_delta   : 1 or more
```

脚本使用 `compose.integration.yaml` 默认配置的 fake provider，不会消耗真实 LLM 调用额度。

## 验证 Worker 崩溃恢复

将测试专用执行延迟设为大于 Lease，任务被领取后终止 Worker 容器，确认 Lease 过期恢复：

```powershell
$env:MEMOBRIDGE_TASKPULSE_EXECUTION_DELAY = "45s"
$env:TASKPULSE_LEASE_DURATION = "30s"
docker compose -f compose.integration.yaml up -d --build memobridge-worker
```

当 Worker 日志出现 `task claimed` 后，使用 `kill` 模拟突然崩溃，不要使用会优雅退出的 `docker compose stop`：

```powershell
docker compose -f compose.integration.yaml kill memobridge-worker
docker compose -f compose.integration.yaml logs -f memobridge-worker
```

`unless-stopped` 重启策略会创建替代 Worker。Lease 过期后，在 TaskEvent 中检查 `task_recovered`，随后应出现新的 Claim 与 `task_succeeded`。

也可以直接运行自动验收脚本。它会创建临时资料、定位 Claim 容器、执行 `docker kill`、确认容器
重启，并验证 `task_created -> task_started -> task_recovered -> task_succeeded` 和 `retry_count=1`：

```powershell
.\scripts\smoke-memobridge-crash-recovery.ps1
```

该延迟仅发生在 MemoBridge Worker 调用 SemanticProfile Executor 之前。它让故障实验可重复，但不会使 TaskPulse 依赖 MemoBridge 或真实 LLM。

## 验证优雅停机交接

这一组实验与 `kill` 不同：它验证容器收到 `SIGTERM` 后，Worker 主动归还 Lease，而不是等待
Lease 过期。先用至少两个 Worker 和足够长的测试延迟启动环境：

```powershell
$env:MEMOBRIDGE_TASKPULSE_EXECUTION_DELAY = "45s"
docker compose -f compose.integration.yaml up -d --build --scale memobridge-worker=2
```

然后运行自动验收脚本：

```powershell
.\scripts\smoke-memobridge-graceful-handoff.ps1
```

脚本会创建可丢弃资料，定位实际 Claim 该任务的容器，执行正常的 `docker stop`，并断言：

```text
task_created
-> task_started
-> task_released
-> task_started
-> task_succeeded
```

它还会检查 `retry_count=0`、没有 `task_recovered`，并将 Task、TaskEvent 和 Prometheus 指标保存到
`artifacts/evidence`。脚本最终会尝试重启被停止的容器，使本地环境恢复为两个 Worker。

## Worker Runtime 配置

Compose 会把 `TASKPULSE_WORKER_AUTH_TOKEN` 显式传给 MemoBridge Worker，并由公共 Go SDK 用于所有 Worker 协议请求。

还可以按需要设置：

```powershell
$env:TASKPULSE_EXECUTION_TIMEOUT = "90s" # 单次 SemanticProfile 执行上限；默认关闭
$env:TASKPULSE_SHUTDOWN_TIMEOUT = "5s"   # SIGTERM 后归还 Lease 的宽限
```

二者均由 TaskPulse 公共 Worker Runtime 处理。MemoBridge 只保留读取资料、校验版本、调用 LLM 和幂等写回 SemanticProfile 的职责。

## 停止

```powershell
docker compose -f compose.integration.yaml down
```

`down -v` 会删除两个数据库卷，只能用于可丢弃的本地数据。
