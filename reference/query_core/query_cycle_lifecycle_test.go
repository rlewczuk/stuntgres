package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"testing"
	"time"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceQueryCycleLifecycle(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			c := cycleConn(t, p, backend)
			t.Run("late failure atomicity", func(t *testing.T) { cycleFailure(t, p, backend, c) })
			t.Run("running cancellation", func(t *testing.T) { cycleCancellation(t, p, backend, c) })
			t.Run("transient and retained", func(t *testing.T) { cycleRetained(t, p, backend, c) })
		})
	}
}

func cycleFailure(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	run := subqueryRun(t, p, b, c)
	division := fixture.ControlsReferenceDiagnostic("22012", "division by zero")
	cteExecutionFailure(t, p, b, c, cycleLate+"SELECT n,v FROM r", division)
	run(fixture.Exec("CREATE TABLE cycle_failure_sink(n INT,v INT)", 0))
	run(fixture.Exec("INSERT INTO cycle_failure_sink VALUES(99,99)", 1))
	for _, prepared := range []bool{false, true} {
		s := fixture.ErrorDiagnostic("INSERT INTO cycle_failure_sink "+cycleLate+"SELECT n,v FROM r", division)
		s.Prepared = prepared
		s.ErrorStage = "execute"
		run(s)
		run(cteBag("SELECT * FROM cycle_failure_sink", []string{"n", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(99), int64(99)}}))
		s = fixture.ErrorDiagnostic("CREATE TABLE cycle_failure_ctas AS "+cycleLate+"SELECT n,v FROM r", division)
		s.Prepared = prepared
		s.ErrorStage = "execute"
		run(s)
		run(cteBag("SELECT relname FROM pg_catalog.pg_class WHERE relname='cycle_failure_ctas'", []string{"relname"}, []string{"NAME"}, nil))
	}
	run(fixture.Exec("CREATE TABLE cycle_failure_bound(n INT)", 0))
	run(fixture.Exec("INSERT INTO cycle_failure_bound VALUES(2)", 1))
	body := "WITH RECURSIVE r(n,v) AS (SELECT 1,1 UNION ALL SELECT n+1,1/(3-n) FROM r WHERE n<(SELECT n FROM cycle_failure_bound)) CYCLE n SET cyc USING path SELECT n,v FROM r"
	run(fixture.Exec("CREATE MATERIALIZED VIEW cycle_failure_mv AS "+body, 2))
	run(fixture.Exec("UPDATE cycle_failure_bound SET n=4", 1))
	subqueryBoth(run, fixture.ErrorDiagnostic("REFRESH MATERIALIZED VIEW cycle_failure_mv", division))
	subqueryBoth(run, cteBag("SELECT * FROM cycle_failure_mv", []string{"n", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(1)}, {int64(2), int64(0)}}))
	tx, err := c.BeginTx(p.Ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	fixture.RunStep(t, p.Ctx, tx, b.Recorder, fixture.Exec("INSERT INTO cycle_failure_sink VALUES(7,7)", 1))
	subqueryQueryFailure(t, p, b, tx, cycleLate+"SELECT n,v FROM r", division)
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	subqueryBoth(run, cteBag("SELECT * FROM cycle_failure_sink", []string{"n", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(99), int64(99)}}))
	subqueryBoth(run, cteBag(cycleFinite+"SELECT n FROM r", []string{"n"}, []string{"INT4"}, recursiveCTEInts(1, 2, 3)))
}

func cycleCancellation(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	run := subqueryRun(t, p, b, c)
	run(fixture.Exec("CREATE TABLE cycle_cancel_sink(n INT)", 0))
	run(fixture.Exec("INSERT INTO cycle_cancel_sink VALUES(99)", 1))
	body := "WITH RECURSIVE r(n) AS (SELECT 1 UNION ALL SELECT n+1 FROM r WHERE $1::int IS NULL OR n<$1) CYCLE n SET cyc USING path "
	q := body + "SELECT sum(n) AS total,count(*) AS count,bool_or(cyc) AS marked FROM r"
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "same held"}[prepared], func(t *testing.T) {
			var x fixture.Executor = c
			if prepared {
				stmt, err := c.PrepareContext(p.Ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				x = fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
			}
			fixture.BinaryConversionActiveCancel(t, p, b, c, q, stuntgres.WorkRecursiveUnion, func(ctx context.Context) fixture.RelationOutcome {
				if b.PG {
					result, err := x.ExecContext(ctx, q, nil)
					return fixture.RelationOutcome{Result: result, Err: err}
				}
				rows, err := x.QueryContext(ctx, q, nil)
				return fixture.RelationOutcome{Rows: rows, Err: err}
			})
			fixture.RunStep(t, p.Ctx, x, b.Recorder, fixture.Select(q, []string{"total", "count", "marked"}, []string{"INT8", "INT8", "BOOL"}, [][]any{{int64(6), int64(3), false}}, int64(3)))
			subqueryRun(t, p, b, c)(fixture.Select("SELECT 7 AS n", []string{"n"}, []string{"INT4"}, recursiveCTEInts(7)))
		})
	}
	for _, tc := range []struct{ name, q string }{
		{"insert", "INSERT INTO cycle_cancel_sink " + body + "SELECT n FROM r"},
		{"ctas", "CREATE TABLE cycle_cancel_ctas AS " + body + "SELECT n FROM r"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fixture.BinaryConversionActiveCancel(t, p, b, c, tc.q, stuntgres.WorkRecursiveUnion, func(ctx context.Context) fixture.RelationOutcome {
				result, err := c.ExecContext(ctx, tc.q, nil)
				return fixture.RelationOutcome{Result: result, Err: err}
			})
			run := subqueryRun(t, p, b, c)
			run(cteBag("SELECT n FROM cycle_cancel_sink", []string{"n"}, []string{"INT4"}, recursiveCTEInts(99)))
			run(cteBag("SELECT relname FROM pg_catalog.pg_class WHERE relname='cycle_cancel_ctas'", []string{"relname"}, []string{"NAME"}, nil))
			run(fixture.Select("SELECT 7 AS n", []string{"n"}, []string{"INT4"}, recursiveCTEInts(7)))
		})
	}
	if !b.PG {
		for _, expired := range []bool{false, true} {
			ctx, cancel := context.WithCancel(p.Ctx)
			want := error(context.Canceled)
			if expired {
				cancel()
				ctx, cancel = context.WithDeadline(p.Ctx, time.Now().Add(-time.Second))
				want = context.DeadlineExceeded
			} else {
				cancel()
			}
			rows, err := c.QueryContext(ctx, q, int64(3))
			cancel()
			if rows != nil {
				rows.Close()
				t.Fatal("cancelled call published Rows")
			}
			fixture.LexicalOrdinary(t, err, want)
		}
	}
	subqueryBoth(run, cteBag(cycleFinite+"SELECT n,cyc,path FROM r", []string{"n", "cyc", "path"}, []string{"INT4", "BOOL", "_RECORD"}, [][]any{{int64(1), false, "{(1)}"}, {int64(2), false, "{(1),(2)}"}, {int64(3), false, "{(1),(2),(3)}"}}))
}

func cycleRetained(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	run := subqueryRun(t, p, b, c)
	var before bytes.Buffer
	if !b.PG {
		if err := stuntgres.Dump(p.Ctx, p.Native, &before); err != nil {
			t.Fatal(err)
		}
	}
	catalog := "SELECT relname FROM pg_catalog.pg_class WHERE relname='cycle_transient_probe'"
	run(cteBag(catalog, []string{"relname"}, []string{"NAME"}, nil))
	q := "WITH RECURSIVE cycle_transient_probe(n) AS (SELECT 1 UNION ALL SELECT n FROM cycle_transient_probe) CYCLE n SET cyc USING path SELECT * FROM cycle_transient_probe"
	want := [][]any{{int64(1), false, "{(1)}"}, {int64(1), true, "{(1),(1)}"}}
	subqueryBoth(run, cteBag(q, []string{"n", "cyc", "path"}, []string{"INT4", "BOOL", "_RECORD"}, want))
	run(cteBag(catalog, []string{"relname"}, []string{"NAME"}, nil))
	typeCatalog := "SELECT typname FROM pg_catalog.pg_type WHERE typname='cycle_transient_probe'"
	run(cteBag(typeCatalog, []string{"typname"}, []string{"NAME"}, nil))
	if b.PG {
		return
	}
	var after bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &after); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before.Bytes(), after.Bytes()) {
		t.Fatal("transient CYCLE changed persistent dump")
	}
	rows, err := c.QueryContext(p.Ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	metadata := cycleMetadata(b, []string{"n", "cyc", "path"}, []string{"INT4", "BOOL", "_RECORD"})
	metadata(t, rows)
	if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(before.Bytes())); err != nil {
		t.Fatal(err)
	}
	metadata(t, rows)
	var got [][]any
	for rows.Next() {
		var n, cyc, path any
		if err := rows.Scan(&n, &cyc, &path); err != nil {
			t.Fatal(err)
		}
		got = append(got, []any{n, cyc, path})
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	cycleAssertBag(t, got, want)
}
