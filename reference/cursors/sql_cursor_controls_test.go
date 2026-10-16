package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceSQLCursorCancellation(t *testing.T) {
	sqlCursorScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		sqlCursorSetup(t, p, b, c)
		sqlCursorStep(t, p, b, c, fixture.Exec("BEGIN", 0))
		if !b.PG {
			ctx, cancel := context.WithCancel(p.Ctx)
			cancel()
			declare := "DECLARE canceled NO SCROLL CURSOR FOR SELECT i,t FROM cursor_rows ORDER BY i"
			stmt, err := c.PrepareContext(ctx, declare)
			if stmt != nil {
				stmt.Close()
				t.Fatal("canceled Prepare exposed statement")
			}
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("Prepare cancellation: %v", err)
			}
			result, err := c.ExecContext(ctx, declare)
			if result != nil || !errors.Is(err, context.Canceled) {
				t.Fatalf("DECLARE cancellation: %v %v", result, err)
			}
			sqlCursorError(t, p, b, c, "FETCH ALL FROM canceled", sqlCursorMissing("canceled"))
			sqlCursorStep(t, p, b, c, fixture.Exec(declare, 0))
			rows, err := c.QueryContext(ctx, "FETCH 1 FROM canceled")
			if rows != nil {
				rows.Close()
				t.Fatal("canceled FETCH exposed rows")
			}
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("FETCH cancellation: %v", err)
			}
			sqlCursorStep(t, p, b, c, sqlCursorBatch("FETCH 1 FROM canceled", [][]any{{int64(1), "one"}}, false))
			return
		}
		// pg_sleep is an oracle-only admission witness, not a new native provider.
		sqlCursorStep(t, p, b, c, fixture.Exec("DECLARE canceled NO SCROLL CURSOR FOR SELECT i,pg_sleep(600) FROM cursor_rows ORDER BY i", 0))
		sqlCursorStep(t, p, b, c, fixture.Exec("SAVEPOINT cancel_s", 0))
		fixture.SQLCursorCancelDemand(t, p, c, "FETCH ALL FROM canceled")
		sqlCursorStep(t, p, b, c, fixture.Exec("ROLLBACK TO cancel_s", 0))
		sqlCursorError(t, p, b, c, "FETCH ALL FROM canceled", sqlCursorDiagnostic("55000", "portal \"canceled\" cannot be run"))
		sqlCursorStep(t, p, b, c, fixture.Exec("CLOSE canceled", 0))
	})
}

func TestReferenceSQLCursorNativeControls(t *testing.T) {
	for _, replacement := range []string{"reset", "restore", "failed-restore"} {
		t.Run(replacement, func(t *testing.T) {
			sqlCursorScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				sqlCursorSetup(t, p, b, c)
				sqlCursorStep(t, p, b, c, fixture.Exec("BEGIN", 0))
				sqlCursorStep(t, p, b, c, fixture.Exec("DECLARE control_c NO SCROLL CURSOR FOR SELECT i,t FROM cursor_rows ORDER BY i", 0))
				query := "FETCH 1 FROM control_c"
				ctx := context.WithValue(p.Ctx, fixture.RecorderKey{}, b.Recorder)
				rows, err := c.QueryContext(ctx, query)
				if err != nil {
					t.Fatal(err)
				}
				defer rows.Close()
				frozen := fixture.SequenceFrozenRows{Executor: c, Rows: rows, Query: query}
				// pq owns a live wire result until consumption; native eager batches
				// alone promise detached reads across a host catalog replacement.
				if b.PG {
					sqlCursorStep(t, p, b, frozen, sqlCursorBatch(query, [][]any{{int64(1), "one"}}, true))
				}
				sqlCursorStep(t, p, b, c, fixture.Exec("INSERT INTO cursor_rows VALUES(4,'private')", 1))
				if b.PG {
					// SQL transaction end is PostgreSQL's real counterpart, not a reset API.
					sqlCursorStep(t, p, b, c, fixture.Exec("ROLLBACK", 0))
				} else {
					var dump bytes.Buffer
					if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
						t.Fatal(err)
					}
					if strings.Contains(dump.String(), "control_c") || strings.Contains(dump.String(), "private") || strings.Contains(dump.String(), "DECLARE") {
						t.Fatalf("Dump contains session state/private write: %s", dump.String())
					}
					switch replacement {
					case "reset":
						err = stuntgres.Reset(p.Ctx, p.Native)
					case "restore":
						err = stuntgres.Restore(p.Ctx, p.Native, strings.NewReader("CREATE TABLE cursor_rows(i INTEGER NOT NULL,t TEXT); INSERT INTO cursor_rows VALUES(99,'replacement')"))
					case "failed-restore":
						err = stuntgres.Restore(p.Ctx, p.Native, strings.NewReader("CREATE TABLE bad(i INTEGER); INSERT INTO bad VALUES($1)"))
						want := sqlCursorDiagnostic("42P02", "parameters are not allowed in restore")
						want.Position = "53"
						fixture.AssertDiagnostic(t, err, &want)
						err = nil
					}
					if err != nil {
						t.Fatal(err)
					}
				}
				if !b.PG {
					sqlCursorStep(t, p, b, frozen, sqlCursorBatch(query, [][]any{{int64(1), "one"}}, false))
				}
				if !b.PG && replacement == "failed-restore" {
					sqlCursorStep(t, p, b, c, sqlCursorBatch(query, [][]any{{int64(2), nil}}, false))
					return
				}
				if !b.PG {
					result, err := c.ExecContext(p.Ctx, "FETCH ALL FROM control_c")
					if result != nil {
						t.Fatal("fenced FETCH exposed result")
					}
					fixture.TransactionOrdinary(t, err, "stuntgres: catalog replaced before publication")
					sqlCursorStep(t, p, b, c, fixture.Exec("ROLLBACK", 0))
				}
				sqlCursorStep(t, p, b, c, fixture.ErrorDiagnostic("FETCH ALL FROM control_c", sqlCursorMissing("control_c")))
			})
		})
	}
}
