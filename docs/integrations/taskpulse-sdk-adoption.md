# TaskPulse SDK 接入 MemoBridge

## 目标

MemoBridge 当前的 `internal/taskpulse` 包已经能够完成联调，但它重复维护了 TaskPulse HTTP 协议。

公开 SDK 的目标是把以下逻辑收回 TaskPulse：

```text
HTTP 请求格式
204 无任务处理
LeaseToken 更新
Heartbeat
Progress
Complete
Fail
错误响应解析
```

MemoBridge 只保留 SemanticProfile 业务 Executor。

## 本地开发接入

在 MemoBridge 的 `go.mod` 中临时添加：

```go
replace github.com/zhaozhonghe/taskpulse => ../TaskPulse
```

实际相对路径以本机目录为准。生产环境应使用发布的版本号，不应依赖 `replace`。

## Worker 结构

```go
const workflow = "memobridge.semantic_profile"

client := &taskpulse.Client{
    BaseURL: os.Getenv("TASKPULSE_URL"),
}

runtime, err := taskpulseworker.New(taskpulseworker.Config{
    Client:            client,
    WorkerID:          workerID,
	Workflow:          workflow,
    LeaseDuration:     leaseDuration,
    PollInterval:      pollInterval,
    HeartbeatInterval: heartbeatInterval,
})
```

MemoBridge 注册：

```go
runtime.Register(
	workflow,
    semanticProfileExecutor,
)
```

启动：

```go
return runtime.Run(ctx)
```

## Executor 的职责

Executor 必须完成：

```text
读取 SourceItem
重新计算 content_hash
校验 prompt_version
调用 LLM
校验结构化输出
幂等 Upsert SemanticProfile
返回 result_ref
```

Executor 不应：

```text
直接修改 TaskPulse 数据库
自己实现 Claim 循环
自己启动 Heartbeat goroutine
把完整 Prompt 或 LLM 输出写入 TaskPulse
```

## 关闭语义

收到 `SIGINT` 或 `SIGTERM` 时：

```text
Runtime 停止领取新任务
取消当前 Executor context
最多等待 ShutdownTimeout
调用 release 主动归还当前 Lease
其他 Worker 立即重新领取任务
```

`release` 不会增加 `retry_count`，也不会把正常部署滚动更新误报成业务失败。只有进程被强制终止、
Executor 无法停止或 release 请求本身失败时，才回退到 Lease 过期后的恢复路径。

## 失败语义

MemoBridge Executor 使用 SDK 的错误分类：

```go
return taskpulseworker.Retryable(
    "provider_timeout",
    "LLM provider timeout",
    err,
)
```

或者：

```go
return taskpulseworker.Permanent(
    "source_changed",
    "source content changed during execution",
    nil,
)
```

## 验收

迁移后必须重新验证：

1. 单个 SemanticProfile 任务成功；
2. 相同幂等键重复创建不重复执行；
3. LLM 可重试错误进入 retrying；
4. 不可重试错误进入 failed；
5. Worker 退出后任务由新 Worker 恢复；
6. Heartbeat 更新后的 LeaseToken 被 Complete/Fail 使用；
7. 进程关闭不会产生 `context canceled` 业务失败。
