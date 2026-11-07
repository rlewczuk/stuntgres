package reference_test

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"stuntgres/pgserver"
	"stuntgres/reference/fixture"
)

// Each sink belongs to one server. Snapshotting may overlap its log worker.
type embeddedBuffer struct {
	mu sync.Mutex
	bytes.Buffer
	closed bool
}

func (b *embeddedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.Buffer.Write(p)
}
func (b *embeddedBuffer) String() string { b.mu.Lock(); defer b.mu.Unlock(); return b.Buffer.String() }
func (b *embeddedBuffer) Close() error   { b.mu.Lock(); defer b.mu.Unlock(); b.closed = true; return nil }

func embeddedStart(t *testing.T, ctx context.Context, cfg pgserver.Config) *pgserver.Server {
	t.Helper()
	s, err := pgserver.Start(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Errorf("close: %v", err)
		}
	})
	return s
}
func embeddedEndpoint(t *testing.T, s *pgserver.Server, user, password, database string) fixture.EmbeddedServerBackend {
	t.Helper()
	dsn := (&url.URL{Scheme: "postgres", User: url.UserPassword(user, password), Host: "127.0.0.1:5432", Path: "/" + database, RawQuery: "sslmode=disable"}).String()
	if s.Addr() != nil {
		var err error
		dsn, err = s.DSN(user, password, database)
		if err != nil {
			t.Fatal(err)
		}
	}
	return fixture.EmbeddedServerBackend{Name: "embedded", DSN: dsn, DialFunc: s.DialFunc()}
}
func embeddedShutdown(t *testing.T, s *pgserver.Server) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), fixture.Liveness)
	defer cancel()
	if err := s.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case <-s.Done():
	default:
		t.Fatal("Shutdown returned before Done")
	}
	if err := s.Err(); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
}

func embeddedWrongCancel(t *testing.T, ctx context.Context, s *pgserver.Server, victim *pgx.Conn) {
	t.Helper()
	c, err := s.DialFunc()(ctx, "not-tcp", "not-an-address")
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if deadline, ok := ctx.Deadline(); ok {
		c.SetDeadline(deadline)
	}
	key := victim.PgConn().SecretKey()
	packet := binary.BigEndian.AppendUint32(nil, uint32(12+len(key)))
	packet = binary.BigEndian.AppendUint32(packet, 80877102)
	packet = binary.BigEndian.AppendUint32(packet, victim.PgConn().PID())
	packet = append(packet, key...)
	if _, err := c.Write(packet); err != nil {
		t.Fatal(err)
	}
	var one [1]byte
	if _, err := c.Read(one[:]); err != io.EOF {
		t.Fatalf("wrong-server cancel EOF: %v", err)
	}
}

func embeddedIsolationData(t *testing.T, ctx context.Context, c *pgx.Conn, want int) {
	t.Helper()
	embeddedExec(t, ctx, c, fmt.Sprintf("CREATE TABLE embedded_isolation(v integer); INSERT INTO embedded_isolation VALUES (%d)", want))
	embeddedInt(t, ctx, c, "SELECT v FROM embedded_isolation", want)
}

func TestReferenceEmbeddedServerIsolation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), fixture.ScenarioBudget)
	defer cancel()
	var logs [2]embeddedBuffer
	var servers [2]*pgserver.Server
	var clients [2]*pgx.Conn
	for i, user := range []string{"embedded_a", "embedded_b"} {
		cfg := pgserver.Config{Listen: "127.0.0.1:0", Accounts: []pgserver.Account{{Username: user, Password: "isolated-password", Databases: []string{"app"}}}, LogWriter: &logs[i], QueryLog: true, QueryLogWriter: &logs[i]}
		servers[i] = embeddedStart(t, ctx, cfg)
		clients[i] = embeddedConnect(t, ctx, embeddedEndpoint(t, servers[i], user, "isolated-password", "app"), true)
		// These are the first physical admissions on each independent allocator.
		if clients[i].PgConn().PID() != 2 {
			t.Fatalf("first server %d PID=%d", i, clients[i].PgConn().PID())
		}
		embeddedInt(t, ctx, clients[i], "SELECT pg_backend_pid()", 2)
		embeddedIsolationData(t, ctx, clients[i], 11*(i+1))
	}
	for i, other := range []string{"embedded_b", "embedded_a"} {
		endpoint := embeddedEndpoint(t, servers[i], other, "isolated-password", "app")
		cfg, err := pgx.ParseConfig(endpoint.DSN)
		if err != nil {
			t.Fatal(err)
		}
		cfg.DialFunc = endpoint.DialFunc
		c, err := pgx.ConnectConfig(ctx, cfg)
		if c != nil {
			c.Close(ctx)
		}
		fixture.AssertDiagnostic(t, err, &fixture.Diagnostic{Severity: "FATAL", SeverityUnlocalized: "FATAL", Code: "28P01", Message: fmt.Sprintf("password authentication failed for user %q", other)})
		embeddedInt(t, ctx, clients[i], "SELECT v FROM embedded_isolation", 11*(i+1))
	}
	observer := embeddedConnect(t, ctx, embeddedEndpoint(t, servers[1], "embedded_b", "isolated-password", "app"), true)
	result := embeddedSleep(t, ctx, clients[1])
	embeddedWaitActivity(t, ctx, observer, clients[1].PgConn().PID(), "SELECT pg_sleep(600)", "PgSleep", "Timeout")
	embeddedWrongCancel(t, ctx, servers[0], clients[1])
	// Completion of the wrong request and renewed positive ownership prove its
	// ineffectiveness without waiting for an absence timeout.
	embeddedWaitActivity(t, ctx, observer, clients[1].PgConn().PID(), "SELECT pg_sleep(600)", "PgSleep", "Timeout")
	if err := clients[1].PgConn().CancelRequest(ctx); err != nil {
		t.Fatal(err)
	}
	embeddedDiagnostic(t, embeddedJoin(t, ctx, result), "57014", "canceling statement due to user request")
	embeddedShutdown(t, servers[0])
	embeddedInt(t, ctx, clients[1], "SELECT 42", 42)
	embeddedInt(t, ctx, clients[1], "SELECT v FROM embedded_isolation", 22)
	embeddedShutdown(t, servers[1])
	for i := range logs {
		text := logs[i].String()
		own := fmt.Sprintf("INSERT INTO embedded_isolation VALUES (%d)", 11*(i+1))
		other := fmt.Sprintf("INSERT INTO embedded_isolation VALUES (%d)", 11*(2-i))
		if !strings.Contains(text, own) || strings.Contains(text, other) {
			t.Fatalf("server %d log isolation: %s", i, text)
		}
		if logs[i].closed {
			t.Fatal("server closed caller writer")
		}
	}
	if *fixture.Mode {
		t.Run("postgres_independent_databases", func(t *testing.T) {
			if err := fixture.EnsureStarted(); err != nil {
				t.Fatal(err)
			}
			for i, name := range []string{"embedded_isolation_a", "embedded_isolation_b"} {
				name := name
				if _, err := fixture.Admin.ExecContext(ctx, "CREATE DATABASE "+name+" OWNER stuntgres"); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() {
					cleanup, stop := context.WithTimeout(context.Background(), 15*time.Second)
					defer stop()
					if _, err := fixture.Admin.ExecContext(cleanup, "DROP DATABASE "+name+" WITH (FORCE)"); err != nil {
						t.Error(err)
					}
				})
				u, err := url.Parse(fixture.DSN)
				if err != nil {
					t.Fatal(err)
				}
				u.Path = "/" + name
				c := embeddedConnect(t, ctx, fixture.EmbeddedServerBackend{DSN: u.String()}, false)
				embeddedIsolationData(t, ctx, c, 11*(i+1))
				clients[i] = c
			}
			for i, c := range clients {
				embeddedInt(t, ctx, c, "SELECT v FROM embedded_isolation", 11*(i+1))
			}
		})
	}
}
