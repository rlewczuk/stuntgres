package reference_test

import (
	"context"
	"net"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"stuntgres/reference/fixture"
)

func TestReferenceEmbeddedServerNotifications(t *testing.T) {
	cfg := embeddedConfig()
	cfg.Listen = ""
	f := fixture.NewEmbeddedServerFixture(t, cfg, fixture.ServerUser, fixture.ServerPassword, fixture.ServerDatabase)
	for _, b := range f.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			listener := embeddedConnect(t, f.Ctx, b, true)
			sender := embeddedConnect(t, f.Ctx, b, true)
			embeddedExec(t, f.Ctx, listener, "LISTEN embedded_positive; LISTEN embedded_sentinel")
			embeddedExec(t, f.Ctx, sender, "NOTIFY embedded_positive, 'delivered'")
			event, err := listener.WaitForNotification(f.Ctx)
			if err != nil || event.Channel != "embedded_positive" || event.Payload != "delivered" || event.PID != sender.PgConn().PID() {
				t.Fatalf("notification: %+v %v", event, err)
			}
			embeddedExec(t, f.Ctx, sender, "BEGIN; NOTIFY embedded_positive, 'rolled-back'; ROLLBACK; NOTIFY embedded_sentinel, 'after'")
			event, err = listener.WaitForNotification(f.Ctx)
			if err != nil || event.Channel != "embedded_sentinel" || event.Payload != "after" || event.PID != sender.PgConn().PID() {
				t.Fatalf("rollback sentinel: %+v %v", event, err)
			}
		})
	}
}

// Observe actual execution ownership, not goroutine launch or a socket write.
// Every SELECT has its own autocommit stats snapshot on both backends.
func embeddedWaitActivity(t *testing.T, ctx context.Context, observer *pgx.Conn, pid uint32, query, event, kind string) {
	t.Helper()
	wait, stop := context.WithTimeout(ctx, fixture.Liveness)
	defer stop()
	for {
		var state, gotQuery, gotEvent, gotKind string
		err := observer.QueryRow(wait, "SELECT state, query, COALESCE(wait_event, ''), COALESCE(wait_event_type, '') FROM pg_stat_activity WHERE pid = $1::integer", int32(pid)).Scan(&state, &gotQuery, &gotEvent, &gotKind)
		if err == nil && state == "active" && gotQuery == query && gotEvent == event && gotKind == kind {
			return
		}
		if wait.Err() != nil {
			t.Fatalf("activity pid %d: %q %q %q %q: %v", pid, state, gotQuery, gotEvent, gotKind, err)
		}
		if err != nil && err != pgx.ErrNoRows {
			t.Fatal(err)
		}
		runtime.Gosched()
	}
}

type embeddedDialTrace struct {
	mu    sync.Mutex
	paths [][2]string
	dial  func(context.Context, string, string) (net.Conn, error)
}

func (d *embeddedDialTrace) connect(ctx context.Context, network, address string) (net.Conn, error) {
	d.mu.Lock()
	d.paths = append(d.paths, [2]string{network, address})
	d.mu.Unlock()
	return d.dial(ctx, network, address)
}

// A worker owns a child context and a buffered result; cleanup always cancels
// and joins it even if an admission assertion fails.
func embeddedSleep(t *testing.T, ctx context.Context, c *pgx.Conn) <-chan error {
	t.Helper()
	worker, stop := context.WithCancel(ctx)
	result := make(chan error, 1)
	joined := make(chan struct{})
	go func() { _, err := c.Exec(worker, "SELECT pg_sleep(600)"); result <- err; close(joined) }()
	t.Cleanup(func() {
		stop()
		select {
		case <-joined:
		case <-time.After(fixture.Liveness):
			t.Error("sleep worker did not join")
		}
	})
	return result
}
func embeddedJoin(t *testing.T, ctx context.Context, result <-chan error) error {
	t.Helper()
	select {
	case err := <-result:
		return err
	case <-ctx.Done():
		t.Fatal(ctx.Err())
		return nil
	}
}

func TestReferenceEmbeddedServerCancellation(t *testing.T) {
	cfg := embeddedConfig()
	cfg.Listen = ""
	f := fixture.NewEmbeddedServerFixture(t, cfg, fixture.ServerUser, fixture.ServerPassword, fixture.ServerDatabase)
	for _, b := range f.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			endpoint := b
			trace := &embeddedDialTrace{dial: b.DialFunc}
			if b.DialFunc != nil {
				endpoint.DialFunc = trace.connect
			}
			victim := embeddedConnect(t, f.Ctx, endpoint, true)
			observer := embeddedConnect(t, f.Ctx, b, true)
			result := embeddedSleep(t, f.Ctx, victim)
			embeddedWaitActivity(t, f.Ctx, observer, victim.PgConn().PID(), "SELECT pg_sleep(600)", "PgSleep", "Timeout")
			if err := victim.PgConn().CancelRequest(f.Ctx); err != nil {
				t.Fatal(err)
			}
			embeddedDiagnostic(t, embeddedJoin(t, f.Ctx, result), "57014", "canceling statement due to user request")
			embeddedInt(t, f.Ctx, victim, "SELECT 42", 42)
			if b.DialFunc != nil {
				trace.mu.Lock()
				paths := append([][2]string(nil), trace.paths...)
				trace.mu.Unlock()
				if len(paths) != 2 || paths[1] != [2]string{"pipe", "pipe"} {
					t.Fatalf("real cancel dial paths: %v", paths)
				}
			}
		})
	}
}
