# MemoBridge SemanticProfile 接入协议

## 目标

MemoBridge 将每个 `SourceItem` 的 SemanticProfile 生成拆成一个独立的 TaskPulse 任务。

```text
TaskPulse：可靠调度、租约、心跳、重试、恢复、取消、事件
MemoBridge：资料读取、Prompt、LLM、JSON 校验、SemanticProfile 写回
```

两个项目不共享业务表，也不互相访问对方数据库。

## 任务模型

每个 SourceItem 对应一个任务：

```json
{
  "workflow": "memobridge.semantic_profile",
  "input": {
    "source_item_id": 11778,
    "content_hash": "sha256:abc123",
    "prompt_version": "source_semantic_profile:v1",
    "requested_by": "manual_batch",
    "batch_id": "batch_001"
  },
  "max_retries": 3
}
```

幂等键：

```text
semantic-profile:{source_item_id}:{content_hash}:{prompt_version}
```

相同资料内容和 Prompt 版本只创建一个有效任务；资料或 Prompt 版本变化后，允许创建新任务。

TaskPulse 只保存引用数据，不理解 SourceItem 或 SemanticProfile 的业务含义。

## 职责边界

TaskPulse 负责：

- 创建和持久化任务；
- Claim、Lease、Heartbeat；
- 重试、退避和崩溃恢复；
- 任务取消；
- 状态、进度和事件；
- 版本冲突保护。

MemoBridge 负责：

- 选择和读取 SourceItem；
- 计算和校验 content_hash；
- 构造 Prompt 并调用 LLM；
- 校验结构化 JSON；
- 幂等写入 SemanticProfile；
- 处理资料变化和批次展示。

TaskPulse 不访问 MemoBridge 数据库，不保存完整 Prompt，不保存完整 LLM 输出，也不写入 SemanticProfile。

## 接入流程

```text
用户选择多个 SourceItem
    ↓
MemoBridge 为每个 SourceItem 创建一个 TaskPulse Task
    ↓
MemoBridge 记录 batch_id 与 task_id 的关系
    ↓
MemoBridge Worker 领取任务
    ↓
Worker 读取 SourceItem 并再次校验 content_hash
    ↓
Worker 调用 LLM、校验结果、幂等写回 SemanticProfile
    ↓
Worker 调用 TaskPulse complete
```

MemoBridge Worker 是独立进程或模块，不需要自己实现任务队列。

## MemoBridge 需要实现的内容

Worker 调用 TaskPulse 的外部协议：

```http
POST /worker/tasks/claim
POST /worker/tasks/{task_id}/heartbeat
POST /worker/tasks/{task_id}/progress
POST /worker/tasks/{task_id}/complete
POST /worker/tasks/{task_id}/fail
```

目标 Claim 请求：

```json
{
  "worker_id": "memobridge-worker-1",
  "workflow": "memobridge.semantic_profile",
  "lease_duration": "30s"
}
```

按 workflow 过滤领取是 TaskPulse 接入前需要补齐的通用能力，避免 `llm-worker` 领取 MemoBridge 任务。

Worker 领取后必须：

1. 根据 source_item_id 查询 MemoBridge 数据库；
2. 资料不存在时报告 `source_not_found`；
3. 重新计算 content_hash；
4. hash 不一致时报告 `source_changed`；
5. 检查 prompt_version 是否有效。

后续所有 `heartbeat`、`progress`、`complete`、`fail` 请求都必须回传 Claim 或上一次响应中的 `lease_token`。TaskPulse 会同时校验 token 内的任务 ID、Worker ID 和 version；缺失、错误或已经过期的 token 都会被拒绝，旧 Worker 不能覆盖新 Worker 的状态。

## 幂等写回

SemanticProfile 的业务幂等键为：

```text
source_item_id + content_hash + prompt_version
```

如果出现下面的情况，MemoBridge 也不能产生重复结果：

```text
SemanticProfile 已写入
但 Worker 在调用 TaskPulse complete 前崩溃
```

TaskPulse 可能重新派发任务，因此 MemoBridge 必须使用幂等 Upsert。

## 批次处理

第一版不在 TaskPulse 中增加 Batch 表。MemoBridge 自己维护批次关系：

```text
batch_001
├── SourceItem 11778 → task_a
├── SourceItem 11779 → task_b
└── SourceItem 11780 → task_c
```

MemoBridge 通过以下接口查询单项结果：

```http
GET /tasks/{task_id}
GET /tasks/{task_id}/events
```

批次进度由 MemoBridge 聚合，例如：

```text
total=6, succeeded=4, running=1, failed=1
```

## 失败处理

可重试错误：

```text
provider_unavailable
rate_limited
provider_timeout
persistence_failed
```

不可重试错误：

```text
source_not_found
source_changed
invalid_model_output
```

LLM 限流时：

```text
Worker 调用 LLM
→ fail(retryable=true)
→ TaskPulse retrying
→ 退避后重新领取
```

资料变化时：

```text
Worker claim
→ SourceItem 被修改
→ content_hash 不一致
→ 不写回旧结果
→ fail(retryable=false)
```

## 第一版验收标准

- 一个 SemanticProfile 任务可以完整成功；
- 6 个 SourceItem 被拆成 6 个独立任务；
- 某些任务失败时，只重试失败任务；
- 成功任务不会重复调用 LLM；
- 相同幂等键不会创建重复任务；
- Worker 崩溃后任务可以恢复；
- SourceItem 变化时，旧结果不会覆盖新资料。

## 实施顺序

TaskPulse：

1. Claim 增加可选 workflow 过滤；
2. 为 Memory 和 MySQL 增加过滤测试；
3. 更新外部 Worker 协议文档。

MemoBridge：

1. 定义任务输入结构；
2. 实现单个 SourceItem Worker；
3. 实现 content_hash 校验；
4. 实现 SemanticProfile 幂等 Upsert；
5. 先完成单任务闭环，再扩展到批量。

第一版不做 Kafka、Redis、动态 DAG、向量数据库和多 Agent 协作。
