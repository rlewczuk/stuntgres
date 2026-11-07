package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/stdlib"

	"stuntgres"
	"stuntgres/reference/fixture"
)

type pgNotification = stuntgres.Notification

func pgNotifyWait(ctx context.Context, conn *sql.Conn, pg bool) (pgNotification, error) {
	if !pg {
		return stuntgres.WaitForNotification(ctx, conn)
	}
	var got pgNotification
	err := conn.Raw(func(raw any) error {
		c, ok := raw.(*stdlib.Conn)
		if !ok {
			return fmt.Errorf("notification oracle connection: %T", raw)
		}
		n, err := c.Conn().WaitForNotification(ctx)
		if err == nil {
			got = pgNotification{PID: uint64(n.PID), Channel: n.Channel, Payload: n.Payload}
		}
		return err
	})
	return got, err
}

func pgNotifyScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend)) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) { body(t, p, backend) })
	}
}

func pgNotifyConn(t *testing.T, p *fixture.Pair, b fixture.Backend) *sql.Conn {
	t.Helper()
	db := p.Native
	if b.PG {
		db = p.CopyPostgres
	}
	c, err := db.Conn(p.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		// A terminal absence probe may retire the PG transport. Cleanup does
		// not reconnect or mask its explicitly observed lifecycle outcome.
		ctx, cancel := context.WithTimeout(context.Background(), fixture.Liveness)
		defer cancel()
		_, _ = c.ExecContext(ctx, "ROLLBACK")
		_, _ = c.ExecContext(ctx, "UNLISTEN *")
		_ = c.Close()
	})
	return c
}

func pgNotifySQL(t *testing.T, p *fixture.Pair, b fixture.Backend, c fixture.Executor, q string) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(q, 0))
}

func pgNotifyVoid(t *testing.T, p *fixture.Pair, b fixture.Backend, c fixture.Executor, q string) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select(
		q, []string{"pg_notify"}, []string{"VOID"}, [][]any{{""}},
	))
}

func pgNotifyReceive(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, channel, payload string, pid uint64) pgNotification {
	t.Helper()
	ctx, cancel := context.WithTimeout(p.Ctx, fixture.Liveness)
	defer cancel()
	got, err := pgNotifyWait(ctx, c, b.PG)
	if err != nil {
		t.Fatalf("receive %q/%q: %v", channel, payload, err)
	}
	if got.PID == 0 || got.Channel != channel || got.Payload != payload {
		t.Fatalf("notification = %+v, want positive PID, channel %q payload %q", got, channel, payload)
	}
	if pid != 0 && got.PID != pid {
		t.Fatalf("notification PID = %d, want sender %d", got.PID, pid)
	}
	return got
}

// Terminal probes deliberately own their physical connection until case end.
// No later phase silently obtains a replacement after transport cancellation.
func pgNotifyAbsent(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	ctx, cancel := context.WithTimeout(p.Ctx, 100*time.Millisecond)
	defer cancel()
	got, err := pgNotifyWait(ctx, c, b.PG)
	if !errors.Is(err, context.DeadlineExceeded) || got != (pgNotification{}) {
		t.Fatalf("absence = %+v, %v; want zero notification and deadline", got, err)
	}
}

func pgNotifyHealth(t *testing.T, p *fixture.Pair, b fixture.Backend, c fixture.Executor) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select(
		"SELECT 1 AS healthy", []string{"healthy"}, []string{"INT4"}, [][]any{{int64(1)}},
	))
}

// pgNotifyPID reports the physical backend identity of the sender session.
func pgNotifyPID(t *testing.T, p *fixture.Pair, c *sql.Conn) int64 {
	t.Helper()
	var pid int64
	if err := c.QueryRowContext(p.Ctx, "SELECT pg_backend_pid()").Scan(&pid); err != nil {
		t.Fatal(err)
	}
	if pid <= 0 {
		t.Fatalf("pg_backend_pid = %d, want positive", pid)
	}
	return pid
}
