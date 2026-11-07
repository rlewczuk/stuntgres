package fixture

import (
	"context"
	"net"
	"net/url"
	"testing"
	"time"

	"stuntgres/pgserver"
)

// EmbeddedServerBackend describes a real wire endpoint. DialFunc is nil for
// PostgreSQL; native clients can choose the same server's pipe or TCP endpoint.
type EmbeddedServerBackend struct {
	Name     string
	DSN      string
	DialFunc func(context.Context, string, string) (net.Conn, error)
}

// EmbeddedServerFixture is opt-in and never builds or starts a CLI subprocess.
// Clients must register cleanup after construction so they close before its server.
type EmbeddedServerFixture struct {
	Ctx      context.Context
	Server   *pgserver.Server
	backends []EmbeddedServerBackend
}

func NewEmbeddedServerFixture(t *testing.T, cfg pgserver.Config, user, password, database string) *EmbeddedServerFixture {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), ScenarioBudget)
	f := &EmbeddedServerFixture{Ctx: ctx}
	var resetReference func()
	t.Cleanup(func() {
		if f.Server != nil {
			cleanup, stop := context.WithTimeout(context.Background(), 15*time.Second)
			err := f.Server.Shutdown(cleanup)
			stop()
			if err != nil {
				t.Errorf("embedded shutdown: %v", err)
			}
			if err := f.Server.Close(); err != nil {
				t.Errorf("embedded close: %v", err)
			}
		}
		cancel()
		if resetReference != nil {
			resetReference()
		}
	})
	if *Mode {
		if err := EnsureStarted(); err != nil {
			t.Fatal(err)
		}
		resetReference = func() {
			cleanup, stop := context.WithTimeout(context.Background(), 15*time.Second)
			defer stop()
			if _, err := Admin.ExecContext(cleanup, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
				t.Errorf("embedded oracle reset: %v", err)
			}
		}
		resetReference()
		u, err := url.Parse(DSN)
		if err != nil {
			t.Fatal(err)
		}
		u.Path = "/" + database
		// Ordinary scenarios use the fixture's existing PostgreSQL credentials.
		f.backends = append(f.backends, EmbeddedServerBackend{Name: "postgres", DSN: u.String()})
	}
	var err error
	f.Server, err = pgserver.Start(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	dsn := (&url.URL{Scheme: "postgres", User: url.UserPassword(user, password), Host: "127.0.0.1:5432", Path: "/" + database, RawQuery: "sslmode=disable"}).String()
	if f.Server.Addr() != nil {
		dsn, err = f.Server.DSN(user, password, database)
		if err != nil {
			t.Fatal(err)
		}
	}
	f.backends = append([]EmbeddedServerBackend{{Name: "embedded", DSN: dsn, DialFunc: f.Server.DialFunc()}}, f.backends...)
	return f
}

func (f *EmbeddedServerFixture) Backends() []EmbeddedServerBackend {
	return append([]EmbeddedServerBackend(nil), f.backends...)
}
