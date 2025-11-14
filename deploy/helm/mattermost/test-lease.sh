#!/bin/bash

set -e

NAMESPACE=${1:-mattermost}
LEASE_NAME=${2:-mattermost-ha}

echo "🧪 Testing Kubernetes Lease Leader Election"
echo "============================================="
echo ""

# Function to check if lease exists and get holder
check_lease() {
    local holder=$(kubectl get lease "$LEASE_NAME" -n "$NAMESPACE" -o jsonpath='{.spec.holderIdentity}' 2>/dev/null || echo "")
    local renew_time=$(kubectl get lease "$LEASE_NAME" -n "$NAMESPACE" -o jsonpath='{.spec.renewTime}' 2>/dev/null || echo "")
    
    if [ -z "$holder" ]; then
        echo "❌ Lease not found or no holder"
        return 1
    fi
    
    echo "✅ Lease Holder: $holder"
    if [ -n "$renew_time" ]; then
        echo "   Last Renewed: $renew_time"
    fi
    return 0
}

# Function to watch lease changes
watch_lease() {
    echo "👀 Watching lease for 30 seconds..."
    timeout 30 kubectl get lease "$LEASE_NAME" -n "$NAMESPACE" -w 2>/dev/null || true
}

# Function to test leader election
test_leader_election() {
    echo ""
    echo "🔄 Testing Leader Election (checking every 2 seconds for 20 seconds)..."
    echo "---"
    
    for i in {1..10}; do
        echo "[$i/10] $(date +%H:%M:%S)"
        check_lease
        echo ""
        sleep 2
    done
}

# Main
echo "1️⃣ Checking initial lease state..."
check_lease
echo ""

echo "2️⃣ Getting all pods..."
kubectl get pods -n "$NAMESPACE" -l app.kubernetes.io/name=mattermost -o wide
echo ""

echo "3️⃣ Testing leader election stability..."
test_leader_election

echo "4️⃣ Final lease state:"
check_lease
echo ""

echo "✅ Test complete!"

