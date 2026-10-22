package reference_test

import (
	"database/sql"
	"strconv"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func classSyntaxBoundaries(t *testing.T, p *fixture.Pair, b fixture.Backend, conn *sql.Conn) {
	t.Helper()
	for _, tc := range []struct{ q, token string }{
		{`CREATE OPERATOR CLASS public.class_diag_failed FOR TYPE integer USING btree AS STORAGE integer GARBAGE`, `GARBAGE`},
		{`ALTER OPERATOR CLASS public.class_diag_existing USING btree ADD FUNCTION 1 pg_catalog.btint4cmp(integer,integer)`, `ADD`},
		{`DROP OPERATOR CLASS public.class_diag_existing USING btree GARBAGE`, `GARBAGE`},
	} {
		t.Run("syntax_"+tc.token+"_"+strings.Fields(tc.q)[0], func(t *testing.T) {
			for _, prepared := range []bool{false, true} {
				s := fixture.ImplicitError(tc.q, fixture.Diagnostic{Code: "42601", Message: `syntax error at or near "` + tc.token + `"`, Position: strconv.Itoa(strings.Index(tc.q, tc.token) + 1)})
				s.Prepared, s.ErrorStage = prepared, "execute"
				if prepared {
					s.ErrorStage = "prepare"
				}
				fixture.RunStep(t, p.Ctx, conn, b.Recorder, s)
				classDiagnosticAtomicity(t, p, b, conn)
			}
		})
	}
}

func classGraphBoundaries(t *testing.T, p *fixture.Pair, b fixture.Backend, conn *sql.Conn) {
	t.Helper()
	run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, b.Recorder, s) }
	for _, q := range []string{
		`CREATE SCHEMA class_graph_schema`,
		`CREATE OPERATOR class_graph_schema.=== (LEFTARG=integer,RIGHTARG=integer,FUNCTION=pg_catalog.int4eq)`,
		`CREATE OPERATOR FAMILY class_graph_schema.f USING btree`,
		`CREATE OPERATOR CLASS class_graph_schema.c FOR TYPE integer USING btree FAMILY class_graph_schema.f AS OPERATOR 3 class_graph_schema.===, FUNCTION 1 pg_catalog.btint4cmp(integer,integer), FUNCTION 2 pg_catalog.btint4sortsupport(internal)`,
	} {
		run(fixture.Exec(q, 0))
	}
	t.Run("required_operator", func(t *testing.T) {
		for _, prepared := range []bool{false, true} {
			s := fixture.ImplicitError(`DROP OPERATOR class_graph_schema.===(integer,integer)`, fixture.Diagnostic{Code: "2BP01", Message: `cannot drop operator class_graph_schema.===(integer,integer) because other objects depend on it`, Detail: `operator class class_graph_schema.c for access method btree depends on operator class_graph_schema.===(integer,integer)`, Hint: `Use DROP ... CASCADE to drop the dependent objects too.`})
			s.Prepared, s.ErrorStage = prepared, "execute"
			fixture.RunStep(t, p.Ctx, conn, b.Recorder, s)
		}
	})
	// Required member DROP is redirected through its owning class; optional
	// support remains loose even when admitted in the CREATE CLASS declaration.
	t.Run("required_member", func(t *testing.T) {
		fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.ImplicitError(`ALTER OPERATOR FAMILY class_graph_schema.f USING btree DROP FUNCTION 1(integer)`, fixture.Diagnostic{Code: "2BP01", Message: `cannot drop function 1 (integer, integer) of operator family class_graph_schema.f for access method btree: btint4cmp(integer,integer) because operator class class_graph_schema.c for access method btree requires it`, Hint: `You can drop operator class class_graph_schema.c for access method btree instead.`}))
	})
	run(fixture.Exec(`ALTER OPERATOR FAMILY class_graph_schema.f USING btree DROP FUNCTION 2(integer)`, 0))
	run(fixture.Exec(`DROP OPERATOR FAMILY class_graph_schema.f USING btree CASCADE`, 0))
	run(fixture.Select(`SELECT opcname FROM pg_opclass WHERE opcname='c' AND opcnamespace='class_graph_schema'::regnamespace`, []string{"opcname"}, []string{"NAME"}, nil))
	run(fixture.Select(`SELECT oprname FROM pg_operator WHERE oprname='===' AND oprnamespace='class_graph_schema'::regnamespace`, []string{"oprname"}, []string{"NAME"}, [][]any{{"==="}}))
	run(fixture.Exec(`CREATE FUNCTION class_graph_schema.cmp(integer,integer) RETURNS integer LANGUAGE SQL AS $$SELECT $1-$2$$`, 0))
	run(fixture.Exec(`CREATE OPERATOR CLASS class_graph_schema.routine_class FOR TYPE integer USING btree AS FUNCTION 1 class_graph_schema.cmp(integer,integer)`, 0))
	t.Run("required_routine", func(t *testing.T) {
		for _, prepared := range []bool{false, true} {
			s := fixture.ImplicitError(`DROP FUNCTION class_graph_schema.cmp(integer,integer)`, fixture.Diagnostic{Code: "2BP01", Message: `cannot drop function class_graph_schema.cmp(integer,integer) because other objects depend on it`, Detail: `operator class class_graph_schema.routine_class for access method btree depends on function class_graph_schema.cmp(integer,integer)`, Hint: `Use DROP ... CASCADE to drop the dependent objects too.`})
			s.Prepared, s.ErrorStage = prepared, "execute"
			fixture.RunStep(t, p.Ctx, conn, b.Recorder, s)
		}
	})
	run(fixture.Exec(`DROP FUNCTION class_graph_schema.cmp(integer,integer) CASCADE`, 0))
	run(fixture.Select(`SELECT opcname FROM pg_opclass WHERE opcname='routine_class' AND opcnamespace='class_graph_schema'::regnamespace`, []string{"opcname"}, []string{"NAME"}, nil))
	run(fixture.Select(`SELECT opfname FROM pg_opfamily WHERE opfname='routine_class' AND opfnamespace='class_graph_schema'::regnamespace`, []string{"opfname"}, []string{"NAME"}, [][]any{{"routine_class"}}))
	run(fixture.Exec(`CREATE OPERATOR CLASS class_graph_schema.schema_class FOR TYPE integer USING btree AS FUNCTION 1 pg_catalog.btint4cmp(integer,integer)`, 0))
	run(fixture.Exec(`DROP SCHEMA class_graph_schema CASCADE`, 0))
	run(fixture.Select(`SELECT opcname FROM pg_opclass WHERE opcname='schema_class'`, []string{"opcname"}, []string{"NAME"}, nil))
}
