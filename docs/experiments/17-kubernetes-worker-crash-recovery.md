# Kubernetes Worker 崩溃恢复实验

本实验验证任务状态由 TaskPulse/MySQL 持久化，而不是保存在某一个 Worker Pod 内；Worker Pod 消失后，Lease 到期可让健康 Worker 恢复任务。

## 启动

```powershell
kubectl apply -k deploy/k8s
kubectl rollout status deployment/taskpulse -n taskpulse
kubectl rollout status deployment/llm-worker -n taskpulse
kubectl get pods -n taskpulse -o wide
```

通过转发后的 TaskPulse Service 创建任务。为使任务在删除 Pod 时仍在执行，将 fake LLM 延迟设为与 Lease 同量级：

```powershell
kubectl set env deployment/llm-worker -n taskpulse TASKPULSE_LLM_FAKE_DELAY=30s
kubectl port-forward service/taskpulse -n taskpulse 18080:8080
```

创建一个 `llm_analysis` 任务，记录任务 ID，找到打印 Claim 日志的 Pod，然后删除该 Pod：

```powershell
kubectl get pods -n taskpulse -l app=llm-worker
kubectl delete pod -n taskpulse <claimed-worker-pod>
```

## 预期证据

```text
旧 Worker Claim 任务
旧 Pod 被删除
Deployment 创建替代 Pod
Lease 过期
TaskPulse 写入 task_recovered
替代 Worker 再次 Claim
任务最终 succeeded 或按重试策略终态失败
```

检查任务状态、事件和 Worker 日志：

```powershell
Invoke-RestMethod "http://127.0.0.1:18080/tasks/<task-id>" |
  ConvertTo-Json -Depth 10
Invoke-RestMethod "http://127.0.0.1:18080/tasks/<task-id>/events" |
  ConvertTo-Json -Depth 10
kubectl logs -n taskpulse -l app=llm-worker --prefix
```

将任务状态、事件和指标保存为可复查文件：

```powershell
.\scripts\capture-task-evidence.ps1 `
  -BaseURL http://127.0.0.1:18080 `
  -TaskID <task-id> `
  -Label kubernetes-worker-recovery
```

重要保证不是“外部 LLM 绝对只调用一次”。TaskPulse 保证的是过期 Lease 会被 fencing，任务可在不丢失持久状态的前提下重新尝试。业务 Executor 必须保证写回幂等。
