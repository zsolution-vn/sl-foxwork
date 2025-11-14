# Mattermost Clustering trên Minikube

Helm chart để triển khai Mattermost với clustering mode trên Kubernetes/Minikube.

## Yêu cầu

- Minikube đã được cài đặt và chạy
- kubectl đã được cấu hình để kết nối với minikube
- Helm 3.x đã được cài đặt

## Cài đặt Minikube (nếu chưa có)

```bash
# Cài đặt minikube
curl -LO https://storage.googleapis.com/minikube/releases/latest/minikube-linux-amd64
sudo install minikube-linux-amd64 /usr/local/bin/minikube

# Khởi động minikube với đủ tài nguyên
minikube start --memory=4096 --cpus=4 --disk-size=20g

# Bật addons cần thiết
minikube addons enable ingress
minikube addons enable metrics-server
```

## Triển khai Mattermost Clustering

### 1. Cài đặt dependencies

```bash
cd deploy/helm/mattermost
helm dependency update
```

### 2. Triển khai với giá trị mặc định

```bash
helm install mattermost . --namespace mattermost --create-namespace
```

### 3. Triển khai với custom values

Tạo file `custom-values.yaml`:

```yaml
replicaCount: 3

mattermost:
  config:
    ServiceSettings:
      SiteURL: "http://mattermost.local"

postgresql:
  auth:
    postgresPassword: "your-secure-password"
    password: "your-secure-password"
```

Sau đó triển khai:

```bash
helm install mattermost . -f custom-values.yaml --namespace mattermost --create-namespace
```

## Kiểm tra deployment

### Xem trạng thái pods

```bash
kubectl get pods -n mattermost
```

Bạn sẽ thấy:
- 3 pods Mattermost (mattermost-0, mattermost-1, mattermost-2)
- 1 pod PostgreSQL

### Xem logs của Mattermost

```bash
# Xem logs của pod đầu tiên
kubectl logs -n mattermost mattermost-0

# Xem logs của tất cả pods
kubectl logs -n mattermost -l app.kubernetes.io/name=mattermost
```

### Kiểm tra clustering status

```bash
# Exec vào một pod
kubectl exec -it -n mattermost mattermost-0 -- sh

# Trong pod, kiểm tra cluster status
# Mattermost sẽ tự động log thông tin về cluster khi khởi động
```

## Truy cập Mattermost

### Port-forward (cho testing)

```bash
kubectl port-forward -n mattermost svc/mattermost 8065:8065
```

Sau đó truy cập: http://localhost:8065

### Sử dụng Ingress (production)

1. Cập nhật `values.yaml`:

```yaml
ingress:
  enabled: true
  className: "nginx"
  hosts:
    - host: mattermost.local
      paths:
        - path: /
          pathType: Prefix
```

2. Thêm vào `/etc/hosts`:

```
$(minikube ip) mattermost.local
```

3. Upgrade deployment:

```bash
helm upgrade mattermost . -f values.yaml -n mattermost
```

## Cấu hình Clustering

### Các tham số quan trọng

- **replicaCount**: Số lượng Mattermost pods (tối thiểu 2 cho HA)
- **ClusterSettings.Enable**: Bật/tắt clustering
- **ClusterSettings.ClusterName**: Tên cluster
- **ClusterSettings.GossipPort**: Port cho gossip protocol (mặc định 7946)
- **ClusterSettings.LeaderElectionMode**: Chế độ leader election (k8s hoặc db)

### Gossip Protocol

Mattermost sử dụng gossip protocol để giao tiếp giữa các nodes. Các pods tự động discover nhau thông qua:
- Headless service: `mattermost-headless`
- DNS: `mattermost-{index}.mattermost-headless.namespace.svc.cluster.local`

### Leader Election

Chart sử dụng Kubernetes leader election (lease) thay vì database election để tối ưu hiệu suất.

## Scaling

### Tăng số replicas

```bash
# Scale lên 5 pods
helm upgrade mattermost . --set replicaCount=5 -n mattermost
```

Lưu ý: Khi scale, gossip peers sẽ tự động được cập nhật trong ConfigMap.

### Auto-scaling (HPA)

Bật HPA trong `values.yaml`:

```yaml
autoscaling:
  enabled: true
  minReplicas: 3
  maxReplicas: 10
  targetCPUUtilizationPercentage: 80
```

## Troubleshooting

### Pods không start

```bash
# Kiểm tra events
kubectl describe pod -n mattermost mattermost-0

# Kiểm tra logs
kubectl logs -n mattermost mattermost-0
```

### Clustering không hoạt động

1. Kiểm tra gossip port đã được expose:

```bash
kubectl get svc -n mattermost mattermost-headless
```

2. Kiểm tra network policies (nếu có)

3. Kiểm tra logs để xem gossip connection:

```bash
kubectl logs -n mattermost mattermost-0 | grep -i gossip
```

### Database connection issues

```bash
# Kiểm tra PostgreSQL pod
kubectl get pods -n mattermost | grep postgres

# Kiểm tra PostgreSQL logs
kubectl logs -n mattermost mattermost-postgres-0

# Test connection từ Mattermost pod
kubectl exec -it -n mattermost mattermost-0 -- \
  sh -c 'echo "SELECT 1;" | psql postgres://mmuser:mostest@mattermost-postgres:5432/mattermost'
```

## Storage

### Persistent Volumes

Chart tự động tạo PVC cho:
- Mattermost data: `/mattermost/data`
- PostgreSQL data: `/var/lib/postgresql/data`

### Backup

```bash
# Backup PostgreSQL
kubectl exec -n mattermost mattermost-postgres-0 -- \
  pg_dump -U mmuser mattermost > backup.sql

# Backup Mattermost files
kubectl exec -n mattermost mattermost-0 -- \
  tar czf - /mattermost/data > mattermost-data-backup.tar.gz
```

## Uninstall

```bash
helm uninstall mattermost -n mattermost

# Xóa namespace (cẩn thận, sẽ xóa tất cả data)
kubectl delete namespace mattermost
```

## Tài liệu tham khảo

- [Mattermost High Availability](https://docs.mattermost.com/scale/high-availability-cluster.html)
- [Mattermost Kubernetes Deployment](https://docs.mattermost.com/install/kubernetes-cluster.html)

