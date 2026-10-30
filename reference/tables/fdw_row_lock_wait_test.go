package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceFDWRowLockCancellation(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fdwRowLockModeName(prepared), func(t *testing.T) {
			fdwRowLockBlockedCancel(t, prepared, "")
		})
	}
}

func TestReferenceFDWRowLockWaitPolicy(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		for _, strength := range []string{"KEY SHARE", "SHARE", "NO KEY UPDATE", "UPDATE"} {
			for _, policy := range []string{"NOWAIT", "SKIP LOCKED"} {
				t.Run(fdwRowLockModeName(prepared)+"/"+strength+"/"+policy, func(t *testing.T) {
					fdwRowLockBlockedCancel(t, prepared, policy, strength)
				})
			}
		}
	}
}

func fdwRowLockBlockedCancel(t *testing.T, prepared bool, policy string, strengths ...string) {
	strength := "UPDATE"
	if len(strengths) != 0 {
		strength = strengths[0]
	}
	fdwRowLockScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, source *sql.DB, run func(fixture.Step)) {
		holder, probe := fixture.LockConnections(t, p.Ctx, source)
		frontend, holderPID := 0, 0
		if b.PG {
			frontend = fixture.LockPID(t, p.Ctx, c)
			holderPID = fixture.LockPID(t, p.Ctx, holder)
		}
		tuple := ""
		if b.PG {
			if err := probe.QueryRowContext(p.Ctx, "SELECT ctid::text FROM fdw_source.data WHERE rid=1").Scan(&tuple); err != nil {
				t.Fatal(err)
			}
		}
		fixture.LockExec(t, p.Ctx, holder, "BEGIN", 0)
		defer fdwRowLockRollback(t, holder)
		if strength == "UPDATE" {
			fixture.LockExec(t, p.Ctx, holder, "UPDATE fdw_source.data SET label='uncommitted',revision=2 WHERE rid=1", 1)
		} else {
			fixture.LockExec(t, p.Ctx, holder, "DELETE FROM fdw_source.data WHERE rid=1", 1)
		}
		if policy == "" {
			run(fixture.Exec("CREATE TABLE fdw_row_lock_local(id integer)", 0))
		}
		run(fixture.Exec("BEGIN", 0))
		defer fdwRowLockRollback(t, c)
		if policy == "" {
			// Real earlier owner effects must disappear on rollback after a
			// later blocked statement is canceled, in both local and source SQL.
			run(fixture.Exec("INSERT INTO fdw_row_lock_local VALUES(1)", 1))
			run(fixture.Exec("UPDATE fdw_data SET label='owner' WHERE id=2", 1))
		}
		q := "SELECT id,label,flag FROM fdw_data ORDER BY id FOR " + strength
		if policy != "" {
			q += " " + policy
		}
		if !b.PG && policy != "" {
			// Native late locking honors the wait policy. Genuine postgres_fdw
			// deliberately does NOT propagate either policy to its source SQL.
			if policy == "SKIP LOCKED" {
				fdwRowLockClose(t, fdwRowLockRead(t, p.Ctx, c, q, prepared, [][]any{{int64(2), "two", false}}))
			} else {
				fdwRowLockDiagnostic(t, fdwRowLockFailedQuery(p.Ctx, c, q, prepared), "55P03", `could not obtain lock on row in relation "data"`)
			}
		} else {
			ctx, cancel := context.WithCancel(p.Ctx)
			defer cancel()
			finished := make(chan struct{})
			join := fixture.LockOperation(t, ctx, func(ctx context.Context) fixture.RelationOutcome {
				defer close(finished)
				return fdwRowLockFailedQuery(ctx, c, q, prepared)
			})
			if b.PG {
				remote := fdwRowLockRemoteWait(t, p.Ctx, frontend, 0, holderPID)
				var canceled bool
				if err := fixture.Admin.QueryRowContext(p.Ctx, "SELECT pg_cancel_backend($1)", remote).Scan(&canceled); err != nil || !canceled {
					t.Fatal("source backend cancellation", canceled, err)
				}
				remoteStrength := strength
				if strength == "KEY SHARE" {
					remoteStrength = "SHARE"
				}
				if strength == "NO KEY UPDATE" {
					remoteStrength = "UPDATE"
				}
				fdwRowLockDiagnostic(t, join(), "57014", "canceling statement due to user request", "while locking tuple "+tuple+" in relation \"data\"\nremote SQL command: SELECT id, label, flag FROM fdw_source.data ORDER BY id ASC NULLS LAST FOR "+remoteStrength)
			} else {
				// Observe the real provider SQL parked in the source scheduler,
				// not merely a launched goroutine or elapsed timeout.
				fixture.RelationLockPending(t, p, b, 0, 0, 0, "", finished)
				cancel()
				o := join()
				if o.Rows != nil || o.Result != nil || !errors.Is(o.Err, context.Canceled) {
					t.Fatalf("source wait cancellation returned rows/count or wrong error: %#v", o)
				}
				var state interface{ SQLState() string }
				if errors.As(o.Err, &state) {
					t.Fatalf("Go cancellation acquired fabricated SQLSTATE: %v", o.Err)
				}
				if o.Stmt != nil {
					o.Stmt.Close()
				}
			}
		}
		run(fixture.Exec("ROLLBACK", 0))
		fixture.LockExec(t, p.Ctx, holder, "ROLLBACK", 0)
		// Inspect actual source values after cancellation/rollback and prove
		// an independent transaction can acquire both rows without a leak.
		var label string
		if err := probe.QueryRowContext(p.Ctx, "SELECT label FROM fdw_source.data WHERE rid=1").Scan(&label); err != nil || label != "one" {
			t.Fatal(label, err)
		}
		if policy == "" {
			run(fixture.Select("SELECT id FROM fdw_row_lock_local", []string{"id"}, []string{"INT4"}, [][]any{}))
			if err := probe.QueryRowContext(p.Ctx, "SELECT label FROM fdw_source.data WHERE rid=2").Scan(&label); err != nil || label != "two" {
				t.Fatal("earlier remote owner write survived rollback", label, err)
			}
		}
		fixture.LockExec(t, p.Ctx, probe, "DELETE FROM fdw_source.data", 2)
	})
}
