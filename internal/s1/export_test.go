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

func (g *Gateway) FailMMESends(err error) {
	g.relay.mu.Lock()
	g.relay.sendMME = func(*MMEAssociation, []byte, uint16) error { return err }
	g.relay.mu.Unlock()
}
