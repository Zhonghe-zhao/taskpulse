# MemoBridge 真实联调冒烟测试

这是 TaskPulse 第一个真实业务负载的验收测试：

```text
MemoBridge API
  -> TaskPulse CreateTask
  -> MemoBridge Worker Claim
  -> content_hash validation
  -> SemanticProfile Upsert
  -> TaskPulse Complete(result_ref)
```

仅在 `compose.integration.yaml` 已启动且两个服务健康检查均通过后运行：

```powershell
.\scripts\smoke-memobridge-integration.ps1
```

从脚本输出中取得 `task_id` 后，再保存 TaskPulse 侧的原始状态、事件和指标：

```powershell
.\scripts\capture-task-evidence.ps1 -TaskID <task_id> -Label memobridge-semantic-profile
```

脚本创建可丢弃测试数据。它刻意与 TaskPulse 协议冒烟测试分开，因为它验证的是业务写回和跨服务所有权边界，而不只是 TaskPulse HTTP 协议。

通过条件：

- 重复调用 MemoBridge 接口返回同一个 `task_id`；
- TaskPulse 到达 `succeeded`；
- TaskEvent 包含创建、Claim/Progress 和成功；
- `memobridge.semantic_profile` 的 Claim 与完成 Prometheus 计数均增长；
- MemoBridge 能读取已提交 SourceItem 的 SemanticProfile；
- TaskPulse Result 只含输出引用，不含资料正文或 LLM 响应。
