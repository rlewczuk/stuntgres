package reference_test

import (
	"context"
	"database/sql"
	"reflect"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

// These are release-source hypotheses until Main records pinned observations:
// pg_sequence_last_value is VOLATILE even behind stored-view expansion, so
// consumer restrictions cannot cross the CTE producer, including NOT MATERIALIZED.
func TestReferencePGSequencesCTE(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			a, peer := fixture.LockConnections(t, p.Ctx, b.DB)
			for _, q := range []string{
				"CREATE SEQUENCE pgsv_cte_a", "CREATE SEQUENCE pgsv_cte_b",
				"CREATE VIEW pgsv_cte_view AS SELECT sequencename,last_value FROM pg_sequences",
				"CREATE VIEW pgsv_cte_nested AS SELECT sequencename,last_value FROM pgsv_cte_view",
			} {
				pgSequenceExec(t, p, b, a, q)
			}
			for _, source := range []string{"pg_sequences", "(SELECT sequencename,last_value FROM pg_sequences) d", "pgsv_cte_view", "pgsv_cte_nested"} {
				for _, hint := range []string{"", "MATERIALIZED", "NOT MATERIALIZED"} {
					prefix := "WITH q AS " + hint + " (SELECT sequencename,last_value FROM " + source + ") "
					for _, c := range []struct {
						name, tail     string
						columns, types []string
						rows           [][]any
						used           bool
					}{
						{"single", "SELECT sequencename,last_value FROM q WHERE sequencename='pgsv_cte_a'", []string{"sequencename", "last_value"}, []string{"NAME", "INT8"}, [][]any{{"pgsv_cte_a", nil}}, true},
						{"shared", "SELECT x.sequencename,x.last_value,y.last_value FROM q x JOIN q y ON x.sequencename=y.sequencename WHERE x.sequencename='pgsv_cte_a' AND y.sequencename='pgsv_cte_a'", []string{"sequencename", "last_value", "last_value"}, []string{"NAME", "INT8", "INT8"}, [][]any{{"pgsv_cte_a", nil, nil}}, true},
						{"unused", "SELECT 7 AS n", []string{"n"}, []string{"INT4"}, [][]any{{int64(7)}}, false},
					} {
						t.Run(source+"/"+hint+"/"+c.name, func(t *testing.T) {
							q := prefix + c.tail
							s := fixture.Select(q, c.columns, c.types, c.rows)
							fixture.PgDescriptionExecutorRows(t, p, b, a, s)
							fixture.PgDescriptionExecutorRows(t, p, b, a, fixture.Prepared(s))
							if !b.PG {
								return
							}
							for _, prepared := range []bool{false, true} {
								pgSequenceExec(t, p, b, a, "BEGIN")
								func() {
									defer a.ExecContext(context.Background(), "ROLLBACK")
									var x fixture.Executor = a
									if prepared {
										stmt, err := a.PrepareContext(p.Ctx, q)
										if err != nil {
											t.Fatal(err)
										}
										defer stmt.Close()
										pgSequencesCTELocks(t, p, a, peer, false)
										x = fixture.HeldStatement{Executor: a, Statement: stmt, SQL: q}
									}
									fixture.PgDescriptionExecutorRows(t, p, b, x, s)
									pgSequencesCTELocks(t, p, a, peer, c.used)
								}()
							}
							if c.used {
								pgSequencesCTEQueue(t, p, b, a, peer, s)
							}
						})
					}
				}
			}
		})
	}
}

func pgSequencesCTELocks(t *testing.T, p *fixture.Pair, a, peer *sql.Conn, used bool) {
	t.Helper()
	pid := fixture.LockPID(t, p.Ctx, a)
	rows, err := peer.QueryContext(p.Ctx, "SELECT c.relname,l.mode,l.granted FROM pg_locks l JOIN pg_class c ON c.oid=l.relation WHERE l.pid=$1 AND (c.relname='pgsv_cte_a' OR c.relname='pgsv_cte_b') ORDER BY c.relname,l.mode", pid)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var actual [][]any
	for rows.Next() {
		var name, mode string
		var granted bool
		if err := rows.Scan(&name, &mode, &granted); err != nil {
			t.Fatal(err)
		}
		actual = append(actual, []any{name, mode, granted})
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	var want [][]any
	if used {
		want = [][]any{{"pgsv_cte_a", "RowExclusiveLock", true}, {"pgsv_cte_b", "RowExclusiveLock", true}}
	}
	if !reflect.DeepEqual(actual, want) {
		t.Errorf("CTE helper frontier=%#v want %#v", actual, want)
	}
}

func pgSequencesCTEQueue(t *testing.T, p *fixture.Pair, b fixture.Backend, a, peer *sql.Conn, s fixture.Step) {
	t.Helper()
	pgSequenceExec(t, p, b, peer, "BEGIN")
	defer peer.ExecContext(context.Background(), "ROLLBACK")
	pgSequenceExec(t, p, b, peer, "ALTER SEQUENCE pgsv_cte_b RESTART 31")
	holder := fixture.LockPID(t, p.Ctx, peer)
	waiter := fixture.LockPID(t, p.Ctx, a)
	oid := fixture.LockOID(t, p.Ctx, "pgsv_cte_b")
	ctx, cancel := context.WithTimeout(p.Ctx, 10*time.Second)
	defer cancel()
	ch := make(chan error, 1)
	go func() {
		rows, err := a.QueryContext(ctx, s.Query)
		if rows != nil {
			for rows.Next() {
			}
			if err == nil {
				err = rows.Err()
			}
			rows.Close()
		}
		ch <- err
	}()
	joined := false
	defer func() {
		cancel()
		if !joined {
			<-ch
		}
	}()
	// b is rejected by every consumer; a real b helper wait distinguishes
	// volatile producer execution from merely comparing equal final rows.
	fixture.SequenceWaitBehind(t, ctx, waiter, holder, oid)
	var stopped bool
	if err := fixture.Admin.QueryRowContext(ctx, "SELECT pg_cancel_backend($1)", waiter).Scan(&stopped); err != nil || !stopped {
		t.Fatal("cancel acknowledgement", stopped, err)
	}
	err := <-ch
	joined = true
	want := fixture.ControlsReferenceDiagnostic("57014", "canceling statement due to user request")
	fixture.AssertDiagnostic(t, err, &want)
	pgSequenceExec(t, p, b, peer, "ROLLBACK")
	fixture.PgDescriptionExecutorRows(t, p, b, a, s)
}
