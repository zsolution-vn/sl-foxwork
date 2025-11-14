package ha

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/hashicorp/memberlist"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/shared/mlog"
	"github.com/mattermost/mattermost/server/public/shared/request"
	"github.com/mattermost/mattermost/server/v8/channels/app/platform"
	"github.com/mattermost/mattermost/server/v8/cluster/gossip"
	"github.com/mattermost/mattermost/server/v8/cluster/leader"
	"github.com/mattermost/mattermost/server/v8/einterfaces"
)

// Config contains HA configuration derived from Mattermost config.
type Config struct {
	Enabled bool

	// Leader election mode: "k8s" or "db"
	LeaderMode string

	// Gossip settings (to be used in subsequent implementations)
	BindAddress     string
	AdvertiseAddr   string
	BindPort        int
	SecretKey       []byte
	PushPullSeconds int
	GossipSeconds   int
	ProbeSeconds    int
	ProbeTimeoutMs  int

	// K8s Lease settings
	K8sNamespace  string
	K8sLeaseName  string
	LeaseDuration time.Duration
	RenewDeadline time.Duration
	RetryPeriod   time.Duration

	// DB Lease settings
	DBLeaseTable string
	LeaseTTL     time.Duration
	Heartbeat    time.Duration

	// Gossip seeds
	SeedPeers []string
}

// ConfigFromPlatform extracts HA config from the platform config.
func ConfigFromPlatform(ps *platform.PlatformService) Config {
	cfg := ps.Config()
	enabled := false
	if cfg.ClusterSettings.Enable != nil {
		enabled = *cfg.ClusterSettings.Enable
	}
	clusterName := ""
	if cfg.ClusterSettings.ClusterName != nil {
		clusterName = strings.TrimSpace(*cfg.ClusterSettings.ClusterName)
	}
	bindAddr := ""
	if cfg.ClusterSettings.BindAddress != nil {
		bindAddr = *cfg.ClusterSettings.BindAddress
	}
	advAddr := ""
	if cfg.ClusterSettings.AdvertiseAddress != nil {
		advAddr = strings.TrimSpace(*cfg.ClusterSettings.AdvertiseAddress)
	}
	if advAddr == "" || advAddr == "0.0.0.0" {
		if auto := autoAdvertiseAddress(bindAddr); auto != "" {
			ps.Log().Debug("Auto-detected advertise address", mlog.String("address", auto))
			advAddr = auto
		}
	}
	port := 0
	if cfg.ClusterSettings.GossipPort != nil {
		port = *cfg.ClusterSettings.GossipPort
	}

	leaderMode := "k8s"
	if cfg.ClusterSettings.LeaderElectionMode != nil {
		mode := strings.TrimSpace(strings.ToLower(*cfg.ClusterSettings.LeaderElectionMode))
		switch mode {
		case "k8s", "db":
			leaderMode = mode
		case "":
			// ignore empty
		default:
			ps.Log().Warn("Unsupported leader election mode, falling back to k8s", mlog.String("mode", mode))
		}
	}

	rawPeers := cfg.ClusterSettings.GossipPeerAddresses
	if len(rawPeers) == 1 {
		trimmed := strings.TrimSpace(rawPeers[0])
		if strings.HasPrefix(trimmed, "[") {
			var parsed []string
			if err := json.Unmarshal([]byte(trimmed), &parsed); err == nil {
				rawPeers = parsed
			}
		}
	}

	seedPeers := make([]string, 0, len(rawPeers))
	for _, peer := range rawPeers {
		p := strings.Trim(peer, " \t\"")
		p = strings.TrimPrefix(p, "[")
		p = strings.TrimSuffix(p, "]")
		if p == "" {
			continue
		}
		for _, candidate := range strings.Split(p, ",") {
			c := strings.Trim(candidate, " \t\"")
			if c != "" {
				seedPeers = append(seedPeers, c)
			}
		}
	}
	if len(seedPeers) == 0 {
		if auto := autoSeedPeers(port, clusterName); len(auto) > 0 {
			ps.Log().Info("Using auto-discovered gossip peers", mlog.String("peers", strings.Join(auto, ",")))
			seedPeers = append(seedPeers, auto...)
		}
	}
	seedPeers = dedupeStrings(seedPeers)

	// Extract K8s lease settings from environment variables (preferred) or config
	// Always read from env first, as config in DB may not have these values yet
	k8sNamespace := strings.TrimSpace(os.Getenv("MM_CLUSTERSETTINGS_LEADERELECTIONK8SNAMESPACE"))
	if k8sNamespace == "" {
		// Try POD_NAMESPACE env var
		k8sNamespace = strings.TrimSpace(os.Getenv("POD_NAMESPACE"))
	}
	if k8sNamespace == "" {
		// Read from service account token (standard Kubernetes way)
		if nsBytes, err := os.ReadFile("/var/run/secrets/kubernetes.io/serviceaccount/namespace"); err == nil {
			k8sNamespace = strings.TrimSpace(string(nsBytes))
		}
	}
	if k8sNamespace == "" && cfg.ClusterSettings.LeaderElectionK8sNamespace != nil {
		k8sNamespace = strings.TrimSpace(*cfg.ClusterSettings.LeaderElectionK8sNamespace)
	}

	k8sLeaseName := strings.TrimSpace(os.Getenv("MM_CLUSTERSETTINGS_LEADERELECTIONK8SLEASENAME"))
	if k8sLeaseName == "" && cfg.ClusterSettings.LeaderElectionK8sLeaseName != nil {
		k8sLeaseName = strings.TrimSpace(*cfg.ClusterSettings.LeaderElectionK8sLeaseName)
	}
	// Fallback to default if still empty
	if k8sLeaseName == "" {
		k8sLeaseName = "mattermost-ha"
	}

	// Log for debugging (use Info level to ensure it's visible)
	if ps != nil {
		ps.Log().Info("K8s lease config extracted",
			mlog.String("namespace", k8sNamespace),
			mlog.String("lease_name", k8sLeaseName),
			mlog.String("namespace_from_env", os.Getenv("MM_CLUSTERSETTINGS_LEADERELECTIONK8SNAMESPACE")),
			mlog.String("lease_from_env", os.Getenv("MM_CLUSTERSETTINGS_LEADERELECTIONK8SLEASENAME")),
			mlog.String("pod_namespace_env", os.Getenv("POD_NAMESPACE")),
		)
	}

	return Config{
		Enabled:       enabled,
		LeaderMode:    leaderMode,
		BindAddress:   bindAddr,
		AdvertiseAddr: advAddr,
		BindPort:      port,
		K8sNamespace:  k8sNamespace,
		K8sLeaseName:  k8sLeaseName,
		DBLeaseTable:  "cluster_leases",
		LeaseTTL:      20 * time.Second,
		Heartbeat:     5 * time.Second,
		LeaseDuration: 15 * time.Second,
		RenewDeadline: 10 * time.Second,
		RetryPeriod:   2 * time.Second,
		SeedPeers:     seedPeers,
	}
}

const (
	clusterMessageTopic = "cluster.message.v1"
)

type clusterEnvelope struct {
	Sender string                `json:"sender"`
	Target string                `json:"target,omitempty"`
	Msg    *model.ClusterMessage `json:"message"`
}

type clusterNodeMetadata struct {
	ID            string `json:"id"`
	Version       string `json:"version"`
	SchemaVersion string `json:"schema_version"`
	ConfigHash    string `json:"config_hash"`
	Hostname      string `json:"hostname"`
	IPAddress     string `json:"ip_address"`
}

func (m clusterNodeMetadata) toClusterInfo() *model.ClusterInfo {
	if m.ID == "" {
		return nil
	}
	return &model.ClusterInfo{
		Id:            m.ID,
		Version:       m.Version,
		SchemaVersion: m.SchemaVersion,
		ConfigHash:    m.ConfigHash,
		Hostname:      m.Hostname,
		IPAddress:     m.IPAddress,
	}
}

// Cluster is a minimal, build-safe skeleton that satisfies einterfaces.ClusterInterface.
// Subsequent edits will add memberlist gossip and pluggable leader election.
type Cluster struct {
	ps        *platform.PlatformService
	cfg       Config
	clusterID string

	isLeader atomic.Bool
	healthy  atomic.Int32

	// gossip membership (initialized when implementing)
	gossip     *gossip.MemberlistGossip
	joinMu     sync.Mutex
	joinCancel context.CancelFunc

	// leader election
	leaderK8s *leader.K8sLease
	leaderDB  *leader.DBLease

	handlerMu sync.RWMutex
	handlers  map[model.ClusterEvent]einterfaces.ClusterMessageHandler

	metaMu       sync.RWMutex
	nodeMetadata map[string]clusterNodeMetadata
}

func New(ps *platform.PlatformService, cfg Config) (*Cluster, error) {
	c := &Cluster{
		ps:           ps,
		cfg:          cfg,
		clusterID:    model.NewId(),
		handlers:     make(map[model.ClusterEvent]einterfaces.ClusterMessageHandler),
		nodeMetadata: map[string]clusterNodeMetadata{},
	}
	c.healthy.Store(0)
	return c, nil
}

// ---------- einterfaces.ClusterInterface (skeleton) ----------

func (c *Cluster) StartInterNodeCommunication() {
	c.ps.Log().Info("Cluster StartInterNodeCommunication")
	c.startGossip()

	switch c.cfg.LeaderMode {
	case "db":
		c.startDBLeader()
	default:
		c.startK8sLeader()
	}
}

func (c *Cluster) StopInterNodeCommunication() {
	c.ps.Log().Info("Cluster StopInterNodeCommunication")
	if c.leaderK8s != nil {
		c.leaderK8s.Stop()
	}
	if c.leaderDB != nil {
		c.leaderDB.Stop()
	}
	c.stopGossip()
}

func (c *Cluster) RegisterClusterMessageHandler(event model.ClusterEvent, crm einterfaces.ClusterMessageHandler) {
	if crm == nil {
		return
	}
	c.handlerMu.Lock()
	c.handlers[event] = crm
	c.handlerMu.Unlock()
}

func (c *Cluster) GetClusterId() string {
	return c.clusterID
}

func (c *Cluster) IsLeader() bool {
	return c.isLeader.Load()
}

func (c *Cluster) HealthScore() int {
	return int(c.healthy.Load())
}

func (c *Cluster) GetMyClusterInfo() *model.ClusterInfo {
	return &model.ClusterInfo{
		Id:         c.clusterID,
		Hostname:   "localhost",
		Version:    model.CurrentVersion,
		ConfigHash: c.ps.ClientConfigHash(),
	}
}

func (c *Cluster) GetClusterInfos() ([]*model.ClusterInfo, error) {
	result := make([]*model.ClusterInfo, 0)

	c.metaMu.RLock()
	for _, meta := range c.nodeMetadata {
		if info := meta.toClusterInfo(); info != nil {
			result = append(result, info)
		}
	}
	c.metaMu.RUnlock()

	if len(result) == 0 {
		result = append(result, c.GetMyClusterInfo())
	}

	sort.Slice(result, func(i, j int) bool {
		return result[i].Id < result[j].Id
	})

	return result, nil
}

func (c *Cluster) SendClusterMessage(msg *model.ClusterMessage) {
	if msg == nil {
		return
	}

	c.ps.Log().Debug("Sending cluster message", mlog.String("event", string(msg.Event)), mlog.String("send_type", msg.SendType))

	g := c.gossip
	if g == nil {
		c.ps.Log().Warn("Cluster message dropped; gossip not initialised", mlog.String("event", string(msg.Event)))
		return
	}

	sendType := msg.SendType
	if sendType == "" && msg.WaitForAllToSend {
		sendType = model.ClusterSendReliable
	}

	if msg.WaitForAllToSend {
		c.ps.Log().Debug("Cluster message requested wait for all to send; acknowledgements are not implemented", mlog.String("event", string(msg.Event)))
	}

	if sendType == model.ClusterSendReliable {
		c.sendReliable(msg)
		return
	}

	payload, err := c.encodeEnvelope("", msg)
	if err != nil {
		c.ps.Log().Warn("Failed to encode cluster message", mlog.String("event", string(msg.Event)), mlog.Err(err))
		return
	}

	if err := g.Broadcast(clusterMessageTopic, payload); err != nil {
		c.ps.Log().Warn("Failed to broadcast cluster message", mlog.String("event", string(msg.Event)), mlog.Err(err))
	}
}

func (c *Cluster) SendClusterMessageToNode(nodeID string, msg *model.ClusterMessage) error {
	if nodeID == "" || msg == nil {
		return errors.New("invalid parameters")
	}

	c.ps.Log().Debug("Sending cluster message to node", mlog.String("event", string(msg.Event)), mlog.String("target", nodeID))

	if nodeID == c.clusterID {
		c.dispatchClusterMessage(msg)
		return nil
	}

	g := c.gossip
	if g == nil {
		return errors.New("gossip not initialised")
	}

	payload, err := c.encodeEnvelope(nodeID, msg)
	if err != nil {
		return err
	}

	if err := g.BroadcastTo(nodeID, clusterMessageTopic, payload); err != nil {
		return err
	}

	return nil
}

func (c *Cluster) NotifyMsg(buf []byte) {
	if len(buf) == 0 {
		return
	}
	c.handleClusterMessage(clusterMessageTopic, buf)
}

func (c *Cluster) GetClusterStats(rctx request.CTX) ([]*model.ClusterStats, *model.AppError) {
	// Minimal stats; extend with full membership later.
	return []*model.ClusterStats{
		{
			Id:                        c.clusterID,
			TotalWebsocketConnections: c.ps.TotalWebsocketConnections(),
			TotalReadDbConnections:    0,
			TotalMasterDbConnections:  0,
		},
	}, nil
}

func (c *Cluster) GetLogs(rctx request.CTX, page, perPage int) ([]string, *model.AppError) {
	return []string{}, nil
}

func (c *Cluster) QueryLogs(rctx request.CTX, page, perPage int) (map[string][]string, *model.AppError) {
	return map[string][]string{
		c.clusterID: nil,
	}, nil
}

func (c *Cluster) GenerateSupportPacket(rctx request.CTX, options *model.SupportPacketOptions) (map[string][]model.FileData, error) {
	return map[string][]model.FileData{
		c.clusterID: nil,
	}, nil
}

func (c *Cluster) GetPluginStatuses() (model.PluginStatuses, *model.AppError) {
	return c.ps.GetPluginStatuses()
}

func (c *Cluster) ConfigChanged(previousConfig *model.Config, newConfig *model.Config, sendToOtherServer bool) *model.AppError {
	// No-op; will broadcast config bump later.
	return nil
}

func (c *Cluster) WebConnCountForUser(userID string) (int, *model.AppError) {
	return c.ps.WebConnCountForUser(userID), nil
}

func (c *Cluster) GetWSQueues(userID, connectionID string, seqNum int64) (map[string]*model.WSQueues, error) {
	queues, err := c.ps.GetWSQueues(userID, connectionID, seqNum)
	if err != nil {
		return nil, err
	}
	if queues == nil {
		return nil, nil
	}
	return map[string]*model.WSQueues{
		c.clusterID: queues,
	}, nil
}

func (c *Cluster) startDBLeader() {
	store := c.ps.Store
	if store == nil {
		c.ps.Log().Error("Cannot start DB leader election without store", mlog.String("mode", "db"))
		return
	}

	master := store.GetInternalMasterDB()
	if master == nil {
		c.ps.Log().Error("Cannot start DB leader election without master DB handle", mlog.String("mode", "db"))
		return
	}

	lease, err := leader.NewDBLease(master, c.ps.Log(), leader.DBLeaseOptions{
		Table:     c.cfg.DBLeaseTable,
		LeaseID:   "mattermost-ha",
		HolderID:  c.clusterID,
		TTL:       c.cfg.LeaseTTL,
		Heartbeat: c.cfg.Heartbeat,
		Retry:     c.cfg.RetryPeriod,
		OnStart: func(ctx context.Context) {
			c.onLeaderStart(ctx, "db")
		},
		OnStop: func() {
			c.onLeaderStop("db")
		},
	})
	if err != nil {
		c.ps.Log().Error("Failed to initialize DB leader election", mlog.Err(err))
		return
	}
	c.leaderDB = lease
	if err := c.leaderDB.Start(context.Background()); err != nil {
		c.ps.Log().Error("Failed to start DB leader election loop", mlog.Err(err))
	}
}

func (c *Cluster) startK8sLeader() {
	// Log config values for debugging (use Info to ensure visibility)
	c.ps.Log().Info("Starting K8s leader election",
		mlog.String("namespace", c.cfg.K8sNamespace),
		mlog.String("lease_name", c.cfg.K8sLeaseName),
		mlog.String("identity", c.clusterID),
	)

	if c.cfg.K8sNamespace == "" {
		c.ps.Log().Error("K8s namespace is empty, cannot start leader election")
		return
	}
	if c.cfg.K8sLeaseName == "" {
		c.ps.Log().Error("K8s lease name is empty, cannot start leader election")
		return
	}

	lease, err := leader.NewK8sLease(leader.K8sLeaseOptions{
		Namespace: c.cfg.K8sNamespace,
		LeaseName: c.cfg.K8sLeaseName,
		Identity:  c.clusterID,
		Duration:  c.cfg.LeaseDuration,
		Renew:     c.cfg.RenewDeadline,
		Retry:     c.cfg.RetryPeriod,
		OnStart: func(ctx context.Context) {
			c.onLeaderStart(ctx, "k8s")
		},
		OnStop: func() {
			c.onLeaderStop("k8s")
		},
		Logger: &mlogLoggerAdapter{logger: c.ps.Log()},
	})
	if err != nil {
		c.ps.Log().Error("Failed to create Kubernetes lease client", mlog.Err(err))
		return
	}
	c.leaderK8s = lease
	if err := c.leaderK8s.Start(context.Background()); err != nil {
		c.ps.Log().Error("Failed to start Kubernetes leader election", mlog.Err(err))
	}
}

// mlogLoggerAdapter adapts mlog.LoggerIFace to leader.Logger interface
type mlogLoggerAdapter struct {
	logger mlog.LoggerIFace
}

// convertKeyvals converts key-value pairs to mlog format
// Input: "key1", "value1", "key2", "value2"
// Output: mlog.String("key1", "value1"), mlog.String("key2", "value2")
func (a *mlogLoggerAdapter) convertKeyvals(keyvals ...interface{}) []mlog.Field {
	if len(keyvals) == 0 {
		return nil
	}

	fields := make([]mlog.Field, 0, len(keyvals)/2)
	for i := 0; i < len(keyvals)-1; i += 2 {
		key, ok := keyvals[i].(string)
		if !ok {
			continue
		}
		value := keyvals[i+1]
		// Convert value to string for mlog
		var strValue string
		switch v := value.(type) {
		case string:
			strValue = v
		case error:
			fields = append(fields, mlog.Err(v))
			continue
		default:
			strValue = fmt.Sprintf("%v", v)
		}
		fields = append(fields, mlog.String(key, strValue))
	}
	return fields
}

func (a *mlogLoggerAdapter) Info(msg string, keyvals ...interface{}) {
	fields := a.convertKeyvals(keyvals...)
	a.logger.Info(msg, fields...)
}

func (a *mlogLoggerAdapter) Warn(msg string, keyvals ...interface{}) {
	fields := a.convertKeyvals(keyvals...)
	a.logger.Warn(msg, fields...)
}

func (a *mlogLoggerAdapter) Error(msg string, keyvals ...interface{}) {
	fields := a.convertKeyvals(keyvals...)
	a.logger.Error(msg, fields...)
}

func (a *mlogLoggerAdapter) Debug(msg string, keyvals ...interface{}) {
	fields := a.convertKeyvals(keyvals...)
	a.logger.Debug(msg, fields...)
}

func (c *Cluster) onLeaderStart(_ context.Context, mode string) {
	c.isLeader.Store(true)
	c.ps.Log().Info("Leadership acquired", mlog.String("mode", mode))
}

func (c *Cluster) onLeaderStop(mode string) {
	c.isLeader.Store(false)
	c.ps.Log().Info("Leadership lost", mlog.String("mode", mode))
}

func (c *Cluster) startGossip() {
	if c.cfg.BindPort == 0 {
		c.ps.Log().Warn("Gossip bind port is zero; skipping gossip initialisation")
		return
	}

	opts := gossip.Options{
		Name:           c.clusterID,
		BindAddr:       firstNonEmpty(c.cfg.BindAddress, "0.0.0.0"),
		BindPort:       c.cfg.BindPort,
		AdvertisePort:  c.cfg.BindPort,
		GossipInterval: time.Second,
		ProbeInterval:  500 * time.Millisecond,
		ProbeTimeout:   2 * time.Second,
		NodeMeta:       c.nodeMetaBytes,
		Logger:         c.ps.Log().StdLogger(mlog.LvlDebug),
	}
	if c.cfg.AdvertiseAddr != "" {
		opts.AdvertiseAddr = c.cfg.AdvertiseAddr
	}

	g, err := gossip.New(opts)
	if err != nil {
		c.ps.Log().Error("Failed to initialise gossip", mlog.Err(err))
		return
	}

	if err := g.RegisterHandler(clusterMessageTopic, c.handleClusterMessage); err != nil {
		c.ps.Log().Error("Failed to register gossip handler", mlog.Err(err))
		_ = g.Shutdown()
		return
	}

	g.AddObserver(c)
	c.gossip = g
	c.setLocalMetadata()

	if len(c.cfg.SeedPeers) > 0 {
		if joined, err := c.gossip.Join(c.cfg.SeedPeers); err != nil {
			c.ps.Log().Warn(
				"Unable to join gossip peers, will retry",
				mlog.Err(err),
				mlog.String("peers", strings.Join(c.cfg.SeedPeers, ",")),
			)
			c.startJoinRetryLoop(c.cfg.SeedPeers)
		} else if joined > 0 {
			c.ps.Log().Info("Joined gossip peers", mlog.Int("joined", joined))
		}
	}
}

func (c *Cluster) stopGossip() {
	if c.gossip == nil {
		return
	}
	c.stopJoinRetryLoop()
	if err := c.gossip.Leave(2 * time.Second); err != nil {
		c.ps.Log().Debug("Gossip leave returned error", mlog.Err(err))
	}
	if err := c.gossip.Shutdown(); err != nil {
		c.ps.Log().Debug("Gossip shutdown returned error", mlog.Err(err))
	}
	c.gossip = nil

	c.metaMu.Lock()
	c.nodeMetadata = map[string]clusterNodeMetadata{}
	c.metaMu.Unlock()
}

func (c *Cluster) startJoinRetryLoop(peers []string) {
	if len(peers) == 0 {
		return
	}

	c.joinMu.Lock()
	if c.joinCancel != nil {
		c.joinCancel()
	}
	ctx, cancel := context.WithCancel(context.Background())
	c.joinCancel = cancel
	c.joinMu.Unlock()

	c.ps.Go(func() {
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				g := c.gossip
				if g == nil {
					continue
				}
				if joined, err := g.Join(peers); err != nil {
					c.ps.Log().Debug("Retrying gossip join failed", mlog.Err(err))
					continue
				} else if joined > 0 {
					c.ps.Log().Info("Successfully joined gossip peers after retry", mlog.Int("joined", joined))
					c.stopJoinRetryLoop()
					return
				}
			}
		}
	})
}

func (c *Cluster) stopJoinRetryLoop() {
	c.joinMu.Lock()
	defer c.joinMu.Unlock()
	if c.joinCancel != nil {
		c.joinCancel()
		c.joinCancel = nil
	}
}

func (c *Cluster) nodeMetaBytes(limit int) []byte {
	meta := c.buildLocalMetadata()
	data, err := json.Marshal(meta)
	if err != nil {
		c.ps.Log().Debug("Failed to marshal node metadata", mlog.Err(err))
		return nil
	}
	if limit > 0 && len(data) > limit {
		c.ps.Log().Warn("Node metadata exceeds limit; omitting metadata", mlog.Int("size", len(data)), mlog.Int("limit", limit))
		return nil
	}
	return data
}

func (c *Cluster) buildLocalMetadata() clusterNodeMetadata {
	schemaVersion := ""
	if store := c.ps.Store; store != nil {
		if v, err := store.GetDBSchemaVersion(); err == nil {
			schemaVersion = strconv.Itoa(v)
		}
	}

	configHash := ""
	if c.ps != nil {
		configHash = c.ps.ClientConfigHash()
	}

	hostname := firstNonEmpty(c.cfg.AdvertiseAddr, c.cfg.BindAddress)
	ip := firstNonEmpty(c.cfg.AdvertiseAddr, c.cfg.BindAddress)
	if c.gossip != nil {
		if self := c.gossip.Self(); self != nil {
			hostIP := self.Addr.String()
			if hostname == "" {
				hostname = hostIP
			}
			ip = net.JoinHostPort(hostIP, strconv.Itoa(int(self.Port)))
		}
	}

	return clusterNodeMetadata{
		ID:            c.clusterID,
		Version:       model.CurrentVersion,
		SchemaVersion: schemaVersion,
		ConfigHash:    configHash,
		Hostname:      hostname,
		IPAddress:     ip,
	}
}

func (c *Cluster) setLocalMetadata() {
	c.metaMu.Lock()
	c.nodeMetadata[c.clusterID] = c.buildLocalMetadata()
	c.metaMu.Unlock()
}

func (c *Cluster) sendReliable(msg *model.ClusterMessage) {
	g := c.gossip
	if g == nil {
		return
	}

	for _, node := range g.Members() {
		if node.Name == c.clusterID {
			continue
		}
		payload, err := c.encodeEnvelope(node.Name, msg)
		if err != nil {
			c.ps.Log().Warn("Failed to encode cluster message", mlog.String("event", string(msg.Event)), mlog.String("target", node.Name), mlog.Err(err))
			continue
		}
		if err := g.BroadcastTo(node.Name, clusterMessageTopic, payload); err != nil {
			c.ps.Log().Warn("Failed to send reliable cluster message", mlog.String("event", string(msg.Event)), mlog.String("target", node.Name), mlog.Err(err))
		}
	}
}

func (c *Cluster) encodeEnvelope(target string, msg *model.ClusterMessage) ([]byte, error) {
	env := clusterEnvelope{
		Sender: c.clusterID,
		Target: target,
		Msg:    msg,
	}
	return json.Marshal(&env)
}

func (c *Cluster) handleClusterMessage(_ string, payload []byte) {
	var env clusterEnvelope
	if err := json.Unmarshal(payload, &env); err != nil {
		c.ps.Log().Warn("Failed to decode cluster envelope", mlog.Err(err))
		return
	}

	if env.Sender == c.clusterID {
		return
	}
	if env.Target != "" && env.Target != c.clusterID {
		return
	}
	if env.Msg == nil {
		return
	}

	c.ps.Log().Debug("Received cluster message", mlog.String("event", string(env.Msg.Event)), mlog.String("sender", env.Sender), mlog.String("target", env.Target))

	c.dispatchClusterMessage(env.Msg)
}

func (c *Cluster) dispatchClusterMessage(msg *model.ClusterMessage) {
	c.handlerMu.RLock()
	handler := c.handlers[msg.Event]
	c.handlerMu.RUnlock()

	if handler == nil {
		c.ps.Log().Debug("No cluster handler registered", mlog.String("event", string(msg.Event)))
		return
	}

	handler(msg)
}

var _ gossip.Observer = (*Cluster)(nil)

func (c *Cluster) OnJoin(node *memberlist.Node) {
	if meta, err := parseNodeMetadata(node); err == nil {
		c.metaMu.Lock()
		c.nodeMetadata[node.Name] = meta
		c.metaMu.Unlock()
	}
	c.ps.Log().Debug("Gossip node joined", mlog.String("node", node.Name), mlog.String("addr", node.Addr.String()))
}

func (c *Cluster) OnLeave(node *memberlist.Node) {
	c.metaMu.Lock()
	delete(c.nodeMetadata, node.Name)
	c.metaMu.Unlock()
	c.ps.Log().Debug("Gossip node left", mlog.String("node", node.Name))
}

func (c *Cluster) OnUpdate(node *memberlist.Node) {
	if meta, err := parseNodeMetadata(node); err == nil {
		c.metaMu.Lock()
		c.nodeMetadata[node.Name] = meta
		c.metaMu.Unlock()
	}
	c.ps.Log().Debug("Gossip node updated", mlog.String("node", node.Name))
}

func parseNodeMetadata(node *memberlist.Node) (clusterNodeMetadata, error) {
	var meta clusterNodeMetadata
	if len(node.Meta) == 0 {
		meta.ID = node.Name
		meta.Hostname = node.Name
		meta.IPAddress = net.JoinHostPort(node.Addr.String(), strconv.Itoa(int(node.Port)))
		return meta, nil
	}
	if err := json.Unmarshal(node.Meta, &meta); err != nil {
		return meta, err
	}
	if meta.IPAddress == "" {
		meta.IPAddress = net.JoinHostPort(node.Addr.String(), strconv.Itoa(int(node.Port)))
	}
	if meta.Hostname == "" {
		meta.Hostname = node.Name
	}
	return meta, nil
}

func autoAdvertiseAddress(bindAddr string) string {
	bindAddr = strings.TrimSpace(bindAddr)
	if bindAddr != "" && bindAddr != "0.0.0.0" {
		return bindAddr
	}
	if auto := firstNonLoopbackIPv4(); auto != "" {
		return auto
	}
	return ""
}

func autoSeedPeers(port int, clusterName string) []string {
	if port <= 0 {
		return nil
	}

	candidates := []string{
		os.Getenv("MM_CLUSTER_DISCOVERY_SERVICE"),
		os.Getenv("SWARM_SERVICE_NAME"),
	}
	clusterName = strings.TrimSpace(clusterName)
	if clusterName != "" && (strings.Contains(clusterName, ".") || strings.Contains(clusterName, ":")) {
		candidates = append(candidates, clusterName)
	}

	// Kubernetes StatefulSet auto-discovery
	// Try to detect headless service from StatefulSet pod name pattern
	podName := os.Getenv("POD_NAME")
	if podName == "" {
		podName = os.Getenv("HOSTNAME")
	}
	if podName != "" {
		// Extract StatefulSet name from pod name (e.g., "mattermost-0" -> "mattermost")
		parts := strings.Split(podName, "-")
		if len(parts) >= 2 {
			// Try to get namespace
			namespace := os.Getenv("POD_NAMESPACE")
			if namespace == "" {
				// Try to read from service account
				if nsBytes, err := os.ReadFile("/var/run/secrets/kubernetes.io/serviceaccount/namespace"); err == nil {
					namespace = strings.TrimSpace(string(nsBytes))
				}
			}
			if namespace != "" {
				// Common headless service patterns for StatefulSet
				// Pattern: <statefulset-name>-headless.<namespace>.svc.cluster.local
				statefulSetName := strings.Join(parts[:len(parts)-1], "-")
				headlessService := fmt.Sprintf("%s-headless.%s.svc.cluster.local", statefulSetName, namespace)
				candidates = append(candidates, headlessService)
				// Also try without cluster.local suffix
				candidates = append(candidates, fmt.Sprintf("%s-headless.%s.svc", statefulSetName, namespace))
				candidates = append(candidates, fmt.Sprintf("%s-headless.%s", statefulSetName, namespace))
			}
		}
	}

	seeds := make([]string, 0, len(candidates)*2)
	isSwarm := os.Getenv("SWARM_SERVICE_NAME") != ""
	for _, name := range candidates {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		// Check if this is a Kubernetes service name (contains .svc)
		isK8sService := strings.Contains(name, ".svc") || strings.Contains(name, ".svc.cluster.local")

		// Docker Swarm pattern: use tasks. prefix only for Swarm services
		if isSwarm && !isK8sService {
			seeds = append(seeds, fmt.Sprintf("tasks.%s:%d", name, port))
		}
		// Always add the direct name:port
		seeds = append(seeds, fmt.Sprintf("%s:%d", name, port))
	}
	return dedupeStrings(seeds)
}

func dedupeStrings(values []string) []string {
	if len(values) == 0 {
		return values
	}
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, v := range values {
		v = strings.TrimSpace(v)
		if v == "" {
			continue
		}
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		result = append(result, v)
	}
	return result
}

func firstNonLoopbackIPv4() string {
	ifaces, err := net.Interfaces()
	if err != nil {
		return ""
	}
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 {
			continue
		}
		if iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			var ip net.IP
			switch v := addr.(type) {
			case *net.IPNet:
				ip = v.IP
			case *net.IPAddr:
				ip = v.IP
			}
			if ip == nil || ip.IsLoopback() {
				continue
			}
			ip = ip.To4()
			if ip == nil {
				continue
			}
			return ip.String()
		}
	}
	return ""
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
