package reference_test

import (
	"context"
	"encoding/base64"
	"errors"
	"testing"
	"time"

	"stuntgres/reference/fixture"
)

func roleTransactionAtomicPassword(t *testing.T) {
	t.Helper()
	for _, held := range []bool{false, true} {
		label := "direct"
		if held {
			label = "held"
		}
		t.Run("password_cancel/"+label, func(t *testing.T) {
			roleTransactionT2Cases(t, []string{"ra_password", "ra_password_keep"}, nil, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
				c, _ := fixture.RoleTransactionConn(t, p, b)
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
				run(fixture.Exec("CREATE ROLE ra_password LOGIN", 0))
				// Password fixture setup and stored verifiers stay outside recorded SQL.
				if _, err := c.ExecContext(p.Ctx, "ALTER ROLE ra_password PASSWORD 'role-core-fixture'"); err != nil {
					t.Fatal(err)
				}
				var before string
				if b.PG {
					if err := fixture.Admin.QueryRowContext(p.Ctx, "SELECT rolpassword FROM pg_authid WHERE rolname='ra_password'").Scan(&before); err != nil {
						t.Fatal(err)
					}
				}
				run(fixture.Exec("BEGIN", 0))
				run(fixture.Exec("CREATE ROLE ra_password_keep", 0))
				run(fixture.Exec("SAVEPOINT password_mark", 0))
				salt := base64.StdEncoding.EncodeToString([]byte("cancel-salt-1234"))
				key := base64.StdEncoding.EncodeToString(make([]byte, 32))
				query := "ALTER ROLE ra_password LOGIN PASSWORD 'SCRAM-SHA-256$2147483647:" + salt + "$" + key + ":" + key + "'"
				if b.PG {
					fixture.SessionCancelQuery(t, p.Ctx, c, held, query, false)
				} else {
					ctx, cancel := context.WithTimeout(p.Ctx, 40*time.Millisecond)
					var outcome fixture.RelationOutcome
					if held {
						cancel()
						stmt, err := c.PrepareContext(p.Ctx, query)
						if err != nil {
							t.Fatal(err)
						}
						ctx, cancel = context.WithTimeout(p.Ctx, 40*time.Millisecond)
						outcome.Result, outcome.Err = stmt.ExecContext(ctx)
						stmt.Close()
					} else {
						outcome = fixture.RowExecute(ctx, c, query, false)
					}
					cancel()
					if outcome.Result != nil || !errors.Is(outcome.Err, context.DeadlineExceeded) {
						t.Fatal("password cancellation", outcome.Result, outcome.Err)
					}
				}
				run(fixture.ErrorDiagnostic("SELECT 1", fixture.ControlsReferenceDiagnostic("25P02", "current transaction is aborted, commands ignored until end of transaction block")))
				run(fixture.Exec("ROLLBACK TO password_mark", 0))
				run(fixture.RoleTransactionExists("ra_password_keep", true))
				run(fixture.Select("SELECT rolcanlogin FROM pg_roles WHERE rolname='ra_password'", []string{"rolcanlogin"}, []string{"BOOL"}, [][]any{{true}}))
				if b.PG {
					var after string
					if err := fixture.Admin.QueryRowContext(p.Ctx, "SELECT rolpassword FROM pg_authid WHERE rolname='ra_password'").Scan(&after); err != nil || after != before {
						t.Fatal("canceled password changed verifier", err)
					}
				}
				run(fixture.Exec("RELEASE password_mark", 0))
				run(fixture.Exec("COMMIT", 0))
				run(fixture.Select("SELECT 42 AS sentinel", []string{"sentinel"}, []string{"INT4"}, [][]any{{int64(42)}}))
			})
		})
	}
}
