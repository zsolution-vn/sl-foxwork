#!/bin/bash

set -e

NAMESPACE=${1:-mattermost}
LEASE_NAME=${2:-mattermost-ha}

echo "🔍 Debugging Kubernetes Lease Leader Election"
echo "=============================================="
echo ""

# Check if namespace exists
if ! kubectl get namespace "$NAMESPACE" > /dev/null 2>&1; then
    echo "❌ Namespace '$NAMESPACE' does not exist"
    exit 1
fi

echo "📋 1. Checking Lease Resource:"
echo "-------------------------------"
if kubectl get lease "$LEASE_NAME" -n "$NAMESPACE" > /dev/null 2>&1; then
    echo "✅ Lease exists:"
    kubectl get lease "$LEASE_NAME" -n "$NAMESPACE" -o yaml
    echo ""
    echo "📊 Lease Details:"
    kubectl describe lease "$LEASE_NAME" -n "$NAMESPACE"
else
    echo "❌ Lease '$LEASE_NAME' not found in namespace '$NAMESPACE'"
    echo ""
    echo "Available leases:"
    kubectl get leases -n "$NAMESPACE"
fi
echo ""

echo "📋 2. Checking RBAC (Role & RoleBinding):"
echo "-----------------------------------------"
kubectl get role,rolebinding -n "$NAMESPACE" | grep -E "(lease|NAME)" || echo "No lease RBAC found"
echo ""

echo "📋 3. Checking Service Account:"
echo "-------------------------------"
kubectl get serviceaccount -n "$NAMESPACE" | grep -E "(ha|lease|NAME)" || echo "No HA service account found"
echo ""

echo "📋 4. Checking Mattermost Pods:"
echo "--------------------------------"
kubectl get pods -n "$NAMESPACE" -l app.kubernetes.io/name=mattermost
echo ""

echo "📋 5. Checking Pod Logs for Lease Errors:"
echo "------------------------------------------"
for pod in $(kubectl get pods -n "$NAMESPACE" -l app.kubernetes.io/name=mattermost -o name); do
    echo ""
    echo "Pod: $pod"
    echo "---"
    kubectl logs -n "$NAMESPACE" "$pod" --tail=50 | grep -i -E "(lease|leader|k8s|election)" || echo "No lease-related logs found"
done
echo ""

echo "📋 6. Checking Lease Events:"
echo "-----------------------------"
kubectl get events -n "$NAMESPACE" --sort-by='.lastTimestamp' | grep -i lease | tail -10 || echo "No lease events found"
echo ""

echo "📋 7. Testing Lease Access (from a pod):"
echo "----------------------------------------"
FIRST_POD=$(kubectl get pods -n "$NAMESPACE" -l app.kubernetes.io/name=mattermost -o jsonpath='{.items[0].metadata.name}' 2>/dev/null)
if [ -n "$FIRST_POD" ]; then
    echo "Testing from pod: $FIRST_POD"
    echo "---"
    kubectl exec -n "$NAMESPACE" "$FIRST_POD" -- sh -c "
        if command -v curl > /dev/null 2>&1; then
            echo 'Testing Kubernetes API access...'
            curl -k -s https://kubernetes.default.svc/api/v1/namespaces/$NAMESPACE/leases 2>&1 | head -20 || echo 'API access test failed'
        else
            echo 'curl not available in pod'
        fi
    " || echo "Could not exec into pod"
else
    echo "No Mattermost pods found"
fi
echo ""

echo "📋 8. Checking Environment Variables:"
echo "-------------------------------------"
if [ -n "$FIRST_POD" ]; then
    kubectl exec -n "$NAMESPACE" "$FIRST_POD" -- env | grep -i -E "(LEASE|LEADER|K8S|NAMESPACE)" || echo "No lease-related env vars found"
fi
echo ""

echo "✅ Debug complete!"
echo ""
echo "💡 Tips:"
echo "   - Check pod logs: kubectl logs -n $NAMESPACE <pod-name> -f"
echo "   - Watch lease: kubectl get lease $LEASE_NAME -n $NAMESPACE -w"
echo "   - Describe lease: kubectl describe lease $LEASE_NAME -n $NAMESPACE"

