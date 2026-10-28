package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"reflect"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceQueryRecursiveCTELifecycle(t *testing.T) {
	t.Run("held arguments and dependencies", recursiveCTEHeldLifecycle)
	t.Run("running cancellation", recursiveCTECancellation)
	t.Run("failure and transaction", recursiveCTEFailure)
	t.Run("transient and retained", recursiveCTERetained)
	t.Run("physical authority", recursiveCTEAuthority)
	t.Run("search path and type freshness", recursiveCTEPathFreshness)
}

func recursiveCTEHeldLifecycle(t *testing.T) {
	recursiveCTEReference(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := subqueryRun(t, p, b, c)
		q := "WITH RECURSIVE r(n) AS (SELECT 1 UNION ALL SELECT n+1 FROM r WHERE n<$1) SELECT n FROM r"
		held := fixture.ViewHold(t, p, b, q)
		for _, tc := range []struct {
			arg  any
			rows [][]any
		}{{int64(2), recursiveCTEInts(1, 2)}, {int64(4), recursiveCTEInts(1, 2, 3, 4)}, {nil, recursiveCTEInts(1)}, {int64(2), recursiveCTEInts(1, 2)}} {
			s := cteBag(q, []string{"n"}, []string{"INT4"}, tc.rows, tc.arg)
			s.CheckMetadata = recursiveCTEMetadata(b, s.Columns, s.Types)
			fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
		}
		run(fixture.Exec("CREATE TABLE recursive_live(src INT,dst INT)", 0))
		run(fixture.Exec("INSERT INTO recursive_live VALUES(1,2)", 1))
		q = "WITH RECURSIVE r(n) AS (SELECT 1 UNION SELECT dst FROM recursive_live e JOIN r ON e.src=r.n) SELECT n FROM r"
		held = fixture.ViewHold(t, p, b, q)
		fixture.RunStep(t, p.Ctx, held, b.Recorder, cteBag(q, []string{"n"}, []string{"INT4"}, recursiveCTEInts(1, 2)))
		run(fixture.Exec("INSERT INTO recursive_live VALUES(2,3)", 1))
		fixture.RunStep(t, p.Ctx, held, b.Recorder, cteBag(q, []string{"n"}, []string{"INT4"}, recursiveCTEInts(1, 2, 3)))
		run(fixture.Exec("DROP TABLE recursive_live", 0))
		want := fixture.ControlsReferenceDiagnostic("42P01", `relation "recursive_live" does not exist`)
		want.Position = fixture.Position(strings.Index(q, "recursive_live") + 1)
		s := fixture.ErrorDiagnostic(q, want)
		s.SelectRows = true
		s.ErrorStage = "execute"
		fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
	})
}

func recursiveCTECancellation(t *testing.T) {
	recursiveCTEReference(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := subqueryRun(t, p, b, c)
		run(fixture.Exec("CREATE TABLE recursive_cancel_sink(n INT)", 0))
		run(fixture.Exec("INSERT INTO recursive_cancel_sink VALUES(99)", 1))
		// NULL keeps the recursive row constant forever (no overflow escape).
		// The same held statement must recover with a finite terminal argument.
		q := "WITH RECURSIVE r(n) AS (SELECT 1 UNION ALL SELECT CASE WHEN $1::int IS NULL THEN n ELSE n+1 END FROM r WHERE $1::int IS NULL OR n<$1) SELECT sum(n) AS total FROM r"
		for _, prepared := range []bool{false, true} {
			var x fixture.Executor = c
			if prepared {
				stmt, err := c.PrepareContext(p.Ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				x = fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
			}
			// The shared helper witnesses real work admission, joins every exit
			// and asserts no published result/cursor before held-statement reuse.
			// PostgreSQL retains independently observed server cancellation.
			fixture.BinaryConversionActiveCancel(t, p, b, c, q, stuntgres.WorkRecursiveUnion, func(ctx context.Context) fixture.RelationOutcome {
				if b.PG {
					result, err := x.ExecContext(ctx, q, nil)
					return fixture.RelationOutcome{Result: result, Err: err}
				}
				rows, err := x.QueryContext(ctx, q, nil)
				return fixture.RelationOutcome{Rows: rows, Err: err}
			})
			fixture.RunStep(t, p.Ctx, x, b.Recorder, fixture.Select(q, []string{"total"}, []string{"INT8"}, recursiveCTEInts(3), int64(2)))
			run(fixture.Select("SELECT 7 AS n", []string{"n"}, []string{"INT4"}, recursiveCTEInts(7)))
		}
		insert := "INSERT INTO recursive_cancel_sink WITH RECURSIVE r(n) AS (SELECT 1 UNION ALL SELECT n FROM r) SELECT n FROM r"
		fixture.BinaryConversionActiveCancel(t, p, b, c, insert, stuntgres.WorkRecursiveUnion, func(ctx context.Context) fixture.RelationOutcome {
			result, err := c.ExecContext(ctx, insert)
			return fixture.RelationOutcome{Result: result, Err: err}
		})
		run(cteBag("SELECT n FROM recursive_cancel_sink", []string{"n"}, []string{"INT4"}, recursiveCTEInts(99)))
	})
}

func recursiveCTEFailure(t *testing.T) {
	recursiveCTEReference(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := subqueryRun(t, p, b, c)
		division := fixture.ControlsReferenceDiagnostic("22012", "division by zero")
		cteExecutionFailure(t, p, b, c, recursiveCTELate+"SELECT * FROM r", division)
		overflow := fixture.ControlsReferenceDiagnostic("22003", "integer out of range")
		cteExecutionFailure(t, p, b, c, "WITH RECURSIVE r(n) AS (SELECT 2147483646 UNION ALL SELECT n+1 FROM r) SELECT * FROM r", overflow)
		run(fixture.Exec("CREATE TABLE recursive_tx(n INT)", 0))
		run(fixture.Exec("INSERT INTO recursive_tx VALUES(99)", 1))
		tx, err := c.BeginTx(p.Ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		fixture.RunStep(t, p.Ctx, tx, b.Recorder, fixture.Exec("INSERT INTO recursive_tx VALUES(7)", 1))
		subqueryQueryFailure(t, p, b, tx, recursiveCTELate+"SELECT * FROM r", division)
		if err := tx.Rollback(); err != nil {
			t.Fatal(err)
		}
		subqueryBoth(run, cteBag("SELECT n FROM recursive_tx", []string{"n"}, []string{"INT4"}, recursiveCTEInts(99)))
		subqueryBoth(run, cteBag(recursiveCTEFinite, []string{"n"}, []string{"INT4"}, recursiveCTEInts(1, 2, 3)))
	})
}

func recursiveCTERetained(t *testing.T) {
	recursiveCTEReference(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := subqueryRun(t, p, b, c)
		var before bytes.Buffer
		if !b.PG {
			if err := stuntgres.Dump(p.Ctx, p.Native, &before); err != nil {
				t.Fatal(err)
			}
		}
		catalog := "SELECT relname FROM pg_catalog.pg_class WHERE relname='recursive_transient_probe'"
		subqueryBoth(run, cteBag(catalog, []string{"relname"}, []string{"NAME"}, nil))
		q := "WITH RECURSIVE recursive_transient_probe(n) AS (SELECT 1 UNION ALL SELECT n+1 FROM recursive_transient_probe WHERE n<3) SELECT n FROM recursive_transient_probe"
		subqueryBoth(run, cteBag(q, []string{"n"}, []string{"INT4"}, recursiveCTEInts(1, 2, 3)))
		missing := fixture.ControlsReferenceDiagnostic("42P01", `relation "recursive_transient_probe" does not exist`)
		missing.Position = "15"
		subqueryAnalysisFailure(run, "SELECT * FROM recursive_transient_probe", missing)
		subqueryBoth(run, cteBag(catalog, []string{"relname"}, []string{"NAME"}, nil))
		if b.PG {
			return
		}
		var after bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &after); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(before.Bytes(), after.Bytes()) {
			t.Fatal("transient worktable persisted")
		}
		rows, err := c.QueryContext(p.Ctx, recursiveCTEFinite+" ORDER BY n")
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		recursiveCTEMetadata(b, []string{"n"}, []string{"INT4"})(t, rows)
		run(fixture.Exec("CREATE TABLE recursive_retained(n INT)", 0))
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(before.Bytes())); err != nil {
			t.Fatal(err)
		}
		recursiveCTEMetadata(b, []string{"n"}, []string{"INT4"})(t, rows)
		var got [][]any
		for rows.Next() {
			var n any
			if err := rows.Scan(&n); err != nil {
				t.Fatal(err)
			}
			got = append(got, []any{n})
		}
		if rows.Err() != nil || !reflect.DeepEqual(got, recursiveCTEInts(1, 2, 3)) {
			t.Fatal(got, rows.Err())
		}
	})
}

func recursiveCTEAuthority(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"recursive_reader"})
	recursiveCTEReference(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := subqueryRun(t, p, b, c)
		run(fixture.Exec("CREATE TABLE recursive_acl(n INT,secret INT)", 0))
		run(fixture.Exec("INSERT INTO recursive_acl VALUES(1,8)", 1))
		run(fixture.Exec("CREATE ROLE recursive_reader", 0))
		if b.PG {
			run(fixture.Exec("GRANT USAGE ON SCHEMA public TO recursive_reader", 0))
		}
		run(fixture.Exec("GRANT SELECT(n) ON recursive_acl TO recursive_reader", 0))
		run(fixture.Exec("SET SESSION AUTHORIZATION recursive_reader", 0))
		defer c.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION")
		for _, suffix := range []string{"", " LIMIT 0", " WHERE FALSE"} {
			q := "WITH RECURSIVE r(n) AS (SELECT secret FROM recursive_acl UNION SELECT n FROM r) SELECT n FROM r" + suffix
			s := fixture.ErrorDiagnostic(q, fixture.ControlsReferenceDiagnostic("42501", "permission denied for table recursive_acl"))
			s.SelectRows = true
			s.ErrorStage = "execute"
			subqueryBoth(run, s)
		}
		subqueryBoth(run, cteBag("WITH RECURSIVE r(n) AS (SELECT secret FROM recursive_acl UNION SELECT n FROM r) SELECT 7 AS n", []string{"n"}, []string{"INT4"}, recursiveCTEInts(7)))
		subqueryBoth(run, cteBag("WITH RECURSIVE r(n) AS (SELECT n FROM recursive_acl UNION SELECT n FROM r) SELECT n FROM r", []string{"n"}, []string{"INT4"}, recursiveCTEInts(1)))
	})
}

func recursiveCTEPathFreshness(t *testing.T) {
	fixture.SchemaReferenceCleanup(t, "recursive_path_a")
	fixture.SchemaReferenceCleanup(t, "recursive_path_b")
	recursiveCTEReference(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := subqueryRun(t, p, b, c)
		for _, s := range []fixture.Step{
			fixture.Exec("CREATE SCHEMA recursive_path_a", 0), fixture.Exec("CREATE SCHEMA recursive_path_b", 0),
			fixture.Exec("CREATE TABLE recursive_path_a.seed(n INT)", 0), fixture.Exec("CREATE TABLE recursive_path_b.seed(n INT)", 0),
			fixture.Exec("INSERT INTO recursive_path_a.seed VALUES(1)", 1), fixture.Exec("INSERT INTO recursive_path_b.seed VALUES(2)", 1),
			fixture.Exec("SET search_path TO recursive_path_a,public", 0),
		} {
			run(s)
		}
		q := "WITH RECURSIVE r(n) AS (SELECT n FROM seed UNION ALL SELECT n+1 FROM r WHERE n<3) SELECT n FROM r"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		fixture.RunStep(t, p.Ctx, held, b.Recorder, cteBag(q, []string{"n"}, []string{"INT4"}, recursiveCTEInts(1, 2, 3)))
		run(fixture.Exec("SET search_path TO recursive_path_b,public", 0))
		fixture.RunStep(t, p.Ctx, held, b.Recorder, cteBag(q, []string{"n"}, []string{"INT4"}, recursiveCTEInts(2, 3)))
		run(fixture.Exec("ALTER TABLE recursive_path_b.seed ADD COLUMN ignored INT", 0))
		fixture.RunStep(t, p.Ctx, held, b.Recorder, cteBag(q, []string{"n"}, []string{"INT4"}, recursiveCTEInts(2, 3)))
		run(fixture.Exec("DROP TABLE recursive_path_b.seed", 0))
		run(fixture.Exec("CREATE TABLE recursive_path_b.seed(n BIGINT)", 0))
		run(fixture.Exec("INSERT INTO recursive_path_b.seed VALUES(2)", 1))
		want := fixture.ControlsReferenceDiagnostic("0A000", "cached plan must not change result type")
		s := fixture.ErrorDiagnostic(q, want)
		s.SelectRows = true
		s.ErrorStage = "execute"
		fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
		// A newly analyzed invocation gets the new INT8 anchor metadata.
		subqueryBoth(run, cteBag(q, []string{"n"}, []string{"INT8"}, recursiveCTEInts(2, 3)))
		run(fixture.Exec("SET search_path TO public", 0))
		run(fixture.Exec("DROP SCHEMA recursive_path_a CASCADE", 0))
		run(fixture.Exec("DROP SCHEMA recursive_path_b CASCADE", 0))
	})
}
