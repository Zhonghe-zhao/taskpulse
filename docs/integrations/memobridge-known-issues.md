# MemoBridge 接入待办

## SemanticProfile 模型输出错误分类

当前 MemoBridge 将部分 LLM JSON decode 或 Schema validation 失败归类为
`provider_unavailable` 并进行重试。该分类不准确：Provider 已经正常返回，失败发生在模型输出无法满足业务契约。

MemoBridge 应将此类错误改为：

```text
error_code = invalid_model_output
retryable = false（或由 MemoBridge 明确配置有限重试）
```

此问题由 MemoBridge 的 Worker/AI Service 修复。TaskPulse 只执行 Worker 上报的通用失败协议，不根据错误文本猜测或覆盖业务错误分类。
