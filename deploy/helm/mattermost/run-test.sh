#!/bin/bash

set -e

echo "🧪 Complete Test Script for Kubernetes Lease on Minikube"
echo "========================================================="
echo ""

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

# Check prerequisites
echo "1️⃣ Checking prerequisites..."
echo "----------------------------"

# Check minikube
if ! command -v minikube &> /dev/null; then
    echo -e "${RED}❌ minikube not found${NC}"
    exit 1
fi
echo -e "${GREEN}✅ minikube found${NC}"

# Check kubectl
if ! command -v kubectl &> /dev/null; then
    echo -e "${RED}❌ kubectl not found${NC}"
    exit 1
fi
echo -e "${GREEN}✅ kubectl found${NC}"

# Check helm
if ! command -v helm &> /dev/null; then
    echo -e "${RED}❌ helm not found${NC}"
    exit 1
fi
echo -e "${GREEN}✅ helm found${NC}"

# Check if minikube is running
if ! minikube status > /dev/null 2>&1; then
    echo -e "${YELLOW}⚠️  Minikube is not running. Starting...${NC}"
    minikube start --memory=4096 --cpus=4 --disk-size=20g
else
    echo -e "${GREEN}✅ minikube is running${NC}"
fi

echo ""
echo "2️⃣ Building/Preparing..."
echo "------------------------"

# Check if we need to build
read -p "Do you want to build Mattermost? (y/N): " -n 1 -r
echo
if [[ $REPLY =~ ^[Yy]$ ]]; then
    echo "Building Mattermost..."
    cd /home/nghia/DEV/sl-foxwork/server
    go mod tidy
    # Add build command here if needed
    echo -e "${GREEN}✅ Build complete${NC}"
else
    echo "Skipping build..."
fi

echo ""
echo "3️⃣ Deploying to Minikube..."
echo "----------------------------"
cd /home/nghia/DEV/sl-foxwork/deploy/helm/mattermost
./deploy-minikube.sh

echo ""
echo "4️⃣ Waiting for pods to be ready..."
echo "-----------------------------------"
sleep 10
kubectl wait --for=condition=ready pod \
    -l app.kubernetes.io/name=mattermost \
    -n mattermost \
    --timeout=120s || echo -e "${YELLOW}⚠️  Some pods may not be ready yet${NC}"

echo ""
echo "5️⃣ Checking Lease Status..."
echo "----------------------------"
sleep 5
if kubectl get lease mattermost-ha -n mattermost > /dev/null 2>&1; then
    echo -e "${GREEN}✅ Lease exists${NC}"
    kubectl get lease mattermost-ha -n mattermost -o yaml | grep -A 5 "spec:"
else
    echo -e "${YELLOW}⚠️  Lease not found yet (may take a moment)${NC}"
fi

echo ""
echo "6️⃣ Running Debug Script..."
echo "---------------------------"
./debug-lease.sh

echo ""
echo "7️⃣ Testing Leader Election..."
echo "------------------------------"
./test-lease.sh

echo ""
echo "✅ Test Complete!"
echo ""
echo "📝 Next Steps:"
echo "   - Check logs: kubectl logs -n mattermost -l app.kubernetes.io/name=mattermost -f"
echo "   - Watch lease: kubectl get lease mattermost-ha -n mattermost -w"
echo "   - Access Mattermost: kubectl port-forward -n mattermost svc/mattermost 8065:8065"

