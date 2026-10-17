package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceQuerySearchLifecycle(t *testing.T) {
	searchReference(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, mode := range searchModes {
			t.Run(mode+" late publication failure", func(t *testing.T) { searchFailure(t, p, b, c, mode) })
			t.Run(mode+" active cancellation", func(t *testing.T) { searchCancellation(t, p, b, c, mode) })
			t.Run(mode+" no persistent runtime state", func(t *testing.T) { searchTransient(t, p, b, c, mode) })
		}
	})
}

func searchFailure(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, mode string) {
	t.Helper()
	run := subqueryRun(t, p, b, c)
	base := recursiveCTELate + searchClause(mode, "n")
	division := fixture.ControlsReferenceDiagnostic("22012", "division by zero")
	cteExecutionFailure(t, p, b, c, base+"SELECT n,v FROM r", division)
	name := "search_failure_" + strings.ToLower(mode)
	run(fixture.Exec("CREATE TABLE "+name+"(n INT,v INT)", 0))
	run(fixture.Exec("INSERT INTO "+name+" VALUES(99,99)", 1))
	for _, prepared := range []bool{false, true} {
		for _, q := range []string{"INSERT INTO " + name + " " + base + "SELECT n,v FROM r", "CREATE TABLE " + name + "_ctas AS " + base + "SELECT n,v FROM r"} {
			s := fixture.ErrorDiagnostic(q, division)
			s.Prepared = prepared
			s.ErrorStage = "execute"
			run(s)
		}
		run(cteBag("SELECT * FROM "+name, []string{"n", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(99), int64(99)}}))
		run(cteBag("SELECT relname FROM pg_catalog.pg_class WHERE relname='"+name+"_ctas'", []string{"relname"}, []string{"NAME"}, nil))
	}
	run(fixture.Exec("CREATE TABLE "+name+"_bound(n INT)", 0))
	run(fixture.Exec("INSERT INTO "+name+"_bound VALUES(2)", 1))
	body := "WITH RECURSIVE r(n,v) AS (SELECT 1,1 UNION ALL SELECT n+1,1/(3-n) FROM r WHERE n<(SELECT n FROM " + name + "_bound))" + searchClause(mode, "n") + "SELECT n,v FROM r"
	run(fixture.Exec("CREATE MATERIALIZED VIEW "+name+"_mv AS "+body, 2))
	run(fixture.Exec("UPDATE "+name+"_bound SET n=4", 1))
	subqueryBoth(run, fixture.ErrorDiagnostic("REFRESH MATERIALIZED VIEW "+name+"_mv", division))
	subqueryBoth(run, cteBag("SELECT * FROM "+name+"_mv", []string{"n", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(1)}, {int64(2), int64(0)}}))
	q := "WITH RECURSIVE r(n,v) AS (SELECT 1,1 UNION ALL SELECT n+1,1/(3-n) FROM r WHERE n<$1::int)" + searchClause(mode, "n") + "SELECT n,v FROM r ORDER BY seq"
	stmt, err := c.PrepareContext(p.Ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
	s := fixture.ErrorDiagnostic(q, division, int64(4))
	s.SelectRows = true
	s.ErrorStage = "execute"
	fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
	fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Select(q, []string{"n", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(1)}, {int64(2), int64(0)}}, int64(2)))
}

func searchCancellation(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, mode string) {
	t.Helper()
	run := subqueryRun(t, p, b, c)
	name := "search_cancel_" + strings.ToLower(mode)
	run(fixture.Exec("CREATE TABLE "+name+"(n INT)", 0))
	run(fixture.Exec("INSERT INTO "+name+" VALUES(99)", 1))
	// No growing user scalar, cycle detector or eventual integer overflow: only
	// the genuine generated sequence changes in the infinite recursion.
	base := "WITH RECURSIVE r(n) AS (SELECT 1 UNION ALL SELECT n FROM r WHERE $1::bool)" + searchClause(mode, "n")
	q := base + "SELECT count(*) AS count FROM r"
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
			fixture.BinaryConversionActiveCancel(t, p, b, c, q, "recursiveUnion", func(ctx context.Context) fixture.RelationOutcome {
				if b.PG {
					result, err := x.ExecContext(ctx, q, true)
					return fixture.RelationOutcome{Result: result, Err: err}
				}
				rows, err := x.QueryContext(ctx, q, true)
				return fixture.RelationOutcome{Rows: rows, Err: err}
			})
			fixture.RunStep(t, p.Ctx, x, b.Recorder, fixture.Select(q, []string{"count"}, []string{"INT8"}, recursiveCTEInts(1), false))
			run(fixture.Select("SELECT 7 AS n", []string{"n"}, []string{"INT4"}, recursiveCTEInts(7)))
		})
	}
	for _, target := range []string{"INSERT INTO " + name + " " + base + "SELECT n FROM r", "CREATE TABLE " + name + "_ctas AS " + base + "SELECT n FROM r"} {
		fixture.BinaryConversionActiveCancel(t, p, b, c, target, "recursiveUnion", func(ctx context.Context) fixture.RelationOutcome {
			result, err := c.ExecContext(ctx, target, true)
			return fixture.RelationOutcome{Result: result, Err: err}
		})
		run(cteBag("SELECT n FROM "+name, []string{"n"}, []string{"INT4"}, recursiveCTEInts(99)))
		run(cteBag("SELECT relname FROM pg_catalog.pg_class WHERE relname='"+name+"_ctas'", []string{"relname"}, []string{"NAME"}, nil))
		run(fixture.Select("SELECT 7 AS n", []string{"n"}, []string{"INT4"}, recursiveCTEInts(7)))
	}
	if !b.PG {
		for _, expired := range []bool{false, true} {
			ctx, cancel := context.WithCancel(p.Ctx)
			var want error = context.Canceled
			if expired {
				cancel()
				ctx, cancel = context.WithDeadline(p.Ctx, time.Now().Add(-time.Second))
				want = context.DeadlineExceeded
			} else {
				cancel()
			}
			rows, err := c.QueryContext(ctx, q, false)
			cancel()
			if rows != nil {
				rows.Close()
				t.Fatal("precanceled query published Rows")
			}
			fixture.LexicalOrdinary(t, err, want)
		}
	}
}

func searchTransient(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, mode string) {
	t.Helper()
	var before []byte
	if !b.PG {
		before = fixture.ViewDump(t, p)
	}
	q := "WITH RECURSIVE search_transient_probe(n) AS (SELECT 1 UNION ALL SELECT n FROM search_transient_probe WHERE FALSE)" + searchClause(mode, "n") + "SELECT n FROM search_transient_probe"
	run := subqueryRun(t, p, b, c)
	subqueryBoth(run, fixture.Select(q, []string{"n"}, []string{"INT4"}, recursiveCTEInts(1)))
	for _, catalog := range []struct{ query, column string }{
		{"SELECT relname FROM pg_catalog.pg_class WHERE relname='search_transient_probe'", "relname"},
		{"SELECT typname FROM pg_catalog.pg_type WHERE typname='search_transient_probe'", "typname"},
	} {
		run(cteBag(catalog.query, []string{catalog.column}, []string{"NAME"}, nil))
	}
	if !b.PG {
		var after bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &after); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(before, after.Bytes()) {
			t.Fatal("transient SEARCH changed persistent state")
		}
	}
}
