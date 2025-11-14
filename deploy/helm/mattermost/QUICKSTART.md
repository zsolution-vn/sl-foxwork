# Quick Start: Testing Kubernetes Lease on Minikube

## Prerequisites

1. **Minikube** installed and running
2. **kubectl** configured
3. **Helm 3.x** installed
4. **Go dependencies** updated (k8s.io/client-go)

## Step 1: Update Go Dependencies

```bash
cd /home/nghia/DEV/sl-foxwork/server
go get k8s.io/client-go@latest
go mod tidy
```

## Step 2: Build Mattermost (Optional - if you modified code)

```bash
cd /home/nghia/DEV/sl-foxwork
make build
# Or if using Docker:
# docker build -t mattermost:test .
```

## Step 3: Deploy to Minikube

```bash
cd /home/nghia/DEV/sl-foxwork/deploy/helm/mattermost
./deploy-minikube.sh
```

This will:
- Start minikube if not running
- Enable required addons
- Deploy Mattermost with Kubernetes lease leader election
- Show deployment status

## Step 4: Debug Lease

After deployment, check lease status:

```bash
cd /home/nghia/DEV/sl-foxwork/deploy/helm/mattermost
./debug-lease.sh
```

This script will show:
- Lease resource status
- RBAC permissions
- Service account
- Pod logs related to lease
- Environment variables

## Step 5: Test Leader Election

Test that leader election is working:

```bash
cd /home/nghia/DEV/sl-foxwork/deploy/helm/mattermost
./test-lease.sh
```

This will monitor the lease for 20 seconds to see if leader election is stable.

## Manual Debugging Commands

### Check Lease Status
```bash
kubectl get lease mattermost-ha -n mattermost -o yaml
kubectl describe lease mattermost-ha -n mattermost
```

### Watch Lease Changes
```bash
kubectl get lease mattermost-ha -n mattermost -w
```

### Check Pod Logs
```bash
# All pods
kubectl logs -n mattermost -l app.kubernetes.io/name=mattermost -f

# Specific pod
kubectl logs -n mattermost mattermost-0 -f | grep -i lease
```

### Check RBAC
```bash
kubectl get role,rolebinding -n mattermost | grep lease
kubectl describe role mattermost-lease -n mattermost
```

### Check Service Account
```bash
kubectl get serviceaccount -n mattermost
kubectl describe serviceaccount mattermost-ha -n mattermost
```

### Check Environment Variables
```bash
kubectl exec -n mattermost mattermost-0 -- env | grep -i lease
```

### Test API Access from Pod
```bash
kubectl exec -n mattermost mattermost-0 -- sh -c 'curl -k https://kubernetes.default.svc/api/v1/namespaces/mattermost/leases'
```

## Common Issues

### Issue: Lease not found
**Solution**: Check if pods are running and RBAC is correct
```bash
kubectl get pods -n mattermost
kubectl get role,rolebinding -n mattermost
```

### Issue: Permission denied
**Solution**: Check service account and RBAC
```bash
kubectl describe role mattermost-lease -n mattermost
kubectl get rolebinding mattermost-lease -n mattermost -o yaml
```

### Issue: Cannot connect to Kubernetes API
**Solution**: Check if running in-cluster or need kubeconfig
```bash
kubectl exec -n mattermost mattermost-0 -- env | grep KUBERNETES
```

### Issue: Leader election not working
**Solution**: Check logs for errors
```bash
kubectl logs -n mattermost mattermost-0 | grep -i -E "(lease|leader|error)"
```

## Expected Behavior

1. **Lease Created**: A lease resource named `mattermost-ha` should exist in the namespace
2. **One Leader**: Only one pod should be the leader at any time
3. **Leader Rotation**: If leader pod is deleted, another pod should acquire leadership
4. **Logs**: Pods should log lease acquisition/loss events

## Cleanup

To remove everything:

```bash
helm uninstall mattermost -n mattermost
kubectl delete namespace mattermost
```

