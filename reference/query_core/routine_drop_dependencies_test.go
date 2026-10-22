package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func routineDropDependencySetup(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	routineExec(t, p, b, c,
		"CREATE FUNCTION rd_dep(integer) RETURNS integer LANGUAGE SQL AS $$SELECT $1+1$$",
		"CREATE FUNCTION rd_dep(text) RETURNS bigint LANGUAGE SQL AS $$SELECT 104::bigint$$",
		"CREATE VIEW rd_v AS SELECT rd_dep(4) AS v",
		"CREATE VIEW rd_v2 AS SELECT v FROM rd_v",
		"CREATE VIEW rd_vt AS SELECT rd_dep('x'::text) AS v",
		"CREATE TABLE rd_data(x integer DEFAULT rd_dep(4), CONSTRAINT rd_positive CHECK(rd_dep(x)>0))")
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO rd_data VALUES (7)", 1))
}

func routineDropDependencyError() fixture.Diagnostic {
	d := fixture.ControlsReferenceDiagnostic("2BP01", "cannot drop function rd_dep(integer) because other objects depend on it")
	d.Detail = "view rd_v depends on function rd_dep(integer)\nview rd_v2 depends on view rd_v\ndefault value for column x of table rd_data depends on function rd_dep(integer)\nconstraint rd_positive on table rd_data depends on function rd_dep(integer)"
	d.Hint = "Use DROP ... CASCADE to drop the dependent objects too."
	return d
}

func routineDropDependencyCascadeNotice() fixture.Diagnostic {
	d := routineDropNotice("drop cascades to 4 other objects")
	d.Detail = "drop cascades to view rd_v\ndrop cascades to view rd_v2\ndrop cascades to default value for column x of table rd_data\ndrop cascades to constraint rd_positive on table rd_data"
	return d
}

func routineDropDependencySurvivors(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	routineValue(t, p, b, c, "SELECT rd_dep('x'::text) AS v", "v", "INT8", int64(104))
	routineValueWithNativeNullability(t, p, b, c, "SELECT v FROM rd_vt", "v", "INT8", int64(104), true, true)
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT x FROM rd_data ORDER BY x", []string{"x"}, []string{"INT4"}, [][]any{{int64(7)}}))
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT count(*) AS n FROM pg_catalog.pg_attrdef WHERE adrelid='rd_data'::regclass", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT count(*) AS n FROM pg_catalog.pg_constraint WHERE conrelid='rd_data'::regclass", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Error("SELECT * FROM rd_v", "42P01"))
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Error("SELECT * FROM rd_v2", "42P01"))
}

func TestReferenceRoutineDropDependencies(t *testing.T) {
	routineDropScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, n *[]fixture.Diagnostic) {
		routineDropDependencySetup(t, p, b, c)
		for _, q := range []string{"DROP FUNCTION rd_dep(integer)", "DROP FUNCTION rd_dep(integer) RESTRICT", "DROP FUNCTION IF EXISTS rd_dep(integer)"} {
			routineDropError(t, p, b, c, q, routineDropDependencyError())
		}
		routineValue(t, p, b, c, "SELECT rd_dep(4) AS v", "v", "INT4", int64(5))
		routineValueWithNativeNullability(t, p, b, c, "SELECT v FROM rd_v2", "v", "INT4", int64(5), true, true)
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO rd_data DEFAULT VALUES", 1))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("DELETE FROM rd_data WHERE x=5", 1))
		routineExec(t, p, b, c, "DROP FUNCTION rd_dep(integer) CASCADE")
		routineDropNotices(t, n, routineDropDependencyCascadeNotice())
		routineDropDependencySurvivors(t, p, b, c)
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO rd_data DEFAULT VALUES", 1))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO rd_data VALUES (-10)", 1))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT x FROM rd_data ORDER BY x NULLS LAST", []string{"x"}, []string{"INT4"}, [][]any{{int64(-10)}, {int64(7)}, {nil}}))
	})
}

func TestReferenceRoutineDropAtomicity(t *testing.T) {
	for _, tc := range []struct {
		name, q string
		want    fixture.Diagnostic
	}{
		{"missing", "DROP FUNCTION rd_first(), rd_missing()", fixture.ControlsReferenceDiagnostic("42883", "function rd_missing() does not exist")},
		{"wrong kind", "DROP FUNCTION rd_first(), pg_catalog.sum(integer)", func() fixture.Diagnostic {
			d := fixture.ControlsReferenceDiagnostic("42809", `"pg_catalog.sum" is an aggregate function`)
			d.Hint = "Use DROP AGGREGATE to drop aggregate functions."
			return d
		}()},
		{"dependency", "DROP FUNCTION rd_first(), rd_dep(integer)", func() fixture.Diagnostic {
			d := routineDropDependencyError()
			d.Message = "cannot drop desired object(s) because other objects depend on them"
			return d
		}()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			routineDropScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, n *[]fixture.Diagnostic) {
				routineDropDependencySetup(t, p, b, c)
				routineExec(t, p, b, c, "CREATE FUNCTION rd_first() RETURNS integer LANGUAGE SQL AS $$SELECT 11$$", "CREATE FUNCTION rd_absent() RETURNS integer LANGUAGE SQL AS $$SELECT 12$$")
				routineDropError(t, p, b, c, tc.q, tc.want)
				routineValue(t, p, b, c, "SELECT rd_first() AS v", "v", "INT4", int64(11))
				routineValue(t, p, b, c, "SELECT rd_absent() AS v", "v", "INT4", int64(12))
				routineValue(t, p, b, c, "SELECT rd_dep(4) AS v", "v", "INT4", int64(5))
				routineDropNotices(t, n)
			})
		})
	}
	t.Run("grouped and duplicate roots", func(t *testing.T) {
		routineDropScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, n *[]fixture.Diagnostic) {
			routineExec(t, p, b, c, "CREATE FUNCTION rd_a() RETURNS integer LANGUAGE SQL AS $$SELECT 1$$", "CREATE FUNCTION rd_b() RETURNS integer LANGUAGE SQL AS $$SELECT 2$$", "DROP ROUTINE rd_a(), rd_b(), rd_a()")
			for _, q := range []string{"SELECT rd_a()", "SELECT rd_b()"} {
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Error(q, "42883"))
			}
			routineExec(t, p, b, c, "CREATE FUNCTION rd_a() RETURNS integer LANGUAGE SQL AS $$SELECT 1$$", "DROP FUNCTION IF EXISTS rd_missing(),rd_a()")
			routineDropNotices(t, n, routineDropNotice("function rd_missing() does not exist, skipping"))
			routineExec(t, p, b, c, "CREATE FUNCTION rd_a() RETURNS integer LANGUAGE SQL AS $$SELECT 1$$")
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.ErrorDiagnostic("DROP PROCEDURE IF EXISTS rd_missing(),rd_a()", fixture.ControlsReferenceDiagnostic("42809", "rd_a() is not a procedure")))
			routineDropNotices(t, n, routineDropNotice("procedure rd_missing() does not exist, skipping"))
			routineValue(t, p, b, c, "SELECT rd_a() AS v", "v", "INT4", int64(1))
		})
	})
}
