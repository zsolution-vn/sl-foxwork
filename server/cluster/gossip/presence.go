package gossip

import "context"

// PresenceBroadcaster publishes lightweight presence deltas over gossip.
type PresenceBroadcaster struct {
	Broadcast func(topic string, payload []byte) error
}

func (p *PresenceBroadcaster) Publish(ctx context.Context, userID string, status string) error {
	// payload format: userID|status (will be replaced by compact TLV/JSON)
	data := []byte(userID + "|" + status)
	return p.Broadcast("presence.v1", data)
}



