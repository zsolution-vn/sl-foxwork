# Kubernetes Lease Leader Election Implementation

## Overview

This implementation provides Kubernetes-native leader election using the Kubernetes Lease API (`coordination.k8s.io/v1`). This is more efficient than database-based leader election as it doesn't require database queries.

## Dependencies

To use this implementation, you need to add the following dependency to `server/go.mod`:

```bash
go get k8s.io/client-go@latest
```

Or add to `server/go.mod`:

```
require (
    k8s.io/client-go v0.30.0
)
```

Then run:

```bash
go mod tidy
```

## Required Kubernetes Permissions

The service account running Mattermost needs the following RBAC permissions:

- **Role**: `coordination.k8s.io/leases` resource with verbs: `get`, `create`, `update`, `patch`
- **Namespace**: The namespace where the lease resource is created

See `deploy/helm/ha/templates/lease-rbac.yaml` for the complete RBAC configuration.

## Configuration

The Kubernetes lease leader election is configured via Mattermost config:

- `ClusterSettings.LeaderElectionMode`: Set to `"k8s"`
- `ClusterSettings.LeaderElectionK8sNamespace`: Namespace where the lease is created
- `ClusterSettings.LeaderElectionK8sLeaseName`: Name of the lease resource

## How It Works

1. Each Mattermost instance attempts to acquire the lease
2. Only one instance can hold the lease at a time
3. The leader periodically renews the lease
4. If the leader fails to renew, another instance can acquire it
5. The lease uses Kubernetes native coordination API, avoiding database overhead

## Implementation Details

- Uses `k8s.io/client-go/tools/leaderelection` for robust leader election
- Automatically detects in-cluster config (when running in Kubernetes)
- Falls back to kubeconfig for local development
- Implements proper error handling and logging
- Thread-safe with atomic operations for leader status

## Migration from DB Lease

If you're currently using database-based leader election (`LeaderElectionMode: "db"`), you can switch to Kubernetes lease by:

1. Adding the `k8s.io/client-go` dependency
2. Setting `LeaderElectionMode: "k8s"` in config
3. Configuring `LeaderElectionK8sNamespace` and `LeaderElectionK8sLeaseName`
4. Ensuring proper RBAC permissions are in place

The implementation will automatically handle the transition.

