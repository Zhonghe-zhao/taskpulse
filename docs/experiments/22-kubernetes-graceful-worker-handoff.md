# 实验 22：Kubernetes Worker 优雅终止的任务交接

## 要验证的区别

本实验与强制删除 Pod 的 Lease 恢复实验不同：

```text
docker kill / 进程崩溃
  -> 没有机会调用 release
  -> 等待 Lease 到期
  -> task_recovered

kubectl delete pod（正常 termination grace period）
  -> SIGTERM
  -> Worker 取消 Executor
  -> release
  -> task_released
  -> 其他 Worker 立即重新 Claim
```

正常缩容不应该消耗 retry budget，也不应该等待完整 Lease。

## 前置条件

```powershell
kubectl apply -k deploy/k8s
kubectl rollout status deployment/taskpulse -n taskpulse
kubectl rollout status deployment/llm-worker -n taskpulse
kubectl port-forward -n taskpulse svc/taskpulse 18080:8080
```

在另一个终端将 fake LLM 延长，使任务在删除 Pod 时仍处于执行状态：

```powershell
kubectl set env deployment/llm-worker -n taskpulse TASKPULSE_LLM_FAKE_DELAY=30s
kubectl rollout status deployment/llm-worker -n taskpulse
```

## 步骤

创建一个 `llm_analysis` 任务，待某个 Worker 输出 `task claimed` 后记录该 Pod 名称：

```powershell
kubectl get pods -n taskpulse -l app=llm-worker
kubectl logs -n taskpulse <claimed-worker-pod> --timestamps
```

删除该 Pod。不要使用 `--grace-period=0` 或 `--force`，否则这是崩溃恢复实验而不是优雅交接：

```powershell
kubectl delete pod -n taskpulse <claimed-worker-pod>
```

任务完成后收集证据：

```powershell
.\scripts\capture-kubernetes-task-evidence.ps1 `
  -TaskID <task-id> `
  -TaskPulseURL http://127.0.0.1:18080 `
  -Label kubernetes-graceful-handoff
```

## 通过条件

TaskEvent 必须包含：

```text
task_created
-> task_started
-> task_released
-> task_started
-> task_succeeded
```

并验证：

- `task_released` 发生在原 `lease_expires_at` 之前；
- 后续 `task_started` 由其他 Worker Pod 触发；
- `retry_count` 在 release 前后保持不变；
- 该路径不应出现 `task_recovered`；
- Worker 日志包含 `task released for graceful shutdown`；
- Prometheus 指标仍能看到相应 workflow 的 claim、release 和 completion 计数。

## 边界

Kubernetes 只提供有限的终止宽限。`llm-worker` 的 `terminationGracePeriodSeconds=10`，
Runtime 的 `TASKPULSE_EXTERNAL_SHUTDOWN_TIMEOUT=5s`，因此 release 有最多 5 秒完成 HTTP
调用。节点故障、OOMKill 或强制 Kill 无法保证该路径执行，仍依赖 Lease 过期恢复。
