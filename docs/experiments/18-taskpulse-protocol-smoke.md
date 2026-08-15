# TaskPulse 协议冒烟实验

这个实验不依赖 MemoBridge，也不需要下载新的镜像或 Go 模块。它直接验证 TaskPulse 对外提供的最小 Worker 协议：

```text
创建任务
  -> 相同幂等键重复创建
  -> Claim 获取 lease_token
  -> Progress 更新版本
  -> Complete 保存 result_ref
  -> 使用相同请求重复 Complete
```

## 前置条件

TaskPulse 已经启动，并且 HTTP 端口为 `8085`。如果端口不同，执行时传入 `-BaseUrl`。

## 执行

在 PowerShell 中，从 TaskPulse 根目录运行：

```powershell
.\scripts\smoke-taskpulse-protocol.ps1
```

也可以指定一次真实的 MemoBridge 资料引用：

```powershell
.\scripts\smoke-taskpulse-protocol.ps1 `
  -SourceItemId 11778 `
  -ContentHash "sha256:..." `
  -PromptVersion "source_semantic_profile:v1"
```

脚本会自动生成唯一幂等键，避免上一次冒烟任务已经完成时无法再次 Claim。

## 通过标准

输出应满足：

```text
lease_token_present : True
progress            : 50
completed_status    : succeeded
replayed_status     : succeeded
```

这证明：

- 相同 `Idempotency-Key` 不会创建第二个任务；
- Worker 只能使用 Claim 返回的租约令牌继续操作；
- Progress 后使用新版本和新令牌完成任务；
- 重复 Complete 不产生新的状态转换或错误副作用；
- `result_ref` 被保存，而不是把完整业务结果交给 TaskPulse。

该脚本只验证 TaskPulse 底座协议，不代表 MemoBridge 的 LLM 业务执行成功。MemoBridge 联调仍需单独运行 API 和 Worker，并检查 SemanticProfile 是否写回。
