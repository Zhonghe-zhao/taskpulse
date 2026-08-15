# TaskPulse 快速开始

本文用于在一台安装了 Docker Desktop 的开发机上启动 TaskPulse，创建任务并观察 Worker 完成任务。默认配置仅适合本地开发。

## 1. 前置条件

- Docker Desktop，并启用 Linux containers；
- PowerShell 7 或 Windows PowerShell 5.1；
- 可选：Go 1.24，用于运行测试和命令行工具。

## 2. 启动本地环境

```powershell
cd E:\CS\TaskPulse
docker compose up -d --build
docker compose ps
```

预期服务：

| 服务 | 宿主机地址 | 作用 |
|---|---|---|
| TaskPulse | `http://127.0.0.1:8085` | API、Dashboard、Metrics |
| MySQL | `127.0.0.1:3306` | 任务和事件事实源 |
| llm-worker | 不暴露端口 | 执行 `llm_analysis` 示例任务 |

打开控制台：

```text
http://127.0.0.1:8085/dashboard
```

健康检查：

```powershell
Invoke-WebRequest -UseBasicParsing http://127.0.0.1:8085/metrics |
  Select-Object StatusCode
```

## 3. 创建第一个任务

```powershell
$headers = @{ "Idempotency-Key" = "quickstart-llm-analysis-001" }
$body = @{
  workflow = "llm_analysis"
  input = @{
    subject = "TaskPulse"
    goal = "验证可靠异步任务链路"
    notes = @("创建", "领取", "执行", "完成")
  }
  max_retries = 3
} | ConvertTo-Json -Depth 10

$task = Invoke-RestMethod `
  -Method Post `
  -Uri http://127.0.0.1:8085/tasks `
  -Headers $headers `
  -ContentType application/json `
  -Body $body

$task | ConvertTo-Json -Depth 10
```

查询任务与事件：

```powershell
Invoke-RestMethod "http://127.0.0.1:8085/tasks/$($task.id)" |
  ConvertTo-Json -Depth 10

Invoke-RestMethod "http://127.0.0.1:8085/tasks/$($task.id)/events" |
  ConvertTo-Json -Depth 10
```

预期事件链：

```text
task_created -> task_started -> task_succeeded
```

重复执行同一个创建请求时，由于 `Idempotency-Key` 和请求内容一致，TaskPulse 返回原任务；如果复用同一个键但改变请求内容，则返回 `409 Conflict`。

## 4. 查看日志

```powershell
docker compose logs -f taskpulse
docker compose logs -f llm-worker
```

停止跟踪日志使用 `Ctrl+C`，不会停止容器。

## 5. 停止环境

保留 MySQL 数据：

```powershell
docker compose down
```

同时删除本地 MySQL 数据卷：

```powershell
docker compose down -v
```

第二条命令会删除任务历史，只在确认不需要数据时使用。

## 6. 下一步

- HTTP 和 Worker 协议：[API_REFERENCE.md](API_REFERENCE.md)
- Kubernetes 与 Compose 部署：[DEPLOYMENT.md](DEPLOYMENT.md)
- 接入自定义业务 Worker：[integrations/taskpulse-sdk-adoption.md](integrations/taskpulse-sdk-adoption.md)
- 真实 MemoBridge 接入：[integrations/memobridge-semantic-profile.md](integrations/memobridge-semantic-profile.md)
- 故障演示与证据：[DEMO_AND_EVIDENCE.md](DEMO_AND_EVIDENCE.md)

