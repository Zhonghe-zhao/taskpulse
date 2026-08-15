# TaskPulse

TaskPulse 是一个使用 Go 构建的**可靠异步任务执行运行时**，面向 LLM 调用、内容处理、批量采集等“执行时间长、依赖外部服务、可能失败”的任务。

它不是消息队列的替代品，也不是 Agent 编排平台。TaskPulse 解决的是任务从创建到终态之间的可靠生命周期：持久化、领取、租约、重试、恢复、进度、幂等协议和可观测性。

## 为什么需要它

同步 HTTP 请求不适合直接承载耗时且不稳定的外部工作：

```text
用户发起 LLM 分析
→ 模型调用可能耗时、限流或超时
→ Worker 进程可能崩溃
→ 用户仍需要查看状态，且成功结果不能被重复覆盖
```

TaskPulse 将“可靠执行”从业务代码中抽出。上层业务保留领域数据和业务决策；TaskPulse 只保存任务引用并管理执行生命周期。

## 第一个真实负载：MemoBridge SemanticProfile

MemoBridge 为每个 SourceItem 创建一个独立的 `memobridge.semantic_profile` 任务：

```text
MemoBridge API
  │ 创建任务：source_item_id + content_hash + prompt_version
  ▼
TaskPulse API + MySQL
  │ 持久化、幂等、Claim、Lease、Retry、Event
  ▼
MemoBridge Worker（使用 TaskPulse Worker Runtime）
  │ 读取资料、重新校验 hash、调用 LLM、幂等 Upsert SemanticProfile
  ▼
MemoBridge PostgreSQL
  │
  └── Complete(result_ref)
```

边界明确：

- TaskPulse **不访问** MemoBridge 数据库；
- TaskPulse 不保存资料正文、完整 Prompt 或完整 LLM 输出；
- MemoBridge 不实现自己的队列、租约、心跳或重试；
- MemoBridge 使用 `source_item_id + content_hash + prompt_version` 保证业务写回幂等。

## 当前能力

- MySQL 持久化 `Task` 和 `TaskEvent`；
- `workflow + idempotency_key` 联合幂等创建；
- MySQL `FOR UPDATE SKIP LOCKED` 并发 Claim；
- Lease、Heartbeat、lease token 和 version fencing；
- 指数退避重试、重试预算与 Lease 过期恢复；
- 取消、进度上报、事件审计、幂等 Complete/Fail；
- 单次执行时限，以及 SIGTERM 下主动 Release Lease 的优雅交接；
- 可复用 Go Client：`pkg/taskpulse`；
- 可复用 Worker Runtime：`pkg/taskpulseworker`；
- Prometheus `/metrics` 与结构化日志；
- 内置中英文运维控制台，支持任务状态、Workflow 筛选、游标分页和事件诊断；
- Docker Compose、Kubernetes 清单与故障/基准实验手册。

## 从这里开始

| 目标 | 文档 |
|---|---|
| 10 分钟启动并创建第一个任务 | [快速开始](docs/GETTING_STARTED.md) |
| 查看真实 HTTP 与 Worker 契约 | [API 与 Worker 协议](docs/API_REFERENCE.md) |
| 使用 Docker Compose 或 Kubernetes | [部署手册](docs/DEPLOYMENT.md) |
| 接入自己的 Go Worker | [Worker SDK 接入](docs/integrations/taskpulse-sdk-adoption.md) |
| 复现演示与故障证据 | [演示与证据指南](docs/DEMO_AND_EVIDENCE.md) |

最短体验路径：

```powershell
docker compose up -d --build
Start-Process http://127.0.0.1:8085/dashboard
```

然后按照[快速开始](docs/GETTING_STARTED.md)创建 `llm_analysis` 示例任务。

## 一致性语义

TaskPulse 追求的是：

```text
持久任务状态
+ 单个有效 Lease
+ 至少一次执行尝试
+ 业务侧幂等写回
```

它不声称外部 LLM 调用“绝对只发生一次”。例如 SemanticProfile 已写入、但 Worker 在 `Complete` 响应返回前崩溃时，TaskPulse 可能重新派发任务；MemoBridge 的幂等 Upsert 保证不会产生错误覆盖。这是外部副作用场景中可证明且诚实的边界。

## 本地运行

### TaskPulse + MySQL + 示例 Worker

```powershell
docker compose up -d
docker compose ps
```

Compose 会为 TaskPulse 和示例 Worker 注入同一个开发用 `TASKPULSE_WORKER_AUTH_TOKEN`。部署到共享环境前必须通过环境变量覆盖默认值；直接启动 `cmd/taskpulse` 时也必须设置该变量。只有显式设置 `TASKPULSE_INSECURE_ALLOW_UNAUTHENTICATED_WORKERS=true` 的隔离本地环境才允许不鉴权。

默认宿主机地址：

```text
TaskPulse: http://127.0.0.1:8085
MySQL:     127.0.0.1:3306
```

Compose 默认只绑定宿主机回环地址。Dashboard、List/Get/Create/Cancel、`/task-stats` 和 `/metrics` 当前没有控制面鉴权；局域网暴露前必须先完成权限与审计设计，详见 [控制面安全边界](docs/CONTROL_PLANE_SECURITY.md)。

示例 Worker 使用 fake LLM，不会调用真实模型。可用下面命令查看指标：

```powershell
Invoke-RestMethod http://127.0.0.1:8085/metrics
```

运维控制台：

```text
http://127.0.0.1:8085/dashboard
```

任务列表 API：`GET /tasks?status=failed&workflow=llm_analysis&limit=25`。
响应使用不透明的 `next_cursor` 继续翻页，列表只返回任务摘要；完整输入和结果仅通过任务详情接口按 ID 查询。

### MemoBridge 跨项目联调

`E:\CS\TaskPulse` 与 `E:\CS\memobridge` 需为同级目录：

```powershell
docker compose -f compose.integration.yaml build
docker compose -f compose.integration.yaml up -d
.\scripts\smoke-memobridge-integration.ps1
```

详细步骤、端口说明和 Worker 崩溃恢复实验见 [Compose 联调手册](docs/integrations/docker-compose-integration-runbook.md)。

## 验证与基准

代码验证：

```powershell
go test ./...
.\scripts\verify-taskpulse.ps1
```

最终运行时验收分为四组：

1. Compose 下的 MemoBridge 真实任务闭环；
2. Worker 容器的优雅停机与主动 Lease 交接；
3. Worker 容器/POD 崩溃后的 Lease 恢复；
4. MySQL 轮询调度基准与 MySQL 快照。

完整通过条件见 [最终验收清单](docs/ACCEPTANCE_CHECKLIST.md)。基准命令与 Redis 判定方法见 [MySQL 调度基准手册](docs/experiments/15-dispatch-benchmark-runbook.md)。

## Redis 决策

当前**没有引入 Redis**。这是有意的工程决策：先测量 MySQL Claim 路径的 p95/p99 队列等待、空 Claim 比例、连接/锁等待和吞吐。

只有数据证明 MySQL 轮询或任务发现是瓶颈时，才会使用 MySQL Outbox + Redis Streams 作为 `task_id` 通知层；MySQL 仍然是任务状态、Lease 和恢复的事实源。若瓶颈是 LLM Provider 或业务执行时间，则 Redis 不能解决问题，也不会被加入。

## 文档入口

- [快速开始](docs/GETTING_STARTED.md)
- [API 与 Worker 协议](docs/API_REFERENCE.md)
- [部署手册](docs/DEPLOYMENT.md)
- [演示与证据指南](docs/DEMO_AND_EVIDENCE.md)
- [项目最终蓝图](docs/PROJECT_BLUEPRINT.md)
- [当前状态与待验证证据](docs/PROJECT_STATUS.md)
- [系统架构](docs/ARCHITECTURE.md)
- [MemoBridge 接入协议](docs/integrations/memobridge-semantic-profile.md)
- [MemoBridge 接入待办](docs/integrations/memobridge-known-issues.md)
- [控制面安全边界](docs/CONTROL_PLANE_SECURITY.md)
- [最终验收清单](docs/ACCEPTANCE_CHECKLIST.md)
- [实验与压测文档](docs/experiments/README.md)
- [面试讲解提纲](docs/INTERVIEW_GUIDE.md)
