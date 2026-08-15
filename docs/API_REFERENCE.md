# TaskPulse API 与 Worker 协议

默认地址为 `http://127.0.0.1:8085`。控制面接口当前没有用户鉴权，只应绑定回环地址；Worker 接口必须携带 Bearer Token。

## 控制面接口

| 方法 | 路径 | 说明 |
|---|---|---|
| `POST` | `/tasks` | 创建任务 |
| `GET` | `/tasks` | 分页查询任务 |
| `GET` | `/task-stats` | 按 Workflow 聚合状态 |
| `GET` | `/tasks/{task_id}` | 查询任务详情 |
| `GET` | `/tasks/{task_id}/events` | 查询事件时间线 |
| `POST` | `/tasks/{task_id}/cancel` | 取消可执行或运行中任务 |
| `GET` | `/metrics` | Prometheus 文本指标 |
| `GET` | `/dashboard` | 运维控制台 |

### 创建任务

```http
POST /tasks
Idempotency-Key: semantic-profile:11778:sha256:example:v1
Content-Type: application/json
```

```json
{
  "workflow": "memobridge.semantic_profile",
  "input": {
    "source_item_id": 11778,
    "content_hash": "sha256:example",
    "prompt_version": "source_semantic_profile:v1",
    "requested_by": "manual_test"
  },
  "max_retries": 3
}
```

真实契约：

- `workflow`：字符串，必填；
- `input`：任意 JSON，建议只保存业务引用；
- `max_retries`：非负整数；
- `Idempotency-Key`：HTTP Header，不放在 JSON Body；
- 幂等范围：`workflow + Idempotency-Key`；
- 相同键和相同请求返回原任务，相同键但请求不同返回 `409`。

### 查询与筛选

```text
GET /tasks?workflow=memobridge.semantic_profile&status=failed&limit=25
GET /task-stats?workflow=memobridge.semantic_profile
```

列表使用不透明游标翻页。客户端应原样传递响应中的 `next_cursor`，不要解析其内部格式。

## Worker 接口

Worker 请求必须包含：

```http
Authorization: Bearer <TASKPULSE_WORKER_AUTH_TOKEN>
```

| 方法 | 路径 | 说明 |
|---|---|---|
| `POST` | `/worker/tasks/claim` | 按 Workflow 领取任务 |
| `POST` | `/worker/tasks/{id}/heartbeat` | 续租 |
| `POST` | `/worker/tasks/{id}/progress` | 上报进度 |
| `POST` | `/worker/tasks/{id}/complete` | 提交成功结果 |
| `POST` | `/worker/tasks/{id}/fail` | 提交失败和重试分类 |
| `POST` | `/worker/tasks/{id}/release` | 优雅退出时主动释放任务 |

Claim 请求：

```json
{
  "worker_id": "memobridge-worker-1",
  "workflow": "memobridge.semantic_profile",
  "lease_duration": "30s"
}
```

后续操作必须携带 Claim 返回的 `lease_token` 和最新 `version`。旧 Token、旧 Version、租约过期的 Worker 或错误的 `worker_id` 不能修改任务。

Fail 请求示例：

```json
{
  "worker_id": "memobridge-worker-1",
  "lease_token": "opaque-token",
  "version": 2,
  "error_code": "provider_unavailable",
  "error_message": "model service unavailable",
  "retryable": true,
  "retry_after": "2s"
}
```

业务 Worker 优先使用 `pkg/taskpulse` 和 `pkg/taskpulseworker`，不要重复实现 Claim、Heartbeat、Token 更新、控制面退避和优雅退出。

## 一致性边界

TaskPulse 提供持久状态、单个有效租约和至少一次执行尝试，不承诺外部副作用 exactly-once。Worker 在业务写入成功后、Complete 返回前崩溃，任务可能再次执行，因此业务结果必须幂等写入。

TaskPulse 不应保存业务正文、完整 Prompt、完整模型输出或直接访问业务数据库。

