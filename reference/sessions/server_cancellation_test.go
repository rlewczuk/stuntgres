package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/binary"
	"errors"
	"net"
	"runtime"
	"stuntgres/reference/fixture"
	"testing"
	"time"

	"github.com/lib/pq"
)

// Cancellation uses an actual conflicting table lock, not an unavailable native
// pg_sleep provider. Repeated CancelRequests are an admission rendezvous: a
// 57014 response proves a matching request reached the registered command.
func TestReferenceServerCancellation(t *testing.T) {
	f := fixture.NewServerFixture(t)
	for _, b := range f.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			dsn := f.DSNFor(b)
			owner := serverAuthenticated(t, dsn)
			serverWantCode(t, owner.query(t, "CREATE TABLE server_cancel(v integer); INSERT INTO server_cancel VALUES (1)"), "")
			t.Run("cancel_registered_command", func(t *testing.T) {
				serverWantCode(t, owner.query(t, "TRUNCATE server_cancel; INSERT INTO server_cancel VALUES (1)"), "")
				serverWantCode(t, owner.query(t, "BEGIN; LOCK TABLE server_cancel IN ACCESS EXCLUSIVE MODE"), "")
				defer func() { serverWantCode(t, owner.query(t, "ROLLBACK"), "") }()
				victim := serverAuthenticated(t, dsn)
				serverWantCode(t, victim.query(t, "BEGIN"), "")
				victim.send(t, serverPacket('Q', []byte("INSERT INTO server_cancel VALUES (9)\x00")))
				if b.PG {
					observer := f.RawPool(t, b)
					ctx, cancel := context.WithTimeout(f.Ctx, 10*time.Second)
					defer cancel()
					serverObserveCancelWait(t, ctx, observer, int(victim.pid), owner.pid, "server_cancel")
				}
				type reply struct {
					frames []serverFrame
					err    error
				}
				result := make(chan reply, 1)
				go func() {
					var frames []serverFrame
					for {
						frame, e := victim.readFrame()
						if e != nil {
							result <- reply{frames, e}
							return
						}
						frames = append(frames, frame)
						if frame.tag == 'Z' {
							result <- reply{frames, nil}
							return
						}
					}
				}()
				joined := false
				defer func() {
					if !joined {
						victim.Close()
						<-result
					}
				}()
				ctx, cancel := context.WithTimeout(f.Ctx, 10*time.Second)
				defer cancel()
				var frames []serverFrame
				if b.PG {
					// The observed wait proves registration; one cancellation avoids
					// signaling a subsequent command after this request completes.
					serverCancel(t, dsn, victim.pid, victim.key)
				}
			loop:
				for {
					select {
					case r := <-result:
						joined = true
						if r.err != nil {
							t.Fatal(r.err)
						}
						frames = r.frames
						break loop
					case <-ctx.Done():
						victim.Close()
						serverWantCode(t, owner.query(t, "ROLLBACK"), "")
						t.Fatal("cancel did not join blocked command")
					default:
						if !b.PG {
							serverCancel(t, dsn, victim.pid, victim.key)
						}
						runtime.Gosched()
					}
				}
				serverWantCode(t, frames, "57014")
				if serverFields(frames[0].body)['M'] != "canceling statement due to user request" {
					t.Fatal(serverFields(frames[0].body))
				}
				if frames[len(frames)-1].tag != 'Z' || string(frames[len(frames)-1].body) != "E" {
					t.Fatal(serverFrameSummary(frames))
				}
				serverWantCode(t, victim.query(t, "ROLLBACK"), "")
				serverWantCode(t, owner.query(t, "COMMIT"), "")
				serverScalar(t, victim, "SELECT count(*) FROM server_cancel", "1")
				serverScalar(t, victim, "SELECT v FROM server_cancel", "1")
				serverScalar(t, victim, "SELECT 42", "42")
			})
			t.Run("wrong_and_retired_keys", func(t *testing.T) {
				closed := serverAuthenticated(t, dsn)
				pid, key := closed.pid, closed.key
				closed.send(t, serverPacket('X', nil))
				closed.Close()
				peer := serverAuthenticated(t, dsn)
				serverCancel(t, dsn, peer.pid, peer.key^1)
				serverCancel(t, dsn, pid, key)
				serverScalar(t, peer, "SELECT 42", "42")
				serverWantCode(t, peer.query(t, "BEGIN; LOCK TABLE server_cancel IN ACCESS EXCLUSIVE MODE; ROLLBACK"), "")
			})
			t.Run("disconnect_releases_transaction", func(t *testing.T) {
				serverWantCode(t, owner.query(t, "TRUNCATE server_cancel; INSERT INTO server_cancel VALUES (1)"), "")
				gone := serverAuthenticated(t, dsn)
				serverWantCode(t, gone.query(t, "BEGIN; LOCK TABLE server_cancel IN ACCESS EXCLUSIVE MODE; INSERT INTO server_cancel VALUES (8)"), "")
				gone.Close()
				peer := serverAuthenticated(t, dsn)
				serverWantCode(t, peer.query(t, "BEGIN; LOCK TABLE server_cancel IN ACCESS EXCLUSIVE MODE; COMMIT"), "")
				serverScalar(t, peer, "SELECT count(*) FROM server_cancel", "1")
				serverScalar(t, peer, "SELECT v FROM server_cancel", "1")
			})
			t.Run("pq_context_cancel", func(t *testing.T) {
				serverWantCode(t, owner.query(t, "CREATE TABLE server_cancel_pq(v integer); INSERT INTO server_cancel_pq VALUES (1)"), "")
				serverWantCode(t, owner.query(t, "BEGIN; LOCK TABLE server_cancel_pq IN ACCESS EXCLUSIVE MODE"), "")
				defer func() { serverWantCode(t, owner.query(t, "ROLLBACK"), "") }()
				const query = "INSERT INTO server_cancel_pq VALUES (77)"
				dialer := &serverCancelDialer{
					query: query, sent: make(chan struct{}, 1),
					cancelSent: make(chan [2]uint32, 1), codes: make(chan string, 1),
				}
				connector, e := pq.NewConnector(dsn)
				if e != nil {
					t.Fatal(e)
				}
				connector.Dialer(dialer)
				pool := sql.OpenDB(connector)
				defer pool.Close()
				conn, e := pool.Conn(f.Ctx)
				if e != nil {
					t.Fatal(e)
				}
				defer conn.Close()
				var pid int
				if b.PG {
					if e = conn.QueryRowContext(f.Ctx, "SELECT pg_backend_pid()").Scan(&pid); e != nil {
						t.Fatal(e)
					}
				}
				deadline, stop := context.WithTimeout(f.Ctx, 10*time.Second)
				defer stop()
				ctx, cancel := context.WithCancel(deadline)
				defer cancel()
				done := make(chan error, 1)
				go func() {
					_, err := conn.ExecContext(ctx, query)
					done <- err
				}()
				joined := false
				defer func() {
					if !joined {
						cancel()
						// Releasing the holder also unblocks a broken cancellation
						// implementation, so the test joins its worker on failure.
						serverWantCode(t, owner.query(t, "ROLLBACK"), "")
						select {
						case <-done:
						case <-time.After(10 * time.Second):
							t.Error("pq cancellation worker did not join")
						}
					}
				}()
				select {
				case <-dialer.sent:
				case e = <-done:
					joined = true
					t.Fatalf("pq query ended before wire observation: %v", e)
				case <-deadline.Done():
					t.Fatal("pq query was not written", deadline.Err())
				}
				if b.PG {
					serverObserveCancelWait(t, deadline, f.RawPool(t, b), pid, owner.pid, "server_cancel_pq")
				}
				// PostgreSQL has an exact lock-queue barrier. Native has no
				// public queue observer: the actual pq Query write precedes
				// cancellation, and matching packets rendezvous with command
				// registration until the server's 57014 proves cancellation.
				cancel()
				var key [2]uint32
				select {
				case key = <-dialer.cancelSent:
				case <-deadline.Done():
					t.Fatal("pq context watcher did not send CancelRequest", deadline.Err())
				}
			cancelLoop:
				for {
					select {
					case e = <-done:
						joined = true
						break cancelLoop
					case <-deadline.Done():
						t.Fatal("pq context cancellation did not join", deadline.Err())
					default:
						if !b.PG {
							serverCancel(t, dsn, key[0], key[1])
						}
						runtime.Gosched()
					}
				}
				if e == nil {
					t.Fatal("actively canceled pq call succeeded")
				}
				var diagnostic *pq.Error
				if !errors.Is(e, context.Canceled) && !(errors.As(e, &diagnostic) && diagnostic.Code == "57014") {
					t.Fatalf("pq cancellation error: %v", e)
				}
				select {
				case code := <-dialer.codes:
					if code != "57014" {
						t.Fatalf("pq wire cancellation SQLSTATE %q want 57014", code)
					}
				default:
					t.Fatal("pq cancellation lacked server 57014 response")
				}
				serverWantCode(t, owner.query(t, "COMMIT"), "")
				// pq deliberately retires a context-canceled connection; a
				// separate observer proves atomicity and subsequent usability.
				serverScalar(t, owner, "SELECT count(*) FROM server_cancel_pq", "1")
				serverScalar(t, owner, "SELECT v FROM server_cancel_pq", "1")
				serverScalar(t, owner, "SELECT 42", "42")
			})
			t.Run("disconnect_blocked_execution", func(t *testing.T) {
				serverWantCode(t, owner.query(t, "TRUNCATE server_cancel; INSERT INTO server_cancel VALUES (1)"), "")
				serverWantCode(t, owner.query(t, "BEGIN; LOCK TABLE server_cancel IN ACCESS EXCLUSIVE MODE"), "")
				defer func() { serverWantCode(t, owner.query(t, "ROLLBACK"), "") }()
				gone := serverAuthenticated(t, dsn)
				serverWantCode(t, gone.query(t, "BEGIN"), "")
				gone.send(t, serverPacket('Q', []byte("INSERT INTO server_cancel VALUES (99)\x00")))
				if b.PG {
					ctx, cancel := context.WithTimeout(f.Ctx, 10*time.Second)
					defer cancel()
					serverObserveCancelWait(t, ctx, f.RawPool(t, b), int(gone.pid), owner.pid, "server_cancel")
				}
				gone.Close()
				serverWantCode(t, owner.query(t, "COMMIT"), "")
				peer := serverAuthenticated(t, dsn)
				serverWantCode(t, peer.query(t, "BEGIN; LOCK TABLE server_cancel IN ACCESS EXCLUSIVE MODE; COMMIT"), "")
				serverScalar(t, peer, "SELECT count(*) FROM server_cancel", "1")
				serverScalar(t, peer, "SELECT v FROM server_cancel", "1")
			})
		})
	}
	if b, ok := f.NativeBackend(); ok {
		t.Run("native_shutdown_joins_sessions", func(t *testing.T) {
			owner := serverAuthenticated(t, f.DSNFor(b))
			serverWantCode(t, owner.query(t, "TRUNCATE server_cancel; INSERT INTO server_cancel VALUES (1)"), "")
			serverWantCode(t, owner.query(t, "BEGIN; LOCK TABLE server_cancel IN ACCESS EXCLUSIVE MODE; INSERT INTO server_cancel VALUES (88)"), "")
			blocked := serverAuthenticated(t, f.DSNFor(b))
			blocked.send(t, serverPacket('Q', []byte("INSERT INTO server_cancel VALUES (99)\x00")))
			f.Stop(t)
			f.Restart(t, true)
			db := f.NativeDB(t, fixture.ServerDatabase)
			serverInt(t, f.Ctx, db, "SELECT count(*) FROM server_cancel", 1)
			serverInt(t, f.Ctx, db, "SELECT v FROM server_cancel", 1)
		})
	}
}

// The dialer observes real lib/pq traffic without replacing execution or its
// context watcher. In particular, a goroutine launch is not Query admission.
type serverCancelDialer struct {
	query      string
	sent       chan struct{}
	cancelSent chan [2]uint32
	codes      chan string
}

func (d *serverCancelDialer) Dial(network, address string) (net.Conn, error) {
	return d.DialTimeout(network, address, 5*time.Second)
}

func (d *serverCancelDialer) DialTimeout(network, address string, timeout time.Duration) (net.Conn, error) {
	c, err := net.DialTimeout(network, address, timeout)
	if err != nil {
		return nil, err
	}
	return &serverCancelConn{Conn: c, observer: d}, nil
}

type serverCancelConn struct {
	net.Conn
	observer *serverCancelDialer
	pending  []byte
	reported bool
}

func (c *serverCancelConn) Write(p []byte) (int, error) {
	n, err := c.Conn.Write(p)
	if err == nil && n == len(p) {
		if bytes.Equal(p, serverPacket('Q', append([]byte(c.observer.query), 0))) {
			select {
			case c.observer.sent <- struct{}{}:
			default:
			}
		}
		if len(p) == 16 && binary.BigEndian.Uint32(p[:4]) == 16 && binary.BigEndian.Uint32(p[4:8]) == 80877102 {
			key := [2]uint32{binary.BigEndian.Uint32(p[8:12]), binary.BigEndian.Uint32(p[12:16])}
			select {
			case c.observer.cancelSent <- key:
			default:
			}
		}
	}
	return n, err
}

func (c *serverCancelConn) Read(p []byte) (int, error) {
	n, err := c.Conn.Read(p)
	if c.reported {
		return n, err
	}
	c.pending = append(c.pending, p[:n]...)
	for len(c.pending) >= 5 {
		size := int(binary.BigEndian.Uint32(c.pending[1:5]))
		if size < 4 || size > 16<<20 {
			c.reported = true
			break
		}
		if len(c.pending) < size+1 {
			break
		}
		if c.pending[0] == 'E' {
			c.observer.codes <- serverFields(c.pending[5 : size+1])['C']
			c.reported = true
			c.pending = nil
			break
		}
		c.pending = c.pending[size+1:]
	}
	return n, err
}

func serverObserveCancelWait(t *testing.T, ctx context.Context, observer *sql.DB, waiter int, holder uint32, table string) {
	t.Helper()
	for {
		var waiting bool
		err := observer.QueryRowContext(ctx, `SELECT EXISTS (
			SELECT 1 FROM pg_locks WHERE pid=$1 AND locktype='relation'
			AND relation=$3::regclass AND mode='RowExclusiveLock' AND NOT granted
		) AND $2 = ANY(pg_blocking_pids($1))`, waiter, holder, table).Scan(&waiting)
		if err != nil {
			t.Fatal("cancellation relation-wait barrier", err)
		}
		if waiting {
			return
		}
		runtime.Gosched()
	}
}

func serverExec(t *testing.T, ctx context.Context, db *sql.DB, q string) {
	t.Helper()
	if _, e := db.ExecContext(ctx, q); e != nil {
		t.Fatalf("%s: %v", q, e)
	}
}
func serverInt(t *testing.T, ctx context.Context, db *sql.DB, q string, want int) {
	t.Helper()
	var got int
	if e := db.QueryRowContext(ctx, q).Scan(&got); e != nil {
		t.Fatal(e)
	}
	if got != want {
		t.Fatalf("%s = %d want %d", q, got, want)
	}
}
