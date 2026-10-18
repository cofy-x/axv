package host

import (
	"net"
	"sync"
)

// Track closes as well as hijacks: net/http emits no StateClosed for a WebSocket.
type connectionSet struct {
	mu          sync.Mutex
	connections map[*connection]bool
	closed      bool
}

type connection struct {
	net.Conn
	owner *connectionSet
}

func (c *connection) Close() error {
	c.owner.mu.Lock()
	delete(c.owner.connections, c)
	c.owner.mu.Unlock()
	return c.Conn.Close()
}

type trackedListener struct {
	net.Listener
	owner *connectionSet
}

func (l *trackedListener) Accept() (net.Conn, error) {
	c, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}
	l.owner.mu.Lock()
	defer l.owner.mu.Unlock()
	if l.owner.closed {
		c.Close()
		return nil, net.ErrClosed
	}
	tracked := &connection{Conn: c, owner: l.owner}
	l.owner.connections[tracked] = true
	return tracked, nil
}

func (s *connectionSet) close() {
	s.mu.Lock()
	s.closed = true
	active := make([]*connection, 0, len(s.connections))
	for c := range s.connections {
		active = append(active, c)
	}
	s.mu.Unlock()
	for _, c := range active {
		c.Close()
	}
}
