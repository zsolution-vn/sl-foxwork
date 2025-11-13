package gossip

import "github.com/hashicorp/memberlist"

// simpleBroadcast implements memberlist.Broadcast.
type simpleBroadcast struct {
	msg    []byte
	notify chan<- struct{}
}

func (b *simpleBroadcast) Invalidates(other memberlist.Broadcast) bool {
	return false
}

func (b *simpleBroadcast) Message() []byte {
	return b.msg
}

func (b *simpleBroadcast) Finished() {
	if b.notify != nil {
		select {
		case b.notify <- struct{}{}:
		default:
		}
	}
}



