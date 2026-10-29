package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"stuntgres/reference/fixture"
	"sync"
	"testing"
	"time"
)

// hostCancelAdmitted owns both callback and worker lifetime, even when an
// assertion fails. Cancellation follows actual map arithmetic, never a timer.
func hostCancelAdmitted(t *testing.T, p *fixture.Pair, native *hostNativeRuntime, call func(context.Context) error) {
	t.Helper()
	admitted := make(chan struct{})
	release := make(chan struct{})
	observed := make(chan error, 1)
	var once sync.Once
	native.mu.Lock()
	previous := native.gate
	native.gate = func(ctx context.Context) error {
		once.Do(func() { close(admitted) })
		select {
		case <-release:
		case <-ctx.Done():
		}
		err := ctx.Err()
		select {
		case observed <- err:
		default:
		}
		return err
	}
	native.mu.Unlock()
	ctx, cancel := context.WithTimeout(p.Ctx, 15*time.Second)
	done := make(chan error, 1)
	joined := false
	defer func() {
		cancel()
		close(release)
		if !joined {
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Error("native canceled callback worker did not join")
			}
		}
		native.mu.Lock()
		native.gate = previous
		native.mu.Unlock()
	}()
	go func() { done <- call(ctx) }()
	select {
	case <-admitted:
	case err := <-done:
		joined = true
		t.Fatalf("native host returned before callback admission: %v", err)
	case <-ctx.Done():
		t.Fatal("native callback was not admitted")
	}
	cancel()
	select {
	case err := <-done:
		joined = true
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("native canceled result=%v", err)
		}
		var state interface{ SQLState() string }
		if errors.As(err, &state) {
			t.Errorf("native cancellation fabricated SQLSTATE: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("native callback did not unwind")
	}
	select {
	case err := <-observed:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("callback context=%v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("callback cancellation observation missing")
	}
}

func hostNativeCancellation(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, native *hostNativeRuntime) {
	t.Helper()
	const recovery = "SELECT key,value FROM each(host_after_cancel($1::hstore)) ORDER BY key"
	good, err := c.PrepareContext(p.Ctx, recovery)
	if err != nil {
		t.Fatal(err)
	}
	defer good.Close()
	held := fixture.HeldStatement{Executor: c, Statement: good, SQL: recovery}
	recoverHeld := func() {
		hostRows(t, p, b, held, recovery, []string{"key", "value"}, []string{"TEXT", "TEXT"}, [][]any{{"a", "8"}, {"c", "3"}}, "a=>7")
	}
	recoverHeld()
	for _, prepared := range []bool{false, true} {
		func() {
			var stmt *sql.Stmt
			if prepared {
				var err error
				stmt, err = c.PrepareContext(p.Ctx, "SELECT host_cancel_prefix()")
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
			}
			hostCancelAdmitted(t, p, native, func(ctx context.Context) error {
				var value any
				if stmt != nil {
					return stmt.QueryRowContext(ctx).Scan(&value)
				}
				return c.QueryRowContext(ctx, "SELECT host_cancel_prefix()").Scan(&value)
			})
			hostRows(t, p, b, c, "SELECT v FROM host_cancel_effects ORDER BY v", []string{"v"}, []string{"INT4"}, [][]any{{int64(1)}})
			hostRows(t, p, b, c, "SELECT current_user::text AS u,current_role::text AS r,current_setting('application_name') AS a", []string{"u", "r", "a"}, []string{"TEXT", "TEXT", "TEXT"}, [][]any{{"postgres", "postgres", ""}})
			recoverHeld()
		}()
	}
	// CREATE has no application compile callback: its real publication owner is
	// the transaction. Cancel admitted executable work after staging both a new
	// declaration and a replacement, then attempt normal COMMIT. The aborted
	// owner must discard both without relying on an explicit cleanup ROLLBACK.
	for _, replacement := range []bool{false, true} {
		func() {
			hostExec(t, p, b, c, "BEGIN")
			ended := false
			defer func() {
				if !ended {
					cleanup, stop := context.WithTimeout(context.Background(), 5*time.Second)
					defer stop()
					if _, err := c.ExecContext(cleanup, "ROLLBACK"); err != nil {
						t.Error(err)
					}
				}
			}()
			name := "host_canceled_publication"
			program := "increment-v1"
			if replacement {
				name = "host_after_cancel"
				program = "double-v1"
			}
			hostExec(t, p, b, c, "CREATE OR REPLACE FUNCTION "+name+"(v hstore) RETURNS hstore LANGUAGE go_host AS '"+program+"'")
			expected := [][]any{{"a", "8"}, {"c", "3"}}
			if replacement {
				expected = [][]any{{"a", "14"}, {"d", "second"}}
			}
			hostBag(t, p, b, c, name+"('a=>7'::hstore)", expected)
			hostCancelAdmitted(t, p, native, func(ctx context.Context) error {
				var value any
				return c.QueryRowContext(ctx, "SELECT host_cancel_prefix()").Scan(&value)
			})
			hostExec(t, p, b, c, "COMMIT")
			ended = true
			hostRows(t, p, b, c, "SELECT count(*) AS v FROM pg_proc WHERE proname='host_canceled_publication'", []string{"v"}, []string{"INT8"}, [][]any{{int64(0)}})
			hostRows(t, p, b, c, "SELECT v FROM host_cancel_effects ORDER BY v", []string{"v"}, []string{"INT4"}, [][]any{{int64(1)}})
			hostRows(t, p, b, c, "SELECT current_user::text AS u,current_role::text AS r,current_setting('application_name') AS a", []string{"u", "r", "a"}, []string{"TEXT", "TEXT", "TEXT"}, [][]any{{"postgres", "postgres", ""}})
			recoverHeld()
		}()
	}
}
