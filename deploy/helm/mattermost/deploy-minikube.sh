#!/bin/bash

set -e

echo "🚀 Deploying Mattermost Clustering on Minikube..."

# Check if minikube is running
if ! minikube status > /dev/null 2>&1; then
    echo "❌ Minikube is not running. Starting minikube..."
    minikube start --memory=4096 --cpus=4 --disk-size=20g
fi

# Enable required addons
echo "📦 Enabling required minikube addons..."
minikube addons enable ingress || true
minikube addons enable metrics-server || true

# Set kubectl context
kubectl config use-context minikube

# Create namespace
echo "📁 Creating namespace..."
kubectl create namespace mattermost --dry-run=client -o yaml | kubectl apply -f -

# Update helm dependencies
echo "📥 Updating Helm dependencies..."
helm dependency update

# Deploy Mattermost
echo "🔧 Deploying Mattermost..."
helm upgrade --install mattermost . \
    --namespace mattermost \
    --set postgresql.auth.postgresPassword=mostest \
    --set postgresql.auth.password=mostest \
    --wait --timeout=10m

# Wait for pods to be ready
echo "⏳ Waiting for pods to be ready..."
kubectl wait --for=condition=ready pod \
    -l app.kubernetes.io/name=mattermost \
    -n mattermost \
    --timeout=300s || true

# Show status
echo ""
echo "✅ Deployment complete!"
echo ""
echo "📊 Pod Status:"
kubectl get pods -n mattermost
echo ""
echo "🌐 Services:"
kubectl get svc -n mattermost
echo ""
echo "🔍 Lease Status:"
kubectl get lease -n mattermost 2>/dev/null || echo "   No leases found (may need to wait for pods to start)"
echo ""
echo "💡 To access Mattermost:"
echo "   kubectl port-forward -n mattermost svc/mattermost 8065:8065"
echo "   Then open: http://localhost:8065"
echo ""
echo "📝 To view logs:"
echo "   kubectl logs -n mattermost -l app.kubernetes.io/name=mattermost -f"
echo ""
echo "🔍 To debug lease:"
echo "   ./debug-lease.sh"
echo ""
echo "🧪 To test lease:"
echo "   ./test-lease.sh"

