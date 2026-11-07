// Package pgserver serves the embedded engine over the PostgreSQL wire protocol.
package pgserver

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/url"
	"os"
	"sync"
	"time"

	"stuntgres/internal/serverauth"
	"stuntgres/internal/serverref"
)

// Account grants one cleartext-password network identity access to databases.
// Passwords are hashed during Prepare and never retained by a running Server.
type Account struct {
	Username  string
	Password  string
	Databases []string
}

// Config describes an independent server. Empty Listen enables only DialFunc.
// Writers are caller-owned; nil LogWriter discards operational and SQL logs.
type Config struct {
	Listen           string
	UserFile         string
	Accounts         []Account
	DumpDir          string
	Restore          bool
	LogDir           string
	LogWriter        io.Writer
	QueryLog         bool
	QueryLogWriter   io.Writer
	Verbose          bool
	References       []string
	ReferenceTimeout time.Duration
}

type startupStage uint8

const (
	stageOpen startupStage = iota
	stagePreparing
	stagePrepared
	stageStarting
	stageRunning
	stageClosed
)

type terminalMode uint8

const (
	terminalNone terminalMode = iota
	terminalGraceful
	terminalAbort
)

// Server owns its transports, sessions, persistence worker and opened log files.
// Startup stages are sequential; all running lifecycle methods are concurrency-safe.
type Server struct {
	mu              sync.Mutex // state and request ownership only; never held over external I/O
	stage           startupStage
	terminal        terminalMode
	stoppedSelected bool // mu; commits the graceful stopped record after persistence
	cfg             Config
	parent          context.Context
	logs            *serverLogs
	users           serverauth.Accounts
	databases       map[string]*database
	core            *server
	addr            net.Addr
	done            chan struct{}
	result          error
	wake            chan struct{}
	dumps           []*dumpRequest
	activeCancel    context.CancelFunc
	acceptDone      chan struct{}
	stopWatch       chan struct{}
	watchDone       chan struct{}
}

// Open snapshots configuration and opens only logging destinations. Call Close on
// every returned Server, including one abandoned before Prepare or Start.
func Open(ctx context.Context, cfg Config) (*Server, error) {
	cfg.References = append([]string(nil), cfg.References...)
	if cfg.Accounts != nil {
		accounts := make([]Account, len(cfg.Accounts))
		copy(accounts, cfg.Accounts)
		for i := range accounts {
			accounts[i].Databases = append([]string(nil), accounts[i].Databases...)
		}
		cfg.Accounts = accounts
	}
	logs, err := openLogs(cfg)
	if err != nil {
		return nil, err
	}
	return &Server{cfg: cfg, parent: ctx, logs: logs, done: make(chan struct{}), wake: make(chan struct{}, 1)}, nil
}

// Prepare loads accounts, selects dumps, provisions engines, attaches references
// and restores, without opening a transport or starting a goroutine.
func (s *Server) Prepare() (err error) {
	s.mu.Lock()
	if s.stage != stageOpen {
		s.mu.Unlock()
		return errors.New("server Prepare requires an open server")
	}
	s.stage = stagePreparing
	s.mu.Unlock()
	defer func() {
		// Drop the owned plaintext copy even when preparation fails.
		s.cfg.Accounts = nil
		if err != nil {
			err = s.startupFailure(err)
		}
	}()
	if err = s.parent.Err(); err != nil {
		return err
	}
	if s.cfg.UserFile != "" && s.cfg.Accounts != nil {
		return errors.New("userfile and accounts are mutually exclusive")
	}
	if s.cfg.UserFile != "" {
		s.users, err = serverauth.Load(s.cfg.UserFile, true)
		if err != nil {
			return err
		}
	} else {
		s.users = make(serverauth.Accounts, len(s.cfg.Accounts))
		for _, input := range s.cfg.Accounts {
			if err = s.parent.Err(); err != nil {
				return err
			}
			if _, exists := s.users[input.Username]; exists {
				return fmt.Errorf("duplicate user %q", input.Username)
			}
			var a serverauth.Account
			a, err = serverauth.NewAccount(input.Username, input.Password, input.Databases)
			if err != nil {
				return err
			}
			s.users[a.Username] = a
		}
	}
	if s.cfg.Restore && s.cfg.DumpDir == "" {
		return errors.New("restore requires a dump directory")
	}
	latest := make(map[string]string)
	if s.cfg.DumpDir != "" {
		if err = os.MkdirAll(s.cfg.DumpDir, 0700); err != nil {
			return err
		}
		if s.cfg.Restore {
			latest, err = latestDumps(s.cfg.DumpDir)
			if err != nil {
				return err
			}
		}
	}
	restored := make([]string, 0, len(latest))
	for name := range latest {
		restored = append(restored, name)
	}
	s.databases, err = newDatabases(s.parent, s.users, restored)
	if err != nil {
		return err
	}
	references, err := serverref.ParseAll(s.cfg.References)
	if err != nil {
		return err
	}
	if len(references) > 0 && !s.cfg.QueryLog {
		return errors.New("reference requires querylog")
	}
	if s.cfg.ReferenceTimeout < 0 {
		return errors.New("reference timeout must be positive")
	}
	if s.cfg.ReferenceTimeout == 0 {
		s.cfg.ReferenceTimeout = serverref.DefaultTimeout
	}
	if err = attachReferences(s.databases, references); err != nil {
		return err
	}
	if s.cfg.Restore {
		if err = restoreDatabases(s.parent, s.databases, latest, s.logs); err != nil {
			return err
		}
	}
	if err = s.parent.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	s.stage = stagePrepared
	s.mu.Unlock()
	return nil
}

// Start opens configured transports and admits connections after startup records
// succeed. Use the package-level Start for ordinary embedded construction.
func (s *Server) Start() (err error) {
	s.mu.Lock()
	if s.stage != stagePrepared {
		s.mu.Unlock()
		return errors.New("server Start requires a prepared server")
	}
	s.stage = stageStarting
	s.mu.Unlock()
	defer func() {
		if err != nil {
			err = s.startupFailure(err)
		}
	}()
	if err = s.parent.Err(); err != nil {
		return err
	}
	// Parent cancellation is observed by the lifecycle watcher, so cleanup owns an
	// independent context and always performs cancel-all before session teardown.
	s.core, err = newServer(context.Background(), s.users, s.databases, s.logs)
	if err != nil {
		return err
	}
	s.core.referenceTimeout = s.cfg.ReferenceTimeout
	address := "in-memory"
	if s.cfg.Listen != "" {
		s.core.listener, err = net.Listen("tcp", s.cfg.Listen)
		if err != nil {
			return err
		}
		s.addr = s.core.listener.Addr()
		address = s.addr.String()
		if tcp, ok := s.addr.(*net.TCPAddr); ok && !tcp.IP.IsLoopback() {
			if err = s.logs.operational(context.Background(), slog.LevelWarn, "WARNING: non-loopback cleartext password authentication without TLS exposes passwords and data", "address", address); err != nil {
				return err
			}
		}
	}
	if err = s.logs.operational(context.Background(), slog.LevelInfo, "server started", "address", address, "databases", databaseNames(s.databases)); err != nil {
		return err
	}
	s.acceptDone = make(chan struct{})
	s.stopWatch = make(chan struct{})
	s.watchDone = make(chan struct{})
	s.mu.Lock()
	s.stage = stageRunning
	s.mu.Unlock()
	if s.core.listener != nil {
		go func() { defer close(s.acceptDone); _ = s.core.serve() }()
	} else {
		close(s.acceptDone)
	}
	go func() {
		defer close(s.watchDone)
		select {
		case <-s.parent.Done():
			s.selectTerminal(terminalGraceful)
		case <-s.core.ctx.Done():
			s.selectTerminal(terminalGraceful)
		case <-s.stopWatch:
		}
	}()
	go s.lifecycle()
	return nil
}

// Start constructs and starts one Server with failure cleanup.
func Start(ctx context.Context, cfg Config) (*Server, error) {
	s, err := Open(ctx, cfg)
	if err != nil {
		return nil, err
	}
	if err = s.Prepare(); err != nil {
		return nil, err
	}
	if err = s.Start(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Server) startupFailure(cause error) error {
	if s.core != nil {
		s.core.shutdown()
	}
	result := errors.Join(cause, s.logs.Close())
	s.mu.Lock()
	s.cfg.Accounts = nil
	s.stage, s.result = stageClosed, result
	close(s.done)
	s.mu.Unlock()
	return result
}

// Log writes an operational record, retaining required runtime write failures.
func (s *Server) Log(ctx context.Context, level slog.Level, message string, args ...any) error {
	err := s.logs.operational(ctx, level, message, args...)
	if err != nil {
		s.mu.Lock()
		core := s.core
		running := s.stage == stageRunning
		s.mu.Unlock()
		if failure := s.logs.failureError(); running && failure != nil {
			core.fail(failure)
		}
	}
	return err
}

// Addr is the bound TCP address, or nil for in-memory-only operation.
func (s *Server) Addr() net.Addr { s.mu.Lock(); defer s.mu.Unlock(); return s.addr }

// DSN returns an escaped PostgreSQL URL for the actual bound TCP address.
func (s *Server) DSN(user, password, database string) (string, error) {
	address := s.Addr()
	if address == nil {
		return "", errors.New("server has no bound TCP address")
	}
	// RawPath preserves a database containing slash as one URL path component.
	path := "/" + database
	u := url.URL{Scheme: "postgres", Host: address.String(), User: url.UserPassword(user, password), Path: path, RawPath: "/" + url.PathEscape(database)}
	u.RawQuery = "sslmode=disable"
	return u.String(), nil
}

// Done closes after all owned work and log closure have finished.
func (s *Server) Done() <-chan struct{} { return s.done }

// Err is nil until Done closes, then the stable joined finalization result.
func (s *Server) Err() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stage != stageClosed {
		return nil
	}
	return s.result
}
