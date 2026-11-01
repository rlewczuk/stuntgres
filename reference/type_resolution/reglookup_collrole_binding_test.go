package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func regLookupError(query, code, message string, args ...any) fixture.Step {
	want := fixture.ControlsReferenceDiagnostic(code, message)
	s := fixture.ErrorDiagnostic(query, want, args...)
	s.SelectRows = true
	s.ErrorStage = "execute"
	return s
}

func regLookupAnalysisError(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, query, signature string) {
	t.Helper()
	want := fixture.ControlsReferenceDiagnostic("42883", "function "+signature+" does not exist")
	want.Position = "8"
	want.Hint = "No function matches the given name and argument types. You might need to add explicit type casts."
	s := fixture.ErrorDiagnostic(query, want)
	s.SelectRows, s.ErrorStage = true, "execute"
	regObjectRunStep(t, p, b, c, s)
	s = fixture.Prepared(s)
	s.ErrorStage = "prepare"
	regObjectRunStep(t, p, b, c, s)
}

func TestReferenceRegLookupCollRoleBinding(t *testing.T) {
	regLookupScenario(t, []string{"cl_bind"}, nil, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		regObjectExec(t, p, b, c, "CREATE SCHEMA cl_bind")
		for _, name := range []string{"to_regcollation", "to_regrole"} {
			for _, tc := range []struct{ args, signature string }{
				{"", ""}, {"'x'::text,'y'::text", "text, text"},
				{"TRUE", "boolean"}, {"42", "integer"},
				{"name => 'x'::text", "name => text"},
				{"text => 'x'::text", "text => text"},
			} {
				regLookupAnalysisError(t, p, b, c, "SELECT "+name+"("+tc.args+")", name+"("+tc.signature+")")
			}
			regLookupAnalysisError(t, p, b, c, "SELECT cl_bind."+name+"('x'::text)", "cl_bind."+name+"(text)")
		}
		regLookupAnalysisError(t, p, b, c, "SELECT cr_missing_lookup('x'::text)", "cr_missing_lookup(text)")
		regLookupAnalysisError(t, p, b, c, `SELECT "To_Regrole"('x'::text)`, "To_Regrole(text)")
		regLookupAnalysisError(t, p, b, c, `SELECT "To_Regcollation"('x'::text)`, "To_Regcollation(text)")
		regObjectBoth(t, p, b, c, regLookupSelect(b, `SELECT pg_catalog.to_regcollation('pg_catalog."C"') AS c,pg_catalog.to_regrole('pg_monitor') AS r,"to_regrole"('pg_monitor') AS quoted`, []string{"c", "r", "quoted"}, []string{"REGCOLLATION", "REGROLE", "REGROLE"}, [][]any{{`"C"`, "pg_monitor", "pg_monitor"}}))
		for _, tc := range []struct{ name, input, kind, spelling string }{
			{"to_regcollation", `pg_catalog."C"`, "REGCOLLATION", `"C"`},
			{"to_regrole", "pg_monitor", "REGROLE", "pg_monitor"},
		} {
			q := "SELECT "+tc.name+"($1) AS value,pg_typeof($1)::text AS input"
			held := regLookupHeld(t, p, c, q)
			defer held.Statement.Close()
			for _, input := range []any{tc.input, nil, tc.input} {
				var output any = tc.spelling
				if input == nil {
					output = nil
				}
				s := regLookupSelect(b, q, []string{"value", "input"}, []string{tc.kind, "TEXT"}, [][]any{{output, "text"}}, input)
				regObjectBoth(t, p, b, c, s)
				regObjectRunStep(t, p, b, held, s)
			}
		}
		regObjectBoth(t, p, b, c, fixture.Select(`SELECT oid::bigint AS oid,proname,proargtypes::text AS args,prorettype::bigint AS result,provolatile::text AS volatility,proisstrict,proparallel::text AS parallel,proretset,proargnames::text AS names,proallargtypes::text AS alltypes,proargmodes::text AS modes,prosrc FROM pg_catalog.pg_proc WHERE oid IN (4093,4195) AND pronamespace=11 ORDER BY oid`, []string{"oid", "proname", "args", "result", "volatility", "proisstrict", "parallel", "proretset", "names", "alltypes", "modes", "prosrc"}, []string{"INT8", "NAME", "TEXT", "INT8", "TEXT", "BOOL", "TEXT", "BOOL", "TEXT", "TEXT", "TEXT", "TEXT"}, [][]any{
			{int64(4093), "to_regrole", "25", int64(4096), "s", true, "s", false, nil, nil, nil, "to_regrole"},
			{int64(4195), "to_regcollation", "25", int64(4191), "s", true, "s", false, nil, nil, nil, "to_regcollation"},
		}))
	})
}

func TestReferenceRegLookupCollRoleDiagnostics(t *testing.T) {
	regLookupScenario(t, []string{"cl_private"}, []string{"cl_reader"}, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		q := "SELECT to_regcollation($1::text) AS c"
		held := regLookupHeld(t, p, c, q)
		defer held.Statement.Close()
		for _, tc := range []struct{ input, code, message string }{
			{"otherdb.public.c", "0A000", "cross-database references are not implemented: otherdb.public.c"},
			{"x.y.z.w", "42601", "improper qualified name (too many dotted names): x.y.z.w"},
		} {
			literal := "SELECT to_regcollation("+regObjectLiteral(tc.input)+") AS c"
			regObjectBoth(t, p, b, c, regLookupError(literal, tc.code, tc.message))
			s := regLookupError(q, tc.code, tc.message, tc.input)
			regObjectBoth(t, p, b, c, s)
			regObjectRunStep(t, p, b, held, s)
			regObjectRunStep(t, p, b, held, regLookupSelect(b, q, []string{"c"}, []string{"REGCOLLATION"}, [][]any{{`"C"`}}, `pg_catalog."C"`))
		}
		// Errors in the child TEXT expression are not soft lookup failures.
		for _, tc := range []struct{ query, code, message string }{
			{`SELECT to_regrole(($1::text::regrole)::text)`, "42704", `role "cl_absent_child" does not exist`},
			{`SELECT to_regcollation(($1::text::regcollation)::text)`, "42704", `collation "cl_absent_child" for encoding "UTF8" does not exist`},
		} {
			s := regLookupError(tc.query, tc.code, tc.message, "cl_absent_child")
			regObjectBoth(t, p, b, c, s)
			child := regLookupHeld(t, p, c, tc.query)
			regObjectRunStep(t, p, b, child, s)
			child.Statement.Close()
		}
		regObjectExec(t, p, b, c, "CREATE SCHEMA cl_private", `CREATE COLLATION cl_private.secret FROM pg_catalog."C"`, "CREATE ROLE cl_reader", "SET search_path TO cl_private,public", "SET ROLE cl_reader")
		defer c.ExecContext(p.Ctx, "RESET ROLE")
		regObjectBoth(t, p, b, c, regLookupSelect(b, `SELECT to_regcollation('secret') AS c,to_regrole('pg_monitor') AS r`, []string{"c", "r"}, []string{"REGCOLLATION", "REGROLE"}, [][]any{{nil, "pg_monitor"}}))
		regObjectBoth(t, p, b, c, regLookupError(`SELECT to_regcollation('cl_private.secret')`, "42501", "permission denied for schema cl_private"))
		s := regLookupError(q, "42501", "permission denied for schema cl_private", "cl_private.secret")
		regObjectBoth(t, p, b, c, s)
		regObjectRunStep(t, p, b, held, s)
		regObjectRunStep(t, p, b, held, regLookupSelect(b, q, []string{"c"}, []string{"REGCOLLATION"}, [][]any{{`"C"`}}, `pg_catalog."C"`))
		regObjectExec(t, p, b, c, "RESET ROLE", "GRANT USAGE ON SCHEMA cl_private TO cl_reader", "SET ROLE cl_reader")
		regObjectRunStep(t, p, b, held, regLookupSelect(b, q, []string{"c"}, []string{"REGCOLLATION"}, [][]any{{"secret"}}, "cl_private.secret"))
		regObjectExec(t, p, b, c, "RESET ROLE")
	})
}
