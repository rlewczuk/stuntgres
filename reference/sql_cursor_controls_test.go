package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"strings"
	"stuntgres"
	"testing"
	"time"
)

func TestReferenceSQLCursorCancellation(t *testing.T) {
	sqlCursorScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		sqlCursorSetup(t, p, b, c)
		sqlCursorStep(t, p, b, c, referenceExec("BEGIN", 0))
		if !b.pg {
			ctx, cancel := context.WithCancel(p.ctx)
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
			sqlCursorStep(t, p, b, c, referenceExec(declare, 0))
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
		sqlCursorStep(t, p, b, c, referenceExec("DECLARE canceled NO SCROLL CURSOR FOR SELECT i,pg_sleep(600) FROM cursor_rows ORDER BY i", 0))
		sqlCursorStep(t, p, b, c, referenceExec("SAVEPOINT cancel_s", 0))
		sqlCursorCancelDemand(t, p, c, "FETCH ALL FROM canceled")
		sqlCursorStep(t, p, b, c, referenceExec("ROLLBACK TO cancel_s", 0))
		sqlCursorError(t, p, b, c, "FETCH ALL FROM canceled", sqlCursorDiagnostic("55000", "portal \"canceled\" cannot be run"))
		sqlCursorStep(t, p, b, c, referenceExec("CLOSE canceled", 0))
	})
}

func sqlCursorCancelDemand(t *testing.T, p *referencePair, c *sql.Conn, query string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(p.ctx, 15*time.Second)
	defer cancel()
	var pid int
	if err := c.QueryRowContext(ctx, "SELECT pg_backend_pid()").Scan(&pid); err != nil {
		t.Fatal(err)
	}
	type outcome struct {
		err     error
		exposed bool
	}
	done := make(chan outcome, 1)
	go func() {
		rows, err := c.QueryContext(ctx, query)
		exposed := rows != nil
		if rows != nil {
			rows.Close()
		}
		done <- outcome{err, exposed}
	}()
	joinFailure := func(message string, err error) {
		cancel()
		result := <-done
		t.Fatalf("%s: %v; joined FETCH: %v", message, err, result.err)
	}
	for {
		var active bool
		if err := referenceAdmin.QueryRowContext(ctx, "SELECT COALESCE((SELECT state='active' AND query=$2 FROM pg_stat_activity WHERE pid=$1),FALSE)", pid, query).Scan(&active); err != nil {
			joinFailure("admission barrier", err)
		}
		if active {
			break
		}
		select {
		case result := <-done:
			t.Fatalf("FETCH completed before observed admission: %#v", result)
		default:
		}
	}
	var signaled bool
	if err := referenceAdmin.QueryRowContext(ctx, "SELECT pg_cancel_backend($1)", pid).Scan(&signaled); err != nil || !signaled {
		joinFailure("server cancellation", err)
	}
	result := <-done
	if result.exposed {
		t.Fatal("canceled FETCH exposed partial rows")
	}
	want := sqlCursorDiagnostic("57014", "canceling statement due to user request")
	referenceAssertDiagnostic(t, result.err, &want)
}

func TestReferenceSQLCursorNativeControls(t *testing.T) {
	for _, replacement := range []string{"reset", "restore", "failed-restore"} {
		t.Run(replacement, func(t *testing.T) {
			sqlCursorScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
				sqlCursorSetup(t, p, b, c)
				sqlCursorStep(t, p, b, c, referenceExec("BEGIN", 0))
				sqlCursorStep(t, p, b, c, referenceExec("DECLARE control_c NO SCROLL CURSOR FOR SELECT i,t FROM cursor_rows ORDER BY i", 0))
				query := "FETCH 1 FROM control_c"
				ctx := context.WithValue(p.ctx, referenceRecorderKey{}, b.recorder)
				rows, err := c.QueryContext(ctx, query)
				if err != nil {
					t.Fatal(err)
				}
				defer rows.Close()
				frozen := sequenceFrozenRows{referenceExecutor: c, rows: rows, query: query}
				// pq owns a live wire result until consumption; native eager batches
				// alone promise detached reads across a host catalog replacement.
				if b.pg {
					sqlCursorStep(t, p, b, frozen, sqlCursorBatch(query, [][]any{{int64(1), "one"}}, true))
				}
				sqlCursorStep(t, p, b, c, referenceExec("INSERT INTO cursor_rows VALUES(4,'private')", 1))
				if b.pg {
					// SQL transaction end is PostgreSQL's real counterpart, not a reset API.
					sqlCursorStep(t, p, b, c, referenceExec("ROLLBACK", 0))
				} else {
					var dump bytes.Buffer
					if err := stuntgres.Dump(p.ctx, p.native, &dump); err != nil {
						t.Fatal(err)
					}
					if strings.Contains(dump.String(), "control_c") || strings.Contains(dump.String(), "private") || strings.Contains(dump.String(), "DECLARE") {
						t.Fatalf("Dump contains session state/private write: %s", dump.String())
					}
					switch replacement {
					case "reset":
						err = stuntgres.Reset(p.ctx, p.native)
					case "restore":
						err = stuntgres.Restore(p.ctx, p.native, strings.NewReader("CREATE TABLE cursor_rows(i INTEGER NOT NULL,t TEXT); INSERT INTO cursor_rows VALUES(99,'replacement')"))
					case "failed-restore":
						err = stuntgres.Restore(p.ctx, p.native, strings.NewReader("CREATE TABLE bad(i INTEGER); INSERT INTO bad VALUES($1)"))
						want := sqlCursorDiagnostic("42P02", "parameters are not allowed in restore")
						want.Position = "53"
						referenceAssertDiagnostic(t, err, &want)
						err = nil
					}
					if err != nil {
						t.Fatal(err)
					}
				}
				if !b.pg {
					sqlCursorStep(t, p, b, frozen, sqlCursorBatch(query, [][]any{{int64(1), "one"}}, false))
				}
				if !b.pg && replacement == "failed-restore" {
					sqlCursorStep(t, p, b, c, sqlCursorBatch(query, [][]any{{int64(2), nil}}, false))
					return
				}
				if !b.pg {
					result, err := c.ExecContext(p.ctx, "FETCH ALL FROM control_c")
					if result != nil {
						t.Fatal("fenced FETCH exposed result")
					}
					transactionOrdinary(t, err, "stuntgres: catalog replaced before publication")
					sqlCursorStep(t, p, b, c, referenceExec("ROLLBACK", 0))
				}
				sqlCursorStep(t, p, b, c, referenceErrorDiagnostic("FETCH ALL FROM control_c", sqlCursorMissing("control_c")))
			})
		})
	}
}
