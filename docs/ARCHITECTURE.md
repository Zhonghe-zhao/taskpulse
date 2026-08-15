# TaskPulse 系统架构

- 文档状态：当前架构说明
- 最近更新：2026-08-16
- 相关决策：[ADR-0001：使用 MySQL 8](adr/0001-use-mysql-as-system-of-record.md)、[ADR-0002：原子创建任务与初始事件](adr/0002-atomic-task-and-created-event.md)、[ADR-0003：原子提交任务终态与终态事件](adr/0003-atomic-terminal-task-transition.md)、[ADR-0004：原子提交任务领取与领取事件](adr/0004-atomic-task-claim-event.md)、[ADR-0005：原子提交过期任务失败与失败事件](adr/0005-atomic-expired-task-failure.md)、[ADR-0006：通用错误分类与重试语义](adr/0006-generic-retry-semantics.md)、[ADR-0007：任务创建幂等](adr/0007-idempotent-task-creation.md)、[ADR-0008：原子取消待执行任务](adr/0008-cancel-pending-tasks.md)、[ADR-0009：静态 LLM Analysis Workflow](adr/0009-static-llm-analysis-workflow.md)

## 架构目标

TaskPulse 当前采用“**模块化控制面 + 可独立部署的业务 Worker**”架构：控制面仍是单个 Go 服务，负责 HTTP API、应用用例和 MySQL 状态；Worker 可以是内置示例 Worker，也可以是使用公共 SDK 的独立进程或容器。

这样既不为了微服务拆分而拆分，也让业务方无需复制 Claim、Lease、Heartbeat、Retry 或状态机代码。业务 Worker 只注册自己理解的 workflow Executor。

## 当前运行架构

```text
业务 API（例如 MemoBridge）
  │
  │ 公共 Go Client：POST /tasks
  ▼
transport/http
  │ 解析 JSON、映射 HTTP 状态码
  ▼
application.TaskService
  │ CreateTaskWithEvent
  ▼
MySQL TaskCreationStore
  │ 同一事务写入 Task 与 task_created
  ▼
MySQL 8（Task/Event 真相源与任务队列）
  ▲
  │ Worker HTTP 协议：Claim / Heartbeat / Progress / Complete / Fail
  │
外部 Worker Runtime（独立进程或容器）
  │
  ▼
业务 Executor Registry
  │ workflow=llm_analysis / memobridge.semantic_profile / memobridge.embedding_index
  ▼
MemoBridge SemanticProfile/Embedding Executor / LLM Analysis Executor
  │ 读取业务数据、调用外部服务、业务幂等写回
  ▼
Result + TaskEvent
```

API 创建任务后立即返回。Worker Runtime 使用 MySQL 事务原子领取任务，并通过租约心跳和版本号处理崩溃恢复与旧 Worker 隔离。Reaper 将重试额度耗尽的过期任务收敛为失败。`llm-worker` 是使用 Runtime 的示例；MemoBridge Worker 使用同一 Runtime 处理 `memobridge.semantic_profile`。真实 LLM Provider 的所有权属于业务 Worker，不属于 TaskPulse。

## 当前模块

| 模块 | 代码位置 | 职责 |
|---|---|---|
| 启动与组装 | `cmd/taskpulse` | 创建 Store、Service、Worker、Executor 和 HTTP Server |
| 公共 Client SDK | `pkg/taskpulse` | 创建、查询、取消和 Worker HTTP 协议的 Go Client |
| 公共 Worker Runtime | `pkg/taskpulseworker` | 轮询、注册 Executor、Heartbeat、进度、完成/失败和优雅退出 |
| Domain | `internal/domain` | Task、TaskEvent、状态机和合法状态流转 |
| Application | `internal/application` | 编排创建任务、查询任务和查询事件用例 |
| Store | `internal/store`、`internal/store/mysqlstore` | 定义存储接口，提供内存测试实现和 MySQL 运行实现 |
| HTTP Transport | `internal/transport/http` | HTTP 路由、请求解析和响应映射 |
| Worker | `internal/worker` | 领取任务、选择 Executor、保存结果和终态事件 |
| URL Check Executor | `internal/executor/urlcheck` | 校验 URL、有界并发请求、汇总成功/部分成功/失败 |
| LLM Analysis Executor | `internal/executor/llmanalysis` | 解析 LLM 分析输入、调用可替换 Client、分类 Provider 错误、生成结构化结果 |
| Observability | `internal/observability` | 暴露 Prometheus 文本格式指标，记录任务执行、重试、租约和 Reaper 行为 |
| Identity | `internal/identity` | 生成进程内唯一的 Task/Event ID |
| Database Platform | `internal/platform/database` | MySQL 配置校验、连接池创建和连通性检查 |

## 依赖方向

```text
transport/http → application → domain
                         └──→ store interfaces

pkg/taskpulseworker → pkg/taskpulse → transport/http
                  └──→ 业务 Executor

worker → domain
      └→ store interfaces

executor/urlcheck → worker.Executor contract
                  └→ domain.Task

executor/llmanalysis → worker.Executor contract
                     └→ domain.Task

store implementations → domain
```

约束：

1. HTTP Handler 不直接操作 Store。
2. Domain 不依赖 HTTP、数据库和具体 Executor。
3. URL 特有规则不能进入 TaskPulse 通用 Domain 或 Store。
4. Memory/MySQL Store 的替换不应要求修改 Handler 和 Executor。
5. Executor 返回执行结果，不直接决定任务如何持久化。
6. LLM Provider 的超时、限流和上游错误由 Executor/Client 分类，Worker 只理解通用执行错误。

## 当前已经具备的能力

- HTTP 创建和查询任务、查询任务事件。
- Task 状态机和终态判断。
- Memory Store 的并发保护和数据深复制。
- MySQL 持久化 Task 与 TaskEvent。
- Task 与 Created Event 的事务原子创建。
- 可选 `Idempotency-Key` 的任务创建幂等、参数冲突检测和并发唯一性。
- queued/retrying 任务的幂等取消，以及取消状态与 canceled Event 的原子提交。
- Worker 终态更新与 succeeded/partial/failed Event 的事务原子提交。
- Worker 领取/恢复任务与 started/recovered Event 的事务原子提交。
- Reaper 失败清理与 failed Event 的事务原子提交。
- `FOR UPDATE SKIP LOCKED` 并发领取。
- Worker 租约、心跳续租、过期接管和版本隔离。
- Memory/MySQL Store 统一按照 `available_at` 控制任务可领取时间。
- 领取路径通过 `ClaimKind` 区分 initial、retry 和 recovery，避免根据重试次数猜测来源。
- 内置 Worker 与外部 Worker Runtime 仅对明确分类的 transient 错误应用 workflow 重试策略，普通错误和永久错误直接失败。
- Worker Runtime 对 TaskPulse 控制面网络错误、HTTP 429 和 5xx 使用独立的有上限指数退避，避免控制面故障时多 Worker 以固定频率同步重试。
- 重试等待、重新领取和生命周期事件均持久化，可跨进程重启继续调度。
- 重试额度耗尽后的 Reaper 失败清理。
- Worker 根据 workflow 选择 Executor。
- URL 检测的有界并发、结果顺序保持和部分成功语义。
- `llm_analysis` 静态 workflow、可替换 LLM Client、Fake Client 和 LLM 错误分类边界。
- Worker/Reaper 结构化日志，记录领取、完成、失败、重试、续租和过期清理。
- `/metrics` Prometheus 文本格式指标，记录任务状态分布、可领取任务积压、最老等待时间、任务领取、完成、重试、租约和执行耗时。
- 公共 Go Client 与 Worker Runtime；外部 Worker 通过 workflow 过滤领取任务，并携带 lease token/version 进行 fencing。
- Compose 和 Kubernetes 清单；Kubernetes 使用 TaskPulse Deployment 与独立 Worker Deployment。
- Context 传递、HTTP 超时和进程信号处理。
- Domain、Store、Application、HTTP、Worker、Executor 单元测试。

## 当前明确不具备的保证

- 人工死信重放尚未完成。
- 实时事件推送尚未完成。
- TaskPulse 自带的 `llm_analysis` 示例仍使用 Fake Client；真实 Provider 由 MemoBridge 等业务 Worker持有，真实 SemanticProfile 与 Embedding 负载已经完成联调。
- SSRF 防护；当前 URL Executor 不能直接暴露到公网。
- 尚未接入 Prometheus Server、Grafana 或 Redis；当前只暴露 Prometheus 格式 `/metrics`。
- Kubernetes 已在 Docker Desktop 本地多节点集群完成多副本和 Worker 故障实验，但不代表生产可用。

这些是后续要通过实现和实验获得的能力，不能在项目介绍中表述为已经完成。

## 当前持久化实现

当前通过 Store 接口同时保留内存测试实现与 MySQL 运行实现：

```text
TaskStore interface           EventStore interface
       │                              │
       ├── MemoryTaskStore            ├── MemoryEventStore
       │   用于单元测试               │   用于单元测试
       │                              │
       └── MySQLTaskStore             └── MySQLEventStore
           用于真实运行                   用于真实运行
```

当前表模型覆盖代码真实使用的 Task 与 TaskEvent：

```text
tasks
- id
- workflow
- status
- input_json / result_json
- progress
- retry_count / max_retries
- error_message
- lease_owner / lease_expires_at
- idempotency_key
- created_at / updated_at / started_at / finished_at

task_events
- id
- task_id
- type
- message
- payload_json
- progress
- created_at
```

只有当 URL 需要独立领取、独立重试和独立查询时，再通过新 ADR 引入 `task_items`，不能因为旧设计文档出现过该表就直接实现。

## 当前关键事务

### 创建任务

```text
BEGIN
→ INSERT tasks
→ INSERT task_events(task_created)
→ COMMIT
```

目标：不能出现“任务存在但创建事件不存在”。

### 领取任务

```text
BEGIN
→ SELECT queued task FOR UPDATE SKIP LOCKED
→ UPDATE status=running, lease_owner, lease_expires_at
→ INSERT task_started event
→ COMMIT
```

当前保证：多个进程竞争时只有一个 Worker 获得该任务，任务领取和 started/recovered
事件在同一事务提交，同时为崩溃恢复留下租约信息。

### 取消任务

```text
BEGIN
→ SELECT task BY id FOR UPDATE
→ 校验状态为 queued/retrying/running
→ UPDATE status=canceled, clear lease, version=version+1
→ INSERT task_canceled event
→ COMMIT
```

当前保证：取消与 Worker 领取或终态提交竞争时只有一方能够完成状态转换；重复取消返回已经取消的任务，
不会重复写入取消事件。运行中的 Worker 会因租约条件失效取消 Executor Context，旧 Worker 不能覆盖 canceled 状态。

### 清理重试耗尽的过期任务

```text
BEGIN
→ SELECT expired running task FOR UPDATE SKIP LOCKED
→ UPDATE status=failed, clear lease, version=version+1
→ INSERT task_failed event
→ COMMIT
```

当前保证：任务失败状态和失败事件同时提交；事件写入失败时，任务仍保持清理前状态，
可由 Reaper 在后续轮询中再次处理。

## 后续演进条件

- Redis Streams：MySQL 队列基线完成，并测得轮询/锁竞争问题或需要独立消费组后再决策。
- Prometheus Server/Grafana：在 `/metrics` 基线稳定后接入，观测积压、等待时间、吞吐、错误和重试。
- Docker Compose：当前已用于 MySQL + TaskPulse + 外部 Worker，以及跨项目 MemoBridge 联调。
- Kubernetes：当前已有 API/Worker Deployment、Service、ConfigMap 与 Secret 清单；仍需保留 Pod Kill 实验的 TaskEvent 证据。

## 文档同步要求

每次架构变化需要同时检查：

1. 本文档的当前运行图和能力清单。
2. `docs/MVP.md` 的里程碑状态。
3. 是否需要新增或替换 ADR。
4. 是否需要在 `docs/experiments` 增加验证证据。
