package s1

func (g *Gateway) HoldMMEAssociation(addrs ...string) (release func()) {
	c := &mmeClient{addrs: addrs, done: make(chan struct{})}

	g.mu.Lock()
	g.stopping[c] = struct{}{}
	g.mu.Unlock()

	return func() {
		g.mu.Lock()
		delete(g.stopping, c)
		g.mu.Unlock()
		close(c.done)
	}
}
