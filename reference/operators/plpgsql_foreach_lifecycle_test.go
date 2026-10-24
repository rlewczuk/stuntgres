package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePLpgSQLForeachLifecycle(t *testing.T) {
	fixture.SchemaReferenceCleanup(t, "fe_saved")
	plpgsqlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		userOperatorExec(t, p, b, c,
			"CREATE SCHEMA fe_saved",
			"CREATE TYPE fe_saved.pair AS (a integer,b text)",
			"CREATE FUNCTION fe_saved.slices(a integer[]) RETURNS text LANGUAGE plpgsql AS $$DECLARE x integer[]; s text:=''; BEGIN FOREACH x SLICE 1 IN ARRAY a LOOP s:=s||x::text||';'; END LOOP; RETURN s; END$$",
			"CREATE FUNCTION fe_saved.targets() RETURNS text LANGUAGE plpgsql AS $$DECLARE x integer; y text; s text:=''; BEGIN FOREACH x,y IN ARRAY '{\"(1,a)\",\"(2,b)\"}'::fe_saved.pair[] LOOP s:=s||x::text||y; END LOOP; RETURN s; END$$",
			"CREATE VIEW fe_saved.v AS SELECT fe_saved.slices('[-1:0][3:4]={{1,NULL},{2,3}}'::integer[]) AS v",
			"CREATE TABLE fe_saved.defaults(id integer,v text DEFAULT fe_saved.slices('{{1,2},{3,4}}'::integer[]))",
			"CREATE TABLE fe_saved.atomic_data(v integer)",
			"CREATE FUNCTION fe_saved.convert(a text[]) RETURNS integer LANGUAGE plpgsql AS $$DECLARE x integer; BEGIN FOREACH x IN ARRAY a LOOP END LOOP; RETURN x; END$$")
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO fe_saved.defaults(id) VALUES(1)", 1))
		q := "SELECT fe_saved.slices($1::integer[]) AS v"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		check := func(cc *sql.Conn, bb fixture.Backend) {
			plpgsqlValue(t, p, bb, cc, "SELECT v FROM fe_saved.v", "TEXT", "[3:4]={1,NULL};[3:4]={2,3};")
			plpgsqlValue(t, p, bb, cc, "SELECT v FROM fe_saved.defaults WHERE id=1", "TEXT", "{1,2};{3,4};")
			plpgsqlValue(t, p, bb, cc, "SELECT fe_saved.targets() AS v", "TEXT", "1a2b")
			current := stmt
			if cc != c {
				var err error
				current, err = cc.PrepareContext(p.Ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				defer current.Close()
			}
			held := fixture.HeldStatement{Executor: cc, Statement: current, SQL: q}
			for _, tc := range []struct{ input, want string }{
				{"{{1,2},{3,4}}", "{1,2};{3,4};"},
				{"[-1:0][3:4]={{1,NULL},{2,3}}", "[3:4]={1,NULL};[3:4]={2,3};"},
			} {
				userOperatorValue(t, p, bb, cc, q, "TEXT", tc.want, tc.input)
				fixture.RunStep(t, p.Ctx, held, bb.Recorder, fixture.Select(q, []string{"v"}, []string{"TEXT"}, [][]any{{tc.want}}, tc.input))
			}
			for _, input := range []any{"{}", nil} {
				want := plpgsqlRuntimeDiagnostic("22004", "FOREACH expression must not be null", "fe_saved.slices(integer[])", "FOREACH over array")
				if input != nil {
					want = plpgsqlRuntimeDiagnostic("2202E", "slice dimension (1) is out of the valid range 0..0", "fe_saved.slices(integer[])", "FOREACH over array")
				}
				for _, prepared := range []bool{false, true} {
					s := fixture.ErrorDiagnostic(q, want, input)
					s.SelectRows, s.ErrorStage, s.Prepared = true, "execute", prepared
					fixture.RunStep(t, p.Ctx, cc, bb.Recorder, s)
				}
				s := fixture.ErrorDiagnostic(q, want, input)
				s.SelectRows, s.ErrorStage = true, "execute"
				fixture.RunStep(t, p.Ctx, held, bb.Recorder, s)
				fixture.RunStep(t, p.Ctx, held, bb.Recorder, fixture.Select(q, []string{"v"}, []string{"TEXT"}, [][]any{{"{5,6};"}}, "{{5,6}}"))
			}
			plpgsqlError(t, p, cc, "INSERT INTO fe_saved.atomic_data VALUES(fe_saved.convert('{1,2}'::text[])),(fe_saved.convert('{1,bad}'::text[]))", false,
				plpgsqlRuntimeDiagnostic("22P02", `invalid input syntax for type integer: "bad"`, "fe_saved.convert(text[])", "FOREACH over array"))
			plpgsqlValue(t, p, bb, cc, "SELECT count(*) AS v FROM fe_saved.atomic_data", "INT8", int64(0))
		}
		// Two native replacements replay each emitted stream unchanged on
		// PostgreSQL while retaining the original native SQL handle.
		parametersReplay(t, p, b, c, "fe_saved", check)
	})
}
