package gossip

import (
	"encoding/binary"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/hashicorp/memberlist"
)

// Observer receives membership callbacks.
type Observer interface {
	OnJoin(node *memberlist.Node)
	OnLeave(node *memberlist.Node)
	OnUpdate(node *memberlist.Node)
}

// MessageHandler is invoked when a broadcast is received.
type MessageHandler func(topic string, payload []byte)

// MemberlistGossip provides a thin wrapper over hashicorp/memberlist for LAN deployments.
type MemberlistGossip struct {
	opts       Options
	list       *memberlist.Memberlist
	broadcast  *memberlist.TransmitLimitedQueue
	handlers   map[string]MessageHandler
	observers  []Observer
	handlerMu  sync.RWMutex
	observerMu sync.RWMutex
}

// Options control the behaviour of the memberlist cluster.
type Options struct {
	Name             string
	BindAddr         string
	AdvertiseAddr    string
	BindPort         int
	AdvertisePort    int
	SecretKey        []byte
	ProbeInterval    time.Duration
	ProbeTimeout     time.Duration
	PushPull         time.Duration
	GossipInterval   time.Duration
	RetransmitMult   int
	NodeMeta         func(limit int) []byte
	LocalState       func(join bool) []byte
	MergeRemoteState func(buf []byte, join bool)
	Logger           *log.Logger
}

const (
	maxTopicLength     = 256
	defaultTopicLength = 2
)

var (
	ErrUnknownNode  = errors.New("gossip: unknown node")
	ErrInvalidTopic = errors.New("gossip: invalid topic")
)

// New creates a gossip cluster using memberlist.
func New(opts Options) (*MemberlistGossip, error) {
	conf := memberlist.DefaultLANConfig()
	conf.Name = opts.Name

	if opts.BindAddr != "" {
		conf.BindAddr = opts.BindAddr
	}
	if opts.AdvertiseAddr != "" {
		conf.AdvertiseAddr = opts.AdvertiseAddr
	}
	if opts.BindPort != 0 {
		conf.BindPort = opts.BindPort
	}
	if opts.AdvertisePort != 0 {
		conf.AdvertisePort = opts.AdvertisePort
	} else if opts.BindPort != 0 {
		conf.AdvertisePort = opts.BindPort
	}
	if len(opts.SecretKey) > 0 {
		conf.SecretKey = opts.SecretKey
	}
	if opts.ProbeInterval > 0 {
		conf.ProbeInterval = opts.ProbeInterval
	}
	if opts.ProbeTimeout > 0 {
		conf.ProbeTimeout = opts.ProbeTimeout
	}
	if opts.PushPull > 0 {
		conf.PushPullInterval = opts.PushPull
	}
	if opts.GossipInterval > 0 {
		conf.GossipInterval = opts.GossipInterval
	}
	if opts.Logger != nil {
		conf.Logger = opts.Logger
	}

	g := &MemberlistGossip{
		opts:     opts,
		handlers: make(map[string]MessageHandler),
	}

	delegate := &memberlistDelegate{gossip: g}
	conf.Delegate = delegate
	conf.Events = &eventDelegate{gossip: g}

	ml, err := memberlist.Create(conf)
	if err != nil {
		return nil, err
	}

	g.list = ml
	retransmit := opts.RetransmitMult
	if retransmit <= 0 {
		retransmit = 3
	}
	g.broadcast = &memberlist.TransmitLimitedQueue{
		NumNodes:       ml.NumMembers,
		RetransmitMult: retransmit,
	}

	return g, nil
}

// Join joins the provided peers.
func (g *MemberlistGossip) Join(peers []string) (int, error) {
	if len(peers) == 0 {
		return 0, nil
	}
	return g.list.Join(peers)
}

// Leave asks the node to leave the cluster.
func (g *MemberlistGossip) Leave(timeout time.Duration) error {
	return g.list.Leave(timeout)
}

// Shutdown stops memberlist.
func (g *MemberlistGossip) Shutdown() error {
	return g.list.Shutdown()
}

// Members returns the currently known nodes.
func (g *MemberlistGossip) Members() []*memberlist.Node {
	return g.list.Members()
}

// Self returns the local node definition.
func (g *MemberlistGossip) Self() *memberlist.Node {
	return g.list.LocalNode()
}

// Broadcast publishes a message to the cluster.
func (g *MemberlistGossip) Broadcast(topic string, payload []byte) error {
	msg, err := encodeMessage(topic, payload)
	if err != nil {
		return err
	}

	if g.broadcast == nil {
		return errors.New("gossip: broadcast queue not initialised")
	}

	g.broadcast.QueueBroadcast(&simpleBroadcast{msg: msg})
	return nil
}

// BroadcastTo sends a reliable direct message to a specific node.
func (g *MemberlistGossip) BroadcastTo(nodeName, topic string, payload []byte) error {
	msg, err := encodeMessage(topic, payload)
	if err != nil {
		return err
	}

	target := g.findNodeByName(nodeName)
	if target == nil {
		return fmt.Errorf("%w: %s", ErrUnknownNode, nodeName)
	}

	return g.list.SendReliable(target, msg)
}

// RegisterHandler registers a topic handler.
func (g *MemberlistGossip) RegisterHandler(topic string, handler MessageHandler) error {
	normalized := strings.TrimSpace(strings.ToLower(topic))
	if normalized == "" {
		return ErrInvalidTopic
	}

	g.handlerMu.Lock()
	defer g.handlerMu.Unlock()
	g.handlers[normalized] = handler
	return nil
}

// AddObserver registers a membership observer.
func (g *MemberlistGossip) AddObserver(observer Observer) {
	if observer == nil {
		return
	}
	g.observerMu.Lock()
	g.observers = append(g.observers, observer)
	g.observerMu.Unlock()
}

func (g *MemberlistGossip) dispatchMessage(topic string, payload []byte) {
	g.handlerMu.RLock()
	handler, ok := g.handlers[strings.ToLower(topic)]
	g.handlerMu.RUnlock()
	if !ok || handler == nil {
		return
	}
	handler(topic, payload)
}

func (g *MemberlistGossip) notifyJoin(node *memberlist.Node) {
	g.observerMu.RLock()
	observers := append([]Observer(nil), g.observers...)
	g.observerMu.RUnlock()
	for _, observer := range observers {
		observer.OnJoin(node)
	}
}

func (g *MemberlistGossip) notifyLeave(node *memberlist.Node) {
	g.observerMu.RLock()
	observers := append([]Observer(nil), g.observers...)
	g.observerMu.RUnlock()
	for _, observer := range observers {
		observer.OnLeave(node)
	}
}

func (g *MemberlistGossip) notifyUpdate(node *memberlist.Node) {
	g.observerMu.RLock()
	observers := append([]Observer(nil), g.observers...)
	g.observerMu.RUnlock()
	for _, observer := range observers {
		observer.OnUpdate(node)
	}
}

func (g *MemberlistGossip) findNodeByName(name string) *memberlist.Node {
	for _, node := range g.list.Members() {
		if node.Name == name {
			return node
		}
	}
	return nil
}

func encodeMessage(topic string, payload []byte) ([]byte, error) {
	topic = strings.TrimSpace(topic)
	if topic == "" {
		return nil, ErrInvalidTopic
	}
	if len(topic) > maxTopicLength {
		return nil, fmt.Errorf("%w: topic too long (%d > %d)", ErrInvalidTopic, len(topic), maxTopicLength)
	}

	header := make([]byte, defaultTopicLength)
	binary.BigEndian.PutUint16(header, uint16(len(topic)))

	buf := make([]byte, len(header)+len(topic)+len(payload))
	copy(buf, header)
	copy(buf[len(header):len(header)+len(topic)], topic)
	copy(buf[len(header)+len(topic):], payload)
	return buf, nil
}

func decodeMessage(msg []byte) (string, []byte, error) {
	if len(msg) < defaultTopicLength {
		return "", nil, errors.New("gossip: message too short")
	}
	topicLen := int(binary.BigEndian.Uint16(msg[:defaultTopicLength]))
	if topicLen <= 0 || topicLen > len(msg)-defaultTopicLength {
		return "", nil, errors.New("gossip: invalid topic length")
	}
	topic := string(msg[defaultTopicLength : defaultTopicLength+topicLen])
	payload := msg[defaultTopicLength+topicLen:]
	return topic, payload, nil
}

type memberlistDelegate struct {
	gossip *MemberlistGossip
}

func (d *memberlistDelegate) NodeMeta(limit int) []byte {
	if d.gossip.opts.NodeMeta != nil {
		return d.gossip.opts.NodeMeta(limit)
	}
	return nil
}

func (d *memberlistDelegate) NotifyMsg(msg []byte) {
	topic, payload, err := decodeMessage(msg)
	if err != nil {
		return
	}
	d.gossip.dispatchMessage(topic, payload)
}

func (d *memberlistDelegate) GetBroadcasts(overhead, limit int) [][]byte {
	if d.gossip.broadcast == nil {
		return nil
	}
	return d.gossip.broadcast.GetBroadcasts(overhead, limit)
}

func (d *memberlistDelegate) LocalState(join bool) []byte {
	if d.gossip.opts.LocalState != nil {
		return d.gossip.opts.LocalState(join)
	}
	return nil
}

func (d *memberlistDelegate) MergeRemoteState(buf []byte, join bool) {
	if d.gossip.opts.MergeRemoteState != nil {
		d.gossip.opts.MergeRemoteState(buf, join)
	}
}

type eventDelegate struct {
	gossip *MemberlistGossip
}

func (e *eventDelegate) NotifyJoin(node *memberlist.Node) {
	e.gossip.notifyJoin(node)
}

func (e *eventDelegate) NotifyLeave(node *memberlist.Node) {
	e.gossip.notifyLeave(node)
}

func (e *eventDelegate) NotifyUpdate(node *memberlist.Node) {
	e.gossip.notifyUpdate(node)
}
