package main

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"errors"
	"io"
	"log/slog"
	"math"
	"net"
	"sync"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/sqlstate"
)

// Shared cancellation identity is immutable. Statement-specific diagnostic
// context belongs on an owned copy, never on this cross-session cause.
var errUserCancel = &sqlstate.Error{Code: "57014", Message: "canceling statement due to user request"}
var errPeerWatchUnsupported = errors.New("passive peer shutdown monitoring unavailable on this platform")

type server struct {
	users     accounts
	databases map[string]*database
	logs      *serverLogs
	dummy     account
	ctx       context.Context
	cancel    context.CancelFunc
	listener  net.Listener
	// mu owns physical sockets, authenticated backend registrations and their
	// current-command cancellation functions. No engine or socket I/O under mu.
	mu       sync.Mutex
	nextID   uint32
	clients  map[*client]struct{}
	backends map[uint32]*client
	failure  error // mu; first visible runtime/logging failure
	workers  sync.WaitGroup
}
type client struct {
	server         *server
	socket         net.Conn
	wire           *wireCodec
	ctx            context.Context
	cancel         context.CancelFunc
	session        *session.Session
	db             *database
	database, user string
	id             uint32
	key            [32]byte
	keyLength      int
	cycle          *session.ProtocolCycle
	messages       <-chan frontendMessage
	sessionEnded   <-chan error
	protocolState  protocolState
	copyStream     protocolCopy             // Serial dispatcher only; never retained by engine.
	copyHooks      engine.CopyProtocolHooks // Bound methods cached once per physical connection.
	copyDrain      [8192]byte
	lastSettings   map[string]string
	commandCancel  context.CancelCauseFunc // server.mu
	readDone       chan struct{}
	freeBuffers    chan []byte
	readFailure    error // Reader-owned; published by closing messages/readDone.
	stopPeerWatch  func()
	sessionDone    chan struct{}
	fatalSent      bool          // Serial writer only; prevents duplicate terminal replies.
	capture        *queryCapture // Serial dispatcher only; nil unless query.log is enabled.
}

func newServer(ctx context.Context, users accounts, databases map[string]*database, logs *serverLogs) (*server, error) {
	ctx, cancel := context.WithCancel(ctx)
	s := &server{users: users, databases: databases, logs: logs, ctx: ctx, cancel: cancel, nextID: 1, clients: make(map[*client]struct{}), backends: make(map[uint32]*client)}
	s.dummy = account{salt: make([]byte, 16), key: make([]byte, 32)}
	if _, err := rand.Read(s.dummy.salt); err != nil {
		cancel()
		return nil, err
	}
	if _, err := rand.Read(s.dummy.key); err != nil {
		cancel()
		return nil, err
	}
	return s, nil
}
func (s *server) newClient(socket net.Conn) (*client, error) {
	var secret [32]byte
	if _, err := rand.Read(secret[:]); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ctx.Err() != nil {
		return nil, s.ctx.Err()
	}
	if s.nextID == math.MaxUint32 {
		return nil, errors.New("backend identity exhausted")
	}
	s.nextID++
	ctx, cancel := context.WithCancel(s.ctx)
	c := &client{server: s, socket: socket, wire: newWire(socket), ctx: ctx, cancel: cancel, id: s.nextID, key: secret, keyLength: 4, lastSettings: make(map[string]string)}
	c.copyStream.client = c
	c.copyHooks = engine.CopyProtocolHooks{Start: c.copyStream.start, FinishInput: c.copyStream.finishInput}
	s.clients[c] = struct{}{}
	s.workers.Add(1)
	return c, nil
}
func (s *server) register(c *client) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.clients[c]; !ok || s.ctx.Err() != nil {
		return errors.New("connection is closing")
	}
	s.backends[c.id] = c
	return nil
}
func (s *server) cancelCommand(id uint32, key []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if c := s.backends[id]; c != nil && len(key) == c.keyLength && subtle.ConstantTimeCompare(c.key[:c.keyLength], key) == 1 && c.commandCancel != nil {
		// context cancellation has no engine callback or external I/O. Holding mu
		// fences this exact registration against a later command replacing it.
		c.commandCancel(errUserCancel)
	}
}
func (c *client) beginCommand() (context.Context, func()) {
	ctx, cancel := context.WithCancelCause(c.ctx)
	c.server.mu.Lock()
	c.commandCancel = cancel
	c.server.mu.Unlock()
	return ctx, func() { c.server.mu.Lock(); c.commandCancel = nil; c.server.mu.Unlock(); cancel(nil) }
}

// startReader provides two owned reusable buffers: the executing message and
// at most one queued message. A passive transport watcher detects peer shutdown
// independently of this bounded queue without consuming frontend bytes.
func (c *client) startReader() <-chan frontendMessage {
	messages := make(chan frontendMessage, 1)
	c.freeBuffers = make(chan []byte, 2)
	c.freeBuffers <- nil
	c.freeBuffers <- nil
	c.readDone = make(chan struct{})
	go func() {
		defer close(c.readDone)
		defer close(messages)
		for {
			var buffer []byte
			select {
			case buffer = <-c.freeBuffers:
			case <-c.ctx.Done():
				return
			}
			message, err := c.wire.readMessage(buffer)
			if err != nil {
				if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, errInvalidFrame) {
					c.cancel()
					_ = c.socket.Close()
					return
				}
				// Only a recognizable unknown tag has a writable FATAL reply.
				c.readFailure = err
				c.cancel()
				return
			}
			select {
			case messages <- message:
			case <-c.ctx.Done():
				return
			}
		}
	}()
	return messages
}
func (c *client) releaseMessage(message frontendMessage) {
	select {
	case c.freeBuffers <- message.body:
	case <-c.ctx.Done():
	}
}

// Native session expiry owns its timers and teardown. Observe the end request,
// not cleanup completion: an active COPY lease must be interruptible before
// WaitEnd can finish. The serial writer still owns the canonical FATAL reply.
func (c *client) watchSessionEnd() <-chan error {
	events := make(chan error, 1)
	c.sessionDone = make(chan struct{})
	go func() {
		defer close(c.sessionDone)
		select {
		case <-c.session.EndRequested():
		case <-c.ctx.Done():
			// A held COPY codec may already have the user-cancel cause.
			// Publish physical retirement through the independent session
			// authority now, before waiting for its operation lease to drain.
			c.session.RequestEnd(session.InvalidConnection)
			return
		}
		if c.ctx.Err() != nil {
			return
		}
		fatal := *diagnostic(c.session.Check(c.ctx))
		fatal.Severity, fatal.SeverityUnlocalized = "FATAL", "FATAL"
		c.server.mu.Lock()
		if c.commandCancel != nil {
			c.commandCancel(&fatal)
		}
		c.server.mu.Unlock()
		select {
		case events <- &fatal:
		case <-c.ctx.Done():
		}
	}()
	return events
}
func (c *client) finish(reason session.EndReason) {
	if c.session != nil {
		// Preserve the chosen clean/fatal/init reason before canceling the
		// physical context observed by the existing end-request watcher.
		c.session.RequestEnd(reason)
	}
	c.cancel()
	_ = c.socket.Close()
	if c.stopPeerWatch != nil {
		c.stopPeerWatch()
	}
	if c.readDone != nil {
		<-c.readDone
	}
	if c.sessionDone != nil {
		<-c.sessionDone
	}
	c.server.mu.Lock()
	delete(c.server.backends, c.id)
	delete(c.server.clients, c)
	c.commandCancel = nil
	c.server.mu.Unlock()
	if c.session != nil {
		if err := c.session.WaitEnd(context.Background()); err != nil {
			c.server.log(slog.LevelError, "session cleanup failed", "connection_id", c.id, "database", c.database, "user", c.user, "error", err.Error())
		}
	}
	c.server.log(slog.LevelInfo, "connection closed", "connection_id", c.id, "database", c.database, "user", c.user)
	c.server.workers.Done()
}
func (s *server) stop() {
	s.cancel()
	if s.listener != nil {
		_ = s.listener.Close()
	}
	s.mu.Lock()
	sockets := make([]net.Conn, 0, len(s.clients))
	for c := range s.clients {
		sockets = append(sockets, c.socket)
	}
	s.mu.Unlock()
	for _, socket := range sockets {
		_ = socket.Close()
	}
}
func (s *server) shutdown() {
	s.stop()
	s.workers.Wait()
}
func (s *server) log(level slog.Level, message string, args ...any) {
	if err := s.logs.operational(context.Background(), level, message, args...); err != nil {
		// Required log failures terminate serving rather than silently discarding
		// records. The CLI reports the retained failure on orderly shutdown.
		s.fail(err)
	}
}
func (s *server) fail(err error) {
	s.mu.Lock()
	if s.failure == nil {
		s.failure = err
	}
	s.mu.Unlock()
	s.stop()
}
func (s *server) runtimeError() error { s.mu.Lock(); defer s.mu.Unlock(); return s.failure }
