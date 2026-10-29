package reference_test

import (
	"context"
	"database/sql"
	"runtime"
	"testing"
	"time"

	"stuntgres/reference/fixture"
)

func TestReferenceRoutineHostCancellation(t *testing.T) {
	hostNativeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, native *hostNativeRuntime) {
		defer routineLanguagesCleanup(t, c, "DROP OWNED BY host_cancel_owner CASCADE", "DROP ROLE host_cancel_owner")()
		hostExec(t, p, b, c, "CREATE ROLE host_cancel_owner")
		hostExec(t, p, b, c, "GRANT USAGE,CREATE ON SCHEMA public TO host_cancel_owner")
		hostExec(t, p, b, c, "CREATE TABLE host_cancel_effects(v integer)")
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO host_cancel_effects VALUES (1)", 1))
		hostExec(t, p, b, c, "ALTER TABLE host_cancel_effects OWNER TO host_cancel_owner")
		// The real dict program reaches an independently held advisory lock only
		// after conversion and arithmetic. Observing its lock wait proves actual
		// PL/Python admission, rather than merely an active outer SQL statement.
		hostFunction(t, p, b, c, "host_wait", "wait-v1", "assert isinstance(v, dict)\nv['a'] = str(int(v['a']) + 1)\nv['c'] = '3'\nplpy.execute('SELECT pg_advisory_xact_lock(918273)')\nreturn v", "SET application_name='host-cancel-inner'", true)
		hostExec(t, p, b, c, "CREATE FUNCTION host_cancel_prefix() RETURNS hstore LANGUAGE plpgsql SECURITY DEFINER SET application_name='host-cancel-wrapper' AS $$ BEGIN INSERT INTO host_cancel_effects VALUES (2); RETURN host_wait('a=>7'::hstore); END $$")
		hostExec(t, p, b, c, "ALTER FUNCTION host_cancel_prefix() OWNER TO host_cancel_owner")
		hostFunction(t, p, b, c, "host_after_cancel", "increment-v1", hostIncrementPython, "", true)
		if !b.PG {
			hostNativeCancellation(t, p, b, c, native)
			return
		}
		observer, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer observer.Close()
		var pid int
		if err := c.QueryRowContext(p.Ctx, "SELECT pg_backend_pid()").Scan(&pid); err != nil {
			t.Fatal(err)
		}
		for _, prepared := range []bool{false, true} {
			func() {
				ctx, cancel := context.WithTimeout(p.Ctx, 15*time.Second)
				defer cancel()
				if _, err := observer.ExecContext(ctx, "SELECT pg_advisory_lock(918273)"); err != nil {
					t.Fatal(err)
				}
				defer func() {
					cleanup, stop := context.WithTimeout(context.Background(), 5*time.Second)
					defer stop()
					if _, err := observer.ExecContext(cleanup, "SELECT pg_advisory_unlock(918273)"); err != nil {
						t.Error(err)
					}
				}()
				q := "SELECT host_cancel_prefix()"
				var stmt *sql.Stmt
				if prepared {
					stmt, err = c.PrepareContext(ctx, q)
					if err != nil {
						t.Fatal(err)
					}
					defer stmt.Close()
				}
				done := make(chan error, 1)
				joined := false
				defer func() {
					if !joined {
						cancel()
						select {
						case <-done:
						case <-time.After(5 * time.Second):
							t.Error("host cancellation worker did not join")
						}
					}
				}()
				go func() {
					var value any
					var callErr error
					if stmt != nil {
						callErr = stmt.QueryRowContext(ctx).Scan(&value)
					} else {
						callErr = c.QueryRowContext(ctx, q).Scan(&value)
					}
					done <- callErr
				}()
				for {
					var admitted bool
					if err := observer.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE pid=$1 AND state='active' AND wait_event_type='Lock' AND wait_event='advisory')", pid).Scan(&admitted); err != nil {
						t.Fatal(err)
					}
					if admitted {
						break
					}
					select {
					case err := <-done:
						joined = true
						t.Fatalf("host returned before admission barrier: %v", err)
					default:
					}
					runtime.Gosched()
				}
				var canceled bool
				if err := observer.QueryRowContext(ctx, "SELECT pg_cancel_backend($1)", pid).Scan(&canceled); err != nil || !canceled {
					t.Fatalf("cancel=%v/%v", canceled, err)
				}
				select {
				case err := <-done:
					joined = true
					if err == nil {
						t.Error("canceled host published success")
					} else {
						// Full SPI traceback observed on the pinned PostgreSQL 18.6 oracle.
						want := fixture.ControlsReferenceDiagnostic("57014", "spiexceptions.QueryCanceled: canceling statement due to user request")
						want.Context = "Traceback (most recent call last):\n  PL/Python function \"host_wait\", line 4, in <module>\n    plpy.execute('SELECT pg_advisory_xact_lock(918273)')\nPL/Python function \"host_wait\"\nPL/pgSQL function host_cancel_prefix() line 1 at RETURN"
						got := fixture.ExtractDiagnostic(t, err)
						if got != want {
							t.Errorf("admitted cancellation prepared=%v diagnostic=%#v; independently expected=%#v", prepared, got, want)
						}
					}
				case <-ctx.Done():
					t.Fatal("admitted host cancellation did not return")
				}
			}()
			// Server-side cancellation preserves the SAME physical session. The
			// query error unwinds statement effects and both routine SAVE frames.
			owner := "postgres"
			if b.PG {
				owner = "stuntgres"
			}
			hostRows(t, p, b, c, "SELECT current_user::text AS u,current_role::text AS r,current_setting('application_name') AS a", []string{"u", "r", "a"}, []string{"TEXT", "TEXT", "TEXT"}, [][]any{{owner, owner, ""}})
			var after int
			if err := c.QueryRowContext(p.Ctx, "SELECT pg_backend_pid()").Scan(&after); err != nil || after != pid {
				t.Fatalf("physical session changed %d -> %d: %v", pid, after, err)
			}
			hostRows(t, p, b, c, "SELECT v FROM host_cancel_effects ORDER BY v", []string{"v"}, []string{"INT4"}, [][]any{{int64(1)}})
			hostBag(t, p, b, c, "host_after_cancel('a=>7'::hstore)", [][]any{{"a", "8"}, {"c", "3"}})
		}
	})
}
