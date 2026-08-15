# 25：Complete 响应丢失与幂等重放

## 实验结论

2026-08-13，用手工 Worker 协议验证：Complete 首次成功落库后，使用**同一** `lease_token` / `version` / `result_ref` 再次 Complete，任务保持 `succeeded`，且 `task_succeeded` 事件只出现一次。

这证明控制面 Complete 可安全重试，用来覆盖「业务已写回、Complete 已成功、但 HTTP 响应丢失」的不确定窗口。

## 要验证的问题

分布式系统中的经典不确定结果：

```text
Worker 完成业务副作用（或本实验中的协议 Complete）
  -> POST /worker/tasks/{id}/complete
  -> TaskPulse 已写入 succeeded + task_succeeded
  -> HTTP 响应丢失 / 客户端超时
  -> Worker 不知道是否成功
  -> 用相同 fencing 字段再发一次 Complete
```

需要确认：

- 第二次 Complete 不报错、不改坏状态；
- 不追加第二条 `task_succeeded`；
- 不要求 Worker 重新 Claim；
- 解释清楚：TaskPulse 是 at-least-once，不是外部副作用 exactly-once。

## 环境

| 项目 | 配置 |
|---|---|
| 日期 | 2026-08-13 |
| TaskPulse | Docker Compose + MySQL，`http://127.0.0.1:8085` |
| Worker 认证 | `Authorization: Bearer taskpulse_dev_worker_token` |
| 模拟 Worker ID | `complete-replay-worker-1` |
| workflow | `memobridge.semantic_profile` |
| 真实 MemoBridge Worker | **已停止**，避免抢 Claim |
| 业务 LLM | 未调用（协议层重放，假 `result_ref`） |

## 操作步骤

1. 停止本机 MemoBridge Worker。
2. 创建可丢弃任务并 Claim：

```powershell
$base = "http://127.0.0.1:8085"
$auth = @{ Authorization = "Bearer taskpulse_dev_worker_token" }
$workerId = "complete-replay-worker-1"
```

3. `POST /worker/tasks/claim` 领到任务，保存 `lease_token` 与 `version`。
4. 第一次 `POST /worker/tasks/{id}/complete`（携带同一 token/version 与 `result_ref`）。
5. **不重新 Claim**，用完全相同的 Complete body 再发一次。
6. 查询 `/tasks/{id}/events`，统计 `task_succeeded` 次数。
7. 保存证据：

```powershell
.\scripts\capture-task-evidence.ps1 `
  -TaskID <task_id> `
  -Label complete-idempotent-replay
```

也可直接运行已覆盖该断言的协议冒烟（需带 Worker 认证时按环境补 Header）：

```powershell
.\scripts\smoke-taskpulse-protocol.ps1
```

## 结果

证据目录：

```text
artifacts/evidence/20260812T195343Z-complete-idempotent-replay-task.json
artifacts/evidence/20260812T195343Z-complete-idempotent-replay-events.json
artifacts/evidence/20260812T195343Z-complete-idempotent-replay-metrics.prom
artifacts/evidence/20260812T195343Z-complete-idempotent-replay-metadata.json
```

核对：

```text
status          = succeeded
worker_id       = complete-replay-worker-1
retry_count     = 0/3
version         = 2
events          = task_created -> task_started -> task_succeeded
succeeded_count = 1
```

`result_ref` 仅含引用字段（本实验为协议桩数据）：

```json
{
  "source_item_id": 888888,
  "content_hash": "sha256:complete-replay",
  "prompt_version": "source_semantic_profile:v1",
  "semantic_profile_id": 1
}
```

## 结论

| 检查项 | 结果 |
|---|---|
| 第一次 Complete | `succeeded` |
| 第二次同请求重放 | 仍 `succeeded`，可接受 |
| `task_succeeded` 次数 | 1 |
| 是否新建任务 | 否 |

**Complete 幂等重放：通过。**

## 工程含义

```text
控制面：Complete / Fail / Release 对同一 fencing 凭证幂等重放
业务面：MemoBridge SemanticProfile Upsert 必须幂等
合起来：响应丢失可安全重试
做不到：承诺外部 LLM 或业务写回绝对只发生一次
```

面试可直接说：

> TaskPulse 保证任务状态机与事件审计的可靠收敛，执行语义是 at-least-once。  
> 「Complete 响应丢失」靠幂等 Complete 消化；「业务写回重复」靠 MemoBridge 的 content_hash / Upsert 消化。

## 与实验 23 的关系

`23-memobridge-real-llm-reliability.md` 证明了真实 DeepSeek 闭环、创建幂等、重试、优雅交接和崩溃恢复。本实验补上其中「尚未证明」的 Complete 响应丢失重放，且刻意停在协议层，避免再消耗真实 LLM 额度。

## 边界

- 本实验未模拟「业务写回成功但第一次 Complete 未到达服务端」；那种情况靠 Worker 重试 Complete + 业务幂等共同覆盖。
- 不同 `lease_token` / `version` 的 Complete 应被 fencing 拒绝，不属于本次重放范围。
