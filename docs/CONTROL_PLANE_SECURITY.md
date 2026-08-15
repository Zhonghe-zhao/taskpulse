# TaskPulse 控制面安全边界

## 当前鉴权状态

TaskPulse 当前只有 `/worker/tasks/*` Worker 协议要求 Bearer Token。

以下控制面端点当前**没有鉴权**：

- `/`、`/dashboard`、`/dashboard.js`；
- `POST /tasks`、`GET /tasks`、`GET /tasks/{id}`；
- `POST /tasks/{id}/cancel`、`GET /tasks/{id}/events`；
- `GET /task-stats`、`GET /metrics`。

因此，控制台当前只适合本机开发和受信任的隔离环境。Compose 默认将 TaskPulse HTTP 和 MySQL 绑定到 `127.0.0.1`，避免意外暴露到局域网。

## 暴露前必须完成

如果需要将控制面暴露到局域网、Kubernetes Ingress 或公网，必须先设计统一控制面鉴权：

1. 区分只读观察权限与创建、取消任务的操作权限；
2. 服务端执行鉴权，不能只在 Dashboard 隐藏按钮；
3. 记录取消等控制操作的操作者与审计事件；
4. 限制 `/metrics` 的访问范围，避免泄露 Workflow 和运行容量信息；
5. 配置 TLS、可信代理与请求速率限制。

在这些能力完成前，不应通过 `0.0.0.0` 暴露控制面。

## 数据边界

Dashboard 只读取 TaskPulse 自己的 Task、TaskEvent 和指标：

- 不访问 MemoBridge 数据库；
- 不解释 SourceItem、Subject 等业务对象；
- 列表只返回任务摘要，不批量返回完整输入和结果；
- 详情可通用展示 `workflow`、`requested_by`、`batch_id`、输入引用和 `result_ref`；
- `lease_token` 只属于 Worker 协议，不进入任务详情和 Dashboard。

MemoBridge 自己负责批次、资料标题和 SemanticProfile 等业务视图。
