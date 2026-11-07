package pgserver

import (
	"context"
	"net"
	"sync"

	"stuntgres/internal/engine/session"
)

type pipeEndpoint struct {
	net.Conn
	closed     chan struct{}
	peerClosed <-chan struct{}
	once       sync.Once
	closeErr   error
}

func newPipe() (*pipeEndpoint, *pipeEndpoint) {
	a, b := net.Pipe()
	ac, bc := make(chan struct{}), make(chan struct{})
	return &pipeEndpoint{Conn: a, closed: ac, peerClosed: bc}, &pipeEndpoint{Conn: b, closed: bc, peerClosed: ac}
}
func (p *pipeEndpoint) Close() error {
	p.once.Do(func() { p.closeErr = p.Conn.Close(); close(p.closed) })
	return p.closeErr
}

// DialFunc connects to this Server without TCP. Network/address are deliberately
// ignored, including the pipe RemoteAddr used by pgconn's second cancel dial.
// A successful connection outlives the dialing context.
func (s *Server) DialFunc() func(context.Context, string, string) (net.Conn, error) {
	return func(ctx context.Context, _, _ string) (net.Conn, error) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		s.mu.Lock()
		if s.stage != stageRunning || s.terminal != terminalNone {
			s.mu.Unlock()
			return nil, net.ErrClosed
		}
		core := s.core
		s.mu.Unlock()
		local, remote := newPipe()
		c, err := core.newClient(remote)
		if err != nil {
			_ = local.Close()
			_ = remote.Close()
			return nil, err
		}
		if err := ctx.Err(); err != nil {
			_ = local.Close()
			c.finish(session.InvalidConnection)
			return nil, err
		}
		go c.run()
		return local, nil
	}
}

func watchPeerShutdown(socket net.Conn, onClose func(), onError func(error)) (func(), error) {
	if pipe, ok := socket.(*pipeEndpoint); ok {
		stop, done := make(chan struct{}), make(chan struct{})
		go func() {
			defer close(done)
			select {
			case <-pipe.peerClosed:
				onClose()
			case <-stop:
			}
		}()
		var once sync.Once
		return func() { once.Do(func() { close(stop) }); <-done }, nil
	}
	return watchSocketPeerShutdown(socket, onClose, onError)
}
