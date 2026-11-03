package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"net/url"
	"sync"
	"testing"
	"time"

	"github.com/lib/pq"
	"stuntgres/reference/fixture"
)

// pq performs real authentication and normal wire execution. The public Dialer
// seam retains only the physical transport for genuine EOF/protocol witnesses.
type statDatabaseWireDialer struct {
	mu     sync.Mutex
	socket net.Conn
}

func (d *statDatabaseWireDialer) keep(c net.Conn, err error) (net.Conn, error) {
	if err == nil {
		d.mu.Lock()
		d.socket = c
		d.mu.Unlock()
	}
	return c, err
}
func (d *statDatabaseWireDialer) Dial(network, address string) (net.Conn, error) {
	return d.keep(net.Dial(network, address))
}
func (d *statDatabaseWireDialer) DialTimeout(network, address string, timeout time.Duration) (net.Conn, error) {
	return d.keep(net.DialTimeout(network, address, timeout))
}
func (d *statDatabaseWireDialer) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	var dialer net.Dialer
	return d.keep(dialer.DialContext(ctx, network, address))
}
func (d *statDatabaseWireDialer) transport() net.Conn {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.socket
}
func statDatabaseWireClient(t *testing.T, f *fixture.ServerFixture, b fixture.Backend) (*sql.Conn, *sql.DB, net.Conn) {
	t.Helper()
	u, err := url.Parse(f.DSNFor(b))
	if err != nil {
		t.Fatal(err)
	}
	u.Path = "/" + fixture.ServerSecondDatabase
	u.RawPath = ""
	connector, err := pq.NewConnector(u.String())
	if err != nil {
		t.Fatal(err)
	}
	dialer := &statDatabaseWireDialer{}
	connector.Dialer(dialer)
	pool := sql.OpenDB(connector)
	t.Cleanup(func() { pool.Close() })
	c, err := pool.Conn(f.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	socket := dialer.transport()
	if socket == nil {
		t.Fatal("missing real transport")
	}
	return c, pool, socket
}

type statDatabaseWireVector struct{ sessions, abandoned, fatal, killed, backends int64 }

func statDatabaseWireIdentity(t *testing.T, ctx context.Context, c *sql.Conn) {
	t.Helper()
	var name string
	var catalogOID, statisticsOID int64
	err := c.QueryRowContext(ctx, "SELECT current_database(),d.oid::bigint,s.datid::bigint FROM pg_catalog.pg_database d JOIN pg_catalog.pg_stat_database s ON s.datname=d.datname WHERE d.datname=current_database()").Scan(&name, &catalogOID, &statisticsOID)
	if err != nil || name != fixture.ServerSecondDatabase || catalogOID <= 0 || statisticsOID != catalogOID {
		t.Fatal("actual wire database attachment/catalog/statistics identity", name, catalogOID, statisticsOID, err)
	}
}

func statDatabaseWireRead(t *testing.T, ctx context.Context, c *sql.Conn) statDatabaseWireVector {
	t.Helper()
	var v statDatabaseWireVector
	if err := c.QueryRowContext(ctx, "SELECT sessions,sessions_abandoned,sessions_fatal,sessions_killed,numbackends FROM pg_stat_database WHERE datname=$1", fixture.ServerSecondDatabase).Scan(&v.sessions, &v.abandoned, &v.fatal, &v.killed, &v.backends); err != nil {
		t.Fatal(err)
	}
	return v
}
func statDatabaseWireAwait(t *testing.T, f *fixture.ServerFixture, c *sql.Conn, want statDatabaseWireVector) {
	t.Helper()
	ctx, cancel := context.WithTimeout(f.Ctx, fixture.Liveness)
	defer cancel()
	tick := time.NewTicker(5 * time.Millisecond)
	defer tick.Stop()
	for {
		got := statDatabaseWireRead(t, ctx, c)
		if got == want {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatal("wire terminal publication", got, want, ctx.Err())
		case <-tick.C:
		}
	}
}
func statDatabaseWireTerminate(t *testing.T, socket net.Conn) {
	t.Helper()
	// Keep the transport open until the server acknowledges Terminate with
	// EOF. An immediate client FIN can otherwise win before X is decoded.
	if err := socket.SetReadDeadline(time.Now().Add(fixture.Liveness)); err != nil {
		t.Fatal(err)
	}
	if _, err := socket.Write([]byte{'X', 0, 0, 0, 4}); err != nil {
		t.Fatal(err)
	}
	var one [1]byte
	if n, err := socket.Read(one[:]); n != 0 || !errors.Is(err, io.EOF) {
		t.Fatal("normal Terminate terminal EOF", n, err)
	}
}
func statDatabaseWireFatal(t *testing.T, socket net.Conn, code, message string) {
	t.Helper()
	if err := socket.SetReadDeadline(time.Now().Add(fixture.Liveness)); err != nil {
		t.Fatal(err)
	}
	var header [5]byte
	if _, err := io.ReadFull(socket, header[:]); err != nil {
		t.Fatal(err)
	}
	n := binary.BigEndian.Uint32(header[1:])
	if header[0] != 'E' || n < 4 || n > 1<<20 {
		t.Fatal("terminal ErrorResponse header", header)
	}
	body := make([]byte, int(n)-4)
	if _, err := io.ReadFull(socket, body); err != nil {
		t.Fatal(err)
	}
	fields := map[byte]string{}
	for len(body) > 1 && body[0] != 0 {
		end := bytes.IndexByte(body[1:], 0)
		if end < 0 {
			t.Fatal("unterminated error field")
		}
		fields[body[0]] = string(body[1 : end+1])
		body = body[end+2:]
	}
	if fields['S'] != "FATAL" || fields['V'] != "FATAL" || fields['C'] != code || fields['M'] != message {
		t.Fatal("terminal diagnostic", fields)
	}
	var one [1]byte
	if n, err := socket.Read(one[:]); n != 0 || !errors.Is(err, io.EOF) {
		t.Fatal("terminal EOF", n, err)
	}
}
func TestReferenceStatDatabaseWire(t *testing.T) {
	f := fixture.NewServerFixture(t)
	for _, b := range f.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			// Managed server databases own independent engines/catalogs. Observe
			// the target engine, not an unrelated primary engine's inventory.
			observer, observerPool, _ := statDatabaseWireClient(t, f, b)
			defer observerPool.Close()
			defer observer.Close()
			statDatabaseWireIdentity(t, f.Ctx, observer)
			if _, err := observer.ExecContext(f.Ctx, "SET stats_fetch_consistency=none"); err != nil {
				t.Fatal(err)
			}
			// Settle a real setup client outside measured wire deltas. The
			// baseline includes the observer and native provisioning client.
			setup, setupPool, setupSocket := statDatabaseWireClient(t, f, b)
			statDatabaseWireIdentity(t, f.Ctx, setup)
			statDatabaseWireTerminate(t, setupSocket)
			if err := setup.Raw(func(any) error { return driver.ErrBadConn }); !errors.Is(err, driver.ErrBadConn) {
				t.Fatal(err)
			}
			setup.Close()
			setupPool.Close()
			setupSessions := int64(2) // Observer plus the retired setup client.
			if !b.PG {
				// database.provision uses one genuine public bootstrap session.
				setupSessions++
			}
			statDatabaseWireAwait(t, f, observer, statDatabaseWireVector{sessions: setupSessions, backends: 1})
			before := statDatabaseWireRead(t, f.Ctx, observer)
			for _, kind := range []string{"terminate", "eof", "protocol", "timeout", "administrator"} {
				t.Run(kind, func(t *testing.T) {
					c, pool, socket := statDatabaseWireClient(t, f, b)
					statDatabaseWireIdentity(t, f.Ctx, c)
					want := before
					want.sessions++
					if live := statDatabaseWireRead(t, f.Ctx, observer); live.backends != before.backends+1 {
						t.Fatal("live backend absent", live, before)
					}
					switch kind {
					case "terminate":
						statDatabaseWireTerminate(t, socket)
						if err := c.Raw(func(any) error { return driver.ErrBadConn }); !errors.Is(err, driver.ErrBadConn) {
							t.Fatal(err)
						}
					case "eof":
						want.abandoned++
						if err := socket.Close(); err != nil {
							t.Fatal(err)
						}
					case "protocol":
						want.fatal++
						if _, err := socket.Write([]byte{'?'}); err != nil {
							t.Fatal(err)
						}
						statDatabaseWireFatal(t, socket, "08P01", "invalid frontend message type 63")
					case "timeout":
						want.fatal++
						if _, err := c.ExecContext(f.Ctx, "SET idle_session_timeout='10ms'"); err != nil {
							t.Fatal(err)
						}
						statDatabaseWireFatal(t, socket, "57P05", "terminating connection due to idle-session timeout")
					case "administrator":
						want.killed++
						var pid int
						var signaled bool
						if err := c.QueryRowContext(f.Ctx, "SELECT pg_backend_pid()").Scan(&pid); err != nil {
							t.Fatal(err)
						}
						if err := observer.QueryRowContext(f.Ctx, "SELECT pg_terminate_backend($1,1000)", pid).Scan(&signaled); err != nil || !signaled {
							t.Fatal(signaled, err)
						}
						statDatabaseWireFatal(t, socket, "57P01", "terminating connection due to administrator command")
					}
					c.Close()
					pool.Close()
					statDatabaseWireAwait(t, f, observer, want)
					before = want
				})
			}
		})
	}
}
