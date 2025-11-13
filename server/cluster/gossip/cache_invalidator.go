package gossip

// CacheInvalidator publishes cache invalidation messages over gossip.
type CacheInvalidator struct {
	Broadcast func(topic string, payload []byte) error
}

// Invalidate sends an invalidation for a named cache and key (empty key means all).
func (c *CacheInvalidator) Invalidate(cacheName string, key string) error {
	payload := []byte(cacheName + "|" + key)
	return c.Broadcast("cache.v1", payload)
}



