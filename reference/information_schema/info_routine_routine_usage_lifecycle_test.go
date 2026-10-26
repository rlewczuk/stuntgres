package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceInfoRoutineRoutineUsageLifecycle(t *testing.T) {
	rruScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, database string) {
		rruExec(t, p, b, c, "CREATE SCHEMA rru_private", "CREATE SCHEMA rru_moved",
			"CREATE FUNCTION rru_private.leaf_a() RETURNS integer RETURN 1", "CREATE FUNCTION rru_private.leaf_b() RETURNS integer RETURN 2",
			"CREATE FUNCTION rru_private.use_a() RETURNS integer RETURN rru_private.leaf_a()", "CREATE FUNCTION rru_private.use_b() RETURNS integer RETURN rru_private.leaf_b()",
			"CREATE FUNCTION rru_private.default_a(a integer DEFAULT rru_private.leaf_a()) RETURNS integer LANGUAGE SQL AS 'SELECT 1'",
			"CREATE FUNCTION rru_private.text_a() RETURNS integer LANGUAGE SQL AS 'SELECT rru_private.leaf_a()'",
			"CREATE FUNCTION rru_private.pl_a() RETURNS integer LANGUAGE plpgsql AS $$BEGIN RETURN rru_private.leaf_a(); END$$")
		id := func(schema, name, inputs string) string { return rruID(t, p, c, schema, name, inputs) }
		a, bid := id("rru_private", "leaf_a", ""), id("rru_private", "leaf_b", "")
		useA, useB, def := id("rru_private", "use_a", ""), id("rru_private", "use_b", ""), id("rru_private", "default_a", "integer")
		where := "specific_schema IN ('rru_private','rru_moved')"
		held := rsuHeld(t, p, c, b.Recorder, rruProjection(b.PG, where, nil).Query)
		check := func(want [][]any) {
			t.Helper()
			rruCheck(t, p, b, c, where, want)
			held(rruProjection(b.PG, where, want))
		}
		pair := func(schema, source, targetSchema, target string) []any {
			return rruRow(database, schema, source, targetSchema, target)
		}
		keep := pair("rru_private", useB, "rru_private", bid)
		check([][]any{pair("rru_private", useA, "rru_private", a), pair("rru_private", def, "rru_private", a), keep})
		rruExec(t, p, b, c, "ALTER FUNCTION rru_private.leaf_a() RENAME TO leaf_renamed", "ALTER FUNCTION rru_private.leaf_renamed() SET SCHEMA rru_moved", "ALTER FUNCTION rru_private.use_a() RENAME TO use_renamed", "ALTER FUNCTION rru_private.use_renamed() SET SCHEMA rru_moved")
		renamedA, renamedUse := id("rru_moved", "leaf_renamed", ""), id("rru_moved", "use_renamed", "")
		if a[len("leaf_a"):] != renamedA[len("leaf_renamed"):] || useA[len("use_a"):] != renamedUse[len("use_renamed"):] {
			t.Fatal("rename changed OID suffix")
		}
		check([][]any{pair("rru_moved", renamedUse, "rru_moved", renamedA), pair("rru_private", def, "rru_moved", renamedA), keep})
		rruExec(t, p, b, c, "DROP FUNCTION rru_moved.use_renamed()", "CREATE FUNCTION rru_moved.use_renamed() RETURNS integer RETURN rru_private.leaf_b()")
		replacement := id("rru_moved", "use_renamed", "")
		if replacement == renamedUse {
			t.Fatal("recreated identity reused")
		}
		check([][]any{pair("rru_moved", replacement, "rru_private", bid), pair("rru_private", def, "rru_moved", renamedA), keep})
		rruExec(t, p, b, c, "CREATE FUNCTION rru_private.unused() RETURNS integer RETURN 0", "DROP FUNCTION rru_private.unused()", "DROP FUNCTION rru_moved.leaf_renamed() CASCADE")
		check([][]any{pair("rru_moved", replacement, "rru_private", bid), keep})
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT proname FROM pg_proc WHERE pronamespace='rru_private'::regnamespace ORDER BY proname", []string{"proname"}, []string{"NAME"}, [][]any{{"leaf_b"}, {"pl_a"}, {"text_a"}, {"use_b"}}))
	})
}

func TestReferenceInfoRoutineRoutineUsageDiagnostics(t *testing.T) {
	rruScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, database string) {
		d := fixture.ControlsReferenceDiagnostic("42703", `column "missing" does not exist`)
		d.Position = "8"
		for _, prepared := range []bool{false, true} {
			s := fixture.ErrorDiagnostic("SELECT missing FROM information_schema.routine_routine_usage", d)
			s.SelectRows = true
			s.Prepared = prepared
			s.ErrorStage = "execute"
			if prepared {
				s.ErrorStage = "prepare"
			}
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
		}
		rruExec(t, p, b, c, "CREATE FUNCTION rru_diag_leaf() RETURNS integer RETURN 1", "CREATE FUNCTION rru_diag_use() RETURNS integer RETURN rru_diag_leaf()")
		want := [][]any{rruRow(database, "public", rruID(t, p, c, "public", "rru_diag_use", ""), "public", rruID(t, p, c, "public", "rru_diag_leaf", ""))}
		d = fixture.ControlsReferenceDiagnostic("2BP01", "cannot drop function rru_diag_leaf() because other objects depend on it")
		d.Detail = "function rru_diag_use() depends on function rru_diag_leaf()"
		d.Hint = "Use DROP ... CASCADE to drop the dependent objects too."
		for _, prepared := range []bool{false, true} {
			s := fixture.ErrorDiagnostic("DROP FUNCTION rru_diag_leaf() RESTRICT", d)
			s.Prepared = prepared
			s.ErrorStage = "execute"
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
			rruCheck(t, p, b, c, "specific_schema='public'", want)
		}
		if !b.PG {
			ctx, cancel := context.WithCancel(p.Ctx)
			cancel()
			rows, err := c.QueryContext(ctx, "SELECT * FROM information_schema.routine_routine_usage")
			if rows != nil {
				rows.Close()
				t.Fatal("canceled rows")
			}
			if !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
		}
	})
}
