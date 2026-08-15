# TaskPulse Go SDK

## 创建任务

```go
client := &taskpulse.Client{BaseURL: "http://taskpulse:8080"}

task, err := client.CreateTask(ctx, taskpulse.CreateTaskRequest{
    Workflow:       "memobridge.semantic_profile",
    Input:          payload,
    MaxRetries:     3,
    IdempotencyKey: "semantic-profile:11778:hash:v1",
})
```

## 注册 Worker

```go
runtime, err := taskpulseworker.New(taskpulseworker.Config{
    Client:            client,
    WorkerID:          "memobridge-worker-1",
    Workflow:          "memobridge.semantic_profile",
    LeaseDuration:     30 * time.Second,
    PollInterval:      time.Second,
    ClaimRetryMaxInterval: 5 * time.Second,
    HeartbeatInterval: 10 * time.Second,
    ExecutionTimeout: 90 * time.Second,
    ShutdownTimeout: 5 * time.Second,
})
if err != nil {
    return err
}

if err := runtime.Register("memobridge.semantic_profile", executor); err != nil {
    return err
}
return runtime.Run(ctx)
```

Executor 只负责业务逻辑：

```go
type SemanticProfileExecutor struct{}

func (e SemanticProfileExecutor) Execute(
    ctx context.Context,
    task *taskpulse.Task,
    progress taskpulseworker.ProgressReporter,
) (taskpulseworker.Result, error) {
    // 读取业务数据、调用 LLM、校验并幂等写回。
    _ = progress.Report(ctx, 50, "llm completed")
    return taskpulseworker.Result{
        ResultRef: []byte(`{"source_item_id":11778}`),
    }, nil
}
```

Runtime 自动负责：

```text
Claim
Heartbeat
LeaseToken 更新
Progress
Complete
Fail
重试错误分类
SIGTERM 优雅退出
临时网络错误、429 和 5xx 的 Claim 重试
```

收到 `SIGTERM` 后，Runtime 会停止 Claim，取消 Executor 的 `context`，最多等待
`ShutdownTimeout`，随后调用 Worker `release` 协议立即归还当前 Lease。该操作不会
增加 `retry_count`；其他 Worker 可以马上重新领取。若进程被强制终止或 release 调用失败，
仍由 Lease 过期恢复作为兜底，因此外部副作用仍需由业务 Executor 保证幂等。

`ExecutionTimeout` 限制的是一次 Executor 尝试，不是 Lease。Lease 仍由 Heartbeat
维持；当执行超过该时限时 Runtime 会取消 Executor 的 `context`，并以
`execution_timeout` 可重试错误调用既有 Fail 协议。该配置适合为 LLM Provider、爬虫等
外部调用设置明确上界。若 Executor 忽略 `ctx.Done()`，Runtime 仍会停止 Heartbeat 并报告
超时，但 Go 无法安全杀死那个 goroutine；外部副作用仍必须由业务层保证幂等。

Worker 协议使用 `Authorization: Bearer <token>` 鉴权。优先在 `taskpulse.Client.WorkerAuthToken` 设置 token；未设置时 SDK 会读取 `TASKPULSE_WORKER_AUTH_TOKEN`，便于容器化 Worker 通过环境变量注入。任务创建、查询和取消 API 不携带该 Worker 凭证。

控制面暂时不可用时，Claim 重试使用有上限的指数退避：`PollInterval` 是初始等待时间，`ClaimRetryMaxInterval` 是最大等待时间，默认上限为 5 秒。这与任务已经领取后由 TaskPulse 执行的业务重试策略相互独立。

`Heartbeat`、`Progress`、`Complete` 和 `Fail` 必须携带 Claim 或上一次响应返回的 `LeaseToken`。token 绑定任务 ID、Worker ID 与版本；Worker 续租、上报进度后应使用响应中的新 token，旧 token 或空 token 会被拒绝。

Runtime 在调用 Executor 前还会校验 Claim 响应：任务 ID、workflow、`running` 状态和 lease token
必须完整，且 workflow 必须与 Runtime 注册的 workflow 一致。协议不匹配时 Worker 会停止而不会执行
业务代码。
