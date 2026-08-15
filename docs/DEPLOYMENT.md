# TaskPulse 部署手册

本文覆盖本地 Docker Compose 和 Docker Desktop Kubernetes。当前 Kubernetes 结果属于本地多节点实验，不宣称生产可用。

## Docker Compose

### 启动

```powershell
cd E:\CS\TaskPulse
docker compose up -d --build
docker compose ps
```

默认只绑定宿主机回环地址：TaskPulse `127.0.0.1:8085`，MySQL `127.0.0.1:3306`。

需要覆盖开发密码和 Worker Token 时，复制 `.env.example` 为 `.env`，填写本地值；`.env` 已被 Git 忽略。

### 扩容示例 Worker

```powershell
docker compose up -d --scale llm-worker=4
docker compose ps
```

每个容器根据 hostname 生成独立 Worker ID。不要为多个副本设置同一个固定 ID。

## Docker Desktop Kubernetes

### 1. 检查集群

```powershell
kubectl get nodes -o wide
```

### 2. 构建镜像

```powershell
cd E:\CS\TaskPulse
docker build --build-arg APP=taskpulse -t taskpulse:dev .
docker build --build-arg APP=llm-worker -t taskpulse-llm-worker:dev .
```

清单使用 `imagePullPolicy: IfNotPresent`，适用于能够访问 Docker Desktop 本地镜像存储的集群。远程集群必须将镜像推送到镜像仓库并修改镜像地址。

### 3. 配置 Secret

`deploy/k8s/secret.yaml` 只包含占位符。部署前必须为本地实验设置新的 MySQL 密码和 Worker Token，禁止提交真实值。

推荐通过命令创建 Secret，再从 `kustomization.yaml` 临时移除 `secret.yaml`，或者维护一个不提交 Git 的覆盖文件。最小命令示例：

```powershell
kubectl create namespace taskpulse --dry-run=client -o yaml | kubectl apply -f -

kubectl create secret generic mysql-secret `
  -n taskpulse `
  --from-literal=MYSQL_USER=taskpulse `
  --from-literal=MYSQL_PASSWORD='<local-password>' `
  --from-literal=MYSQL_ROOT_PASSWORD='<local-root-password>' `
  --from-literal=TASKPULSE_WORKER_AUTH_TOKEN='<local-worker-token>' `
  --dry-run=client -o yaml | kubectl apply -f -
```

不要把实际密码直接写进 PowerShell 历史；正式环境应使用 Secret 管理系统。

### 4. 应用清单

如果已通过命令创建 Secret，请确保本次 apply 不会用占位符 Secret 覆盖它。应用其他资源后检查：

```powershell
kubectl apply -f deploy/k8s/configmap.yaml
kubectl apply -f deploy/k8s/mysql.yaml
kubectl apply -f deploy/k8s/taskpulse.yaml
kubectl apply -f deploy/k8s/llm-worker.yaml

kubectl rollout status statefulset/mysql -n taskpulse
kubectl rollout status deployment/taskpulse -n taskpulse
kubectl rollout status deployment/llm-worker -n taskpulse
kubectl get deployment,statefulset,service,pods -n taskpulse -o wide
```

只有使用了不含真实值、且已经正确替换占位符的本地覆盖清单时，才直接运行：

```powershell
kubectl apply -k deploy/k8s
```

### 5. 访问控制台

```powershell
kubectl port-forward -n taskpulse service/taskpulse 18080:8080
```

保持窗口运行并访问：

```text
http://127.0.0.1:18080/dashboard
http://127.0.0.1:18080/metrics
```

### 6. 查看日志

```powershell
kubectl logs -n taskpulse deployment/taskpulse --prefix --timestamps --since=10m
kubectl logs -n taskpulse deployment/llm-worker --prefix --timestamps --since=10m
kubectl get pods -n taskpulse -w
```

## 生产化缺口

当前清单没有解决公网控制面鉴权、TLS、外部托管 MySQL、备份恢复、跨可用区部署、镜像签名、NetworkPolicy、资源自动扩缩和集中日志。对外描述时应明确它是可靠性机制的本地 Kubernetes 验证环境。

