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

type sessionMessage = stuntgres.Notification

func sessionMessageWait(ctx context.Context, conn *sql.Conn, pg bool) (sessionMessage, error) {
	if !pg {
		return stuntgres.WaitForNotification(ctx, conn)
	}
	var got sessionMessage
	err := conn.Raw(func(raw any) error {
		c, ok := raw.(*stdlib.Conn)
		if !ok {
			return fmt.Errorf("notification oracle connection: %T", raw)
		}
		n, err := c.Conn().WaitForNotification(ctx)
		if err == nil {
			got = sessionMessage{PID: uint64(n.PID), Channel: n.Channel, Payload: n.Payload}
		}
		return err
	})
	return got, err
}

func sessionMessagesScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend)) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) { body(t, p, backend) })
	}
}

func sessionMessageConn(t *testing.T, p *fixture.Pair, b fixture.Backend) *sql.Conn {
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
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_, _ = c.ExecContext(ctx, "ROLLBACK")
		_, _ = c.ExecContext(ctx, "UNLISTEN *")
		_ = c.Close()
	})
	return c
}

func sessionMessageSQL(t *testing.T, p *fixture.Pair, b fixture.Backend, c fixture.Executor, q string) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(q, 0))
}

func sessionMessageReceive(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, channel, payload string) sessionMessage {
	t.Helper()
	ctx, cancel := context.WithTimeout(p.Ctx, 2*time.Second)
	defer cancel()
	got, err := sessionMessageWait(ctx, c, b.PG)
	if err != nil {
		t.Fatalf("receive %q/%q: %v", channel, payload, err)
	}
	if got.PID == 0 || got.Channel != channel || got.Payload != payload {
		t.Fatalf("notification = %+v, want positive PID, channel %q payload %q", got, channel, payload)
	}
	return got
}

// Terminal probes deliberately own their physical connection until case end.
// No later phase silently obtains a replacement after transport cancellation.
func sessionMessageAbsent(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	ctx, cancel := context.WithTimeout(p.Ctx, 100*time.Millisecond)
	defer cancel()
	got, err := sessionMessageWait(ctx, c, b.PG)
	if !errors.Is(err, context.DeadlineExceeded) || got != (sessionMessage{}) {
		t.Fatalf("absence = %+v, %v; want zero notification and deadline", got, err)
	}
}

func sessionMessageHealth(t *testing.T, p *fixture.Pair, b fixture.Backend, c fixture.Executor) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select(
		"SELECT 1 AS healthy", []string{"healthy"}, []string{"INT4"}, [][]any{{int64(1)}},
	))
}
