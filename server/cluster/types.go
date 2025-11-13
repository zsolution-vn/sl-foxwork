package cluster

// Internal contracts for the HA layer.
// These interfaces are used to compose a concrete implementation
// of einterfaces.ClusterInterface without leaking implementation details.

// Broadcaster represents a lightweight pub-sub used for small, ephemeral messages
// such as presence deltas, cache invalidation, and config version bumps.
type Broadcaster interface {
	Broadcast(topic string, payload []byte) error
	BroadcastTo(nodeID string, topic string, payload []byte) error
}

// MembershipObserver receives notifications about membership changes.
type MembershipObserver interface {
	OnJoin(nodeID string, addr string)
	OnLeave(nodeID string)
	OnUpdate(nodeID string, meta map[string]string)
}

// Cluster provides lifecycle and composition hooks for the HA subsystem.
type Cluster interface {
	// Start initializes gossip membership and leader election.
	Start() error
	// Stop gracefully leaves the cluster and stops leader election.
	Stop() error

	// IsLeader returns true if the current node holds leadership.
	IsLeader() bool
	// LeaderID returns the current leader node identity, if any.
	LeaderID() string

	// LocalNodeID returns the local node identity.
	LocalNodeID() string

	// HealthScore reports a lower-is-better score about local node health.
	HealthScore() int
}



