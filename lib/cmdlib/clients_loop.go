package cmdlib

import "sync"

type clientsLoop struct {
	clients   []*Client
	clientIdx int
	mu        sync.Mutex
}

func (c *clientsLoop) NextClient() *Client {
	c.mu.Lock()
	oldIdx := c.clientIdx
	c.clientIdx++
	if c.clientIdx == len(c.clients) {
		c.clientIdx = 0
	}
	c.mu.Unlock()
	return c.clients[oldIdx]
}
