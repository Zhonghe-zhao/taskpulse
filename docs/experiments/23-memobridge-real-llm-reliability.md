# 23：MemoBridge 真实 LLM 可靠执行联调

## 实验结论

2026-08-12 至 2026-08-13，TaskPulse 与 MemoBridge 完成了五组人工联调：正常执行、幂等重放与冲突检测、可重试失败、Worker 优雅交接和 Worker 崩溃恢复。

实验使用真实 MemoBridge SourceItem 和真实 DeepSeek 调用，不是 TaskPulse 的 `llm_analysis` Fake Worker。五组链路均符合预期，证明 `memobridge.semantic_profile` 已经能够作为 TaskPulse 的真实业务负载运行。

## 要验证的问题

SemanticProfile 生成包含数据库读取、外部 LLM 调用、结构化结果校验和业务写回，单次执行约数十秒。需要回答：

1. 正常任务能否从业务入口执行到 SemanticProfile 写回？
2. 重复提交是否会重复调用 LLM？
3. LLM 暂时不可用时，是否按照退避策略重试并耗尽预算后终止？
4. Worker 正常退出时，任务能否主动交给其他 Worker？
5. Worker 强制崩溃时，任务能否在 Lease 过期后恢复？

## 系统边界

```text
MemoBridge 桌面端 / 测试请求
  -> MemoBridge API
  -> TaskPulse HTTP API
  -> TaskPulse MySQL
  -> MemoBridge Worker Claim
  -> MemoBridge PostgreSQL 读取 SourceItem
  -> DeepSeek
  -> MemoBridge 幂等 Upsert SemanticProfile
  -> TaskPulse Complete / Fail
```

TaskPulse 不读取 MemoBridge PostgreSQL，不理解 SourceItem 业务含义，也不保存资料正文、Prompt 或完整模型输出。TaskPulse 只保存任务输入引用、执行状态、租约、事件和 `result_ref`。

## 操作环境

| 项目 | 本次配置 |
|---|---|
| 操作日期 | 2026-08-12 至 2026-08-13 |
| 操作系统 | Windows，PowerShell |
| TaskPulse | Docker Compose 启动，MySQL 持久化 |
| TaskPulse Dashboard/API | `http://127.0.0.1:8085` |
| MemoBridge API | 本机进程；实验请求曾通过 `http://127.0.0.1:18083` 访问 |
| MemoBridge Worker | 本机独立进程 |
| MemoBridge 数据库 | PostgreSQL 测试数据库 |
| LLM | DeepSeek 真实 Provider |
| workflow | `memobridge.semantic_profile` |
| Lease | 30 秒 |
| Heartbeat | 10 秒 |
| 最大重试 | 3 |

本次使用过的 Worker ID：

```text
memobridge-worker-local-1
memobridge-worker-local-2
memobridge-worker-crash-1
```

任务幂等键：

```text
semantic-profile:{source_item_id}:{content_hash}:{prompt_version}
```

## 实验一：正常成功闭环

### 输入

```text
source_item_id = 11799
requested_by = manual_test
prompt_version = source_semantic_profile:v1
task_id = task_1786470612941742914_6987
worker_id = memobridge-worker-local-1
```

### 操作

从 MemoBridge 创建 SemanticProfile 任务，同时观察 MemoBridge Worker 日志和 TaskPulse Dashboard。Worker Claim 后读取资料、调用真实 DeepSeek、写回 SemanticProfile，最后向 TaskPulse Complete。

### 事件和结果

```text
task_created
-> task_started
-> task_progress(10%)
-> heartbeat
-> task_progress(90%)
-> task_succeeded(100%)
```

观测结果：

- 状态最终为 `succeeded`；
- `retry_count = 0/3`；
- 排队约 0 秒，执行约 59 秒，总耗时约 60 秒；
- `result_ref.semantic_profile_id = 99`；
- `result_ref` 只包含 `source_item_id`、`content_hash`、`prompt_version` 和结果 ID；
- MemoBridge 成功保存 SemanticProfile。

### 结论

真实业务链路通过。任务事实由 TaskPulse/MySQL 持久化，业务数据所有权仍属于 MemoBridge。

## 实验二：幂等重放与冲突检测

### 相同请求重放

对 SourceItem 11799 再次提交与原任务相同的请求：

```json
{"requested_by":"manual_test"}
```

TaskPulse 返回原任务：

```text
task_id = task_1786470612941742914_6987
```

Worker 没有再次 Claim，LLM 没有再次调用，Dashboard 没有新增实际任务。

### 相同键但不同请求

将请求来源改为：

```json
{"requested_by":"idempotency_test"}
```

虽然语义幂等键仍相同，但完整任务输入已经不同，TaskPulse 返回：

```text
HTTP 409
idempotency key is already used by a different request
```

### 结论

两种行为均正确：

- 相同 workflow、幂等键和请求指纹返回原任务；
- 相同幂等键对应不同请求时拒绝复用，防止调用方错误覆盖请求语义。

当前 `requested_by` 位于任务 input，因此参与请求指纹比较。这是当前协议的严格语义，不是执行故障。

## 实验三：Provider 不可用与退避重试

### 输入

```text
requested_by = retry_test
task_id = task_1786548320587574617_24948
worker_id = memobridge-worker-local-1
max_retries = 3
```

### 故障注入

让 MemoBridge Worker 使用不可访问的 DeepSeek 地址，使真实 Provider 调用失败。Worker 将错误分类为：

```text
error_code = provider_unavailable
retryable = true
```

### 事件和结果

任务反复经历：

```text
task_started
-> task_progress(10%)
-> task_retrying
-> task_retry_started
```

Dashboard 显示的退避时间约为 1 秒、4 秒；最后：

```text
retry_count = 3/3
status = failed
progress = 10%
result_ref = empty
```

### 结论

- 可重试错误正确进入退避调度；
- 每次尝试重新建立 Worker Lease；
- 重试预算耗尽后进入稳定终态，不会无限重试；
- 失败任务没有保存伪造或不完整的业务结果。

该轮只验证“预算耗尽后失败”。后续的交接实验额外证明了依赖恢复后，尚有预算的原任务可以继续成功。

## 实验四：优雅退出、临时配置错误与恢复

### 操作

1. `memobridge-worker-local-1` Claim 长任务并上报 10%；
2. 正常终止 Worker，使 Runtime 有机会主动 Release；
3. `memobridge-worker-local-2` 领取原任务；
4. local-2 首次启动时遗漏 DeepSeek 配置，返回 `provider_unavailable`；
5. 修正配置后重新启动 Worker；
6. 原任务重试并成功。

本次截图未包含 Task ID，报告不补写无法确认的标识。

### 事件和结果

```text
task_created
-> task_started(local-1)
-> task_progress(10%)
-> task_released
-> task_started(local-2)
-> task_progress(10%)
-> task_retrying(provider_unavailable)
-> task_retry_started(local-2)
-> task_progress(10%)
-> task_progress(90%)
-> task_succeeded(100%)
```

### 结论

- 正常退出使用 `task_released`，无需等待完整 Lease 到期；
- Release 不等同于执行失败，不应消耗重试预算；
- 任务能够跨 Worker 交接；
- 临时依赖恢复后，原任务继续执行，不需要创建替代任务。

## 实验五：Worker 强制崩溃恢复

### 操作

1. `memobridge-worker-crash-1` Claim 新任务并上报 10%；
2. 强制终止进程，不允许 Runtime 发送 Release；
3. 等待 30 秒 Lease 到期；
4. 保持 `memobridge-worker-local-2` 运行并持续 Claim；
5. local-2 恢复原任务并完成。

本次截图未包含 Task ID，报告保留 Worker、时间和事件证据，不伪造任务标识。

### 事件和结果

```text
2026-08-13 02:57:54 task_created
2026-08-13 02:57:54 task_started(worker-crash-1, lease until 02:58:24)
2026-08-13 02:57:54 task_progress(10%)
2026-08-13 03:00:00 task_recovered(worker-local-2)
2026-08-13 03:00:00 task_progress(10%)
2026-08-13 03:00:52 task_progress(90%)
2026-08-13 03:00:53 task_succeeded(100%)
```

关键证据是出现 `task_recovered` 且没有 `task_released`。这说明原 Worker 没有正常归还任务，新 Worker 是在旧 Lease 失效后取得新的有效执行权。

### 结论

- Worker 崩溃不会丢失持久化任务；
- 任务所有权不依赖某个 Worker 进程；
- Lease 到期后其他 Worker 可以恢复任务；
- 恢复从 10% 重新执行符合 at-least-once 语义；
- MemoBridge 必须继续保持业务写回幂等，TaskPulse 不承诺外部副作用 exactly-once。

## 总结

| 场景 | 结果 | 主要证据 |
|---|---|---|
| 真实 LLM 正常执行 | 通过 | 真实 SemanticProfile 写回，TaskPulse succeeded |
| 完全相同请求重放 | 通过 | 返回同一 Task ID，不重新执行 |
| 相同键、不同请求 | 通过 | HTTP 409，原任务不被覆盖 |
| Provider 不可用 | 通过 | provider_unavailable、退避、3/3 后 failed |
| Worker 优雅退出 | 通过 | task_released，其他 Worker 接手 |
| 依赖恢复 | 通过 | 原任务重试后 succeeded |
| Worker 强制崩溃 | 通过 | task_recovered，最终 succeeded |

## 尚未由本轮证明

- SourceItem 在执行期间变化时返回 `source_changed`，且旧结果不覆盖新资料（当前无强产品需求，保留 hash 防线与单测即可）；
- Kubernetes 中 MemoBridge Worker Pod 的真实崩溃和优雅交接；
- 多条 SemanticProfile 任务的批量并发、部分失败与批次视图；
- 多 TaskPulse 实例指标的 Prometheus 聚合。

后续已由独立实验补齐：

- Dashboard 取消：见 `24-dashboard-task-cancellation.md`；
- Complete 响应丢失重放：见 `25-complete-idempotent-replay.md`。

## 后续证据保存

已知 Task ID 可以使用统一脚本保存原始任务、事件和指标：

```powershell
.\scripts\capture-task-evidence.ps1 `
  -TaskID task_1786470612941742914_6987 `
  -Label memobridge-real-success

.\scripts\capture-task-evidence.ps1 `
  -TaskID task_1786548320587574617_24948 `
  -Label memobridge-provider-unavailable
```

原始证据输出到 `artifacts/evidence`。若后续从 Dashboard 或数据库取得交接、崩溃任务 ID，应再补采对应 JSON，而不是只保留截图。
