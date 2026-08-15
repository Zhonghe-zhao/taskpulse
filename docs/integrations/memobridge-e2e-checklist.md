# MemoBridge 与 TaskPulse 联调验收清单

## 双方职责

TaskPulse 只负责可靠执行：任务持久化、workflow 筛选、Claim、租约、Heartbeat、Progress、Retry、崩溃恢复和事件记录。

MemoBridge 负责业务执行：读取 SourceItem、校验 content_hash、构造 Prompt、调用 LLM、校验结果、幂等写入 SemanticProfile。

两边不共享业务表。TaskPulse 不访问 MemoBridge 数据库，也不保存资料全文、Prompt 或完整 LLM 输出。

## 调用链

```text
MemoBridge 创建任务
  -> workflow = memobridge.semantic_profile
  -> idempotency_key = semantic-profile:{source_item_id}:{content_hash}:{prompt_version}
  -> TaskPulse 保存任务
  -> MemoBridge Worker Claim
  -> TaskPulse 返回 lease_token
  -> Worker 读取 SourceItem 并校验 hash
  -> Worker 调用 LLM 并写入 SemanticProfile
  -> Worker complete(result_ref) 或 fail(error_code)
```

## 验收场景

### 1. 单任务成功

- 创建一条 `memobridge.semantic_profile` 任务。
- MemoBridge Worker 成功 Claim。
- 观察 Progress、Heartbeat 和 Complete 请求。
- TaskPulse 最终为 `succeeded`。
- MemoBridge 产生一条 SemanticProfile。

### 2. LLM 暂时失败

- 让 LLM 返回 `provider_unavailable` 或 `provider_timeout`。
- Worker 使用 `fail(retryable=true)`。
- TaskPulse 进入 retrying。
- 退避后再次 Claim。
- 恢复 LLM 后任务成功。

### 3. Worker 崩溃

- Worker Claim 后进程退出。
- 等待 lease 到期。
- TaskPulse 恢复任务。
- 新 Worker 重新 Claim。
- MemoBridge 使用幂等 Upsert，不能产生重复 SemanticProfile。

### 4. 重复创建

- 使用相同 workflow 和 idempotency_key 创建两次。
- 两次返回同一个 Task。
- 更换 content_hash 或 prompt_version 后，应创建新任务。

### 5. Complete 请求重试

- 第一次 Complete 已落库，但模拟 HTTP 响应丢失。
- Worker 使用相同 lease_token 再次 Complete。
- TaskPulse 不重复修改任务，也不重复追加事件。

## 当前边界

`result_ref` 只保存业务结果引用，例如 `source_item_id`、`content_hash` 和 `prompt_version`。完整 SemanticProfile 保存在 MemoBridge，TaskPulse 不保存完整 LLM 输出。

## 已完成的真实联调

2026-08-12 至 2026-08-13 已使用真实 SourceItem、真实 DeepSeek Provider 和独立 MemoBridge Worker 完成成功、幂等、Provider 失败重试、优雅交接与强制崩溃恢复。详细环境、步骤、事件和结论见：

[23：MemoBridge 真实 LLM 可靠执行联调](../experiments/23-memobridge-real-llm-reliability.md)
