package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func userOperatorScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn)) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			body(t, p, b, c)
		})
	}
}
func userOperatorExec(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, statements ...string) {
	t.Helper()
	for _, q := range statements {
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(q, 0))
	}
}
func userOperatorValue(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, q, typ string, value any, args ...any) {
	t.Helper()
	s := fixture.Select(q, []string{"v"}, []string{typ}, [][]any{{value}}, args...)
	fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(s))
}
func userOperatorError(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, q, code, message, detail, hint, position string, query bool) {
	t.Helper()
	d := fixture.ControlsReferenceDiagnostic(code, message)
	d.Detail = detail
	d.Hint = hint
	d.Position = position
	s := fixture.ErrorDiagnostic(q, d)
	s.SelectRows = query
	if !query {
		s.ErrorStage = "execute"
	}
	fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
	if query {
		s = fixture.Prepared(s)
		s.ErrorStage = "prepare"
		fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
	}
}
func TestReferenceUserOperatorDefinitionExecution(t *testing.T) {
	userOperatorScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		userOperatorExec(t, p, b, c,
			"CREATE OPERATOR public.#~# (LEFTARG=int4,RIGHTARG=int4,FUNCTION=pg_catalog.int4pl)",
			"CREATE OPERATOR public.#/# (LEFTARG=int4,RIGHTARG=int4,PROCEDURE=pg_catalog.int4mi)",
			"CREATE OPERATOR public.##~ (RIGHTARG=int4,FUNCTION=pg_catalog.int4um)",
			"CREATE OPERATOR public.##~ (RIGHTARG=text,FUNCTION=pg_catalog.lower)",
			"CREATE TABLE userop_inputs(i integer)")
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO userop_inputs VALUES (2)", 1))
		for _, q := range []string{"SELECT 2 OPERATOR(public.#~#) 3 AS v", "SELECT i OPERATOR(public.#~#) 3 AS v FROM userop_inputs", "SELECT abs(-2) OPERATOR(public.#~#) abs(-3) AS v"} {
			userOperatorValue(t, p, b, c, q, "INT4", int64(5))
		}
		userOperatorValue(t, p, b, c, "SELECT 2 OPERATOR(public.#/#) 3 AS v", "INT4", int64(-1))
		userOperatorValue(t, p, b, c, "SELECT OPERATOR(public.##~) 3 AS v", "INT4", int64(-3))
		userOperatorValue(t, p, b, c, "SELECT OPERATOR(public.##~) 'ABC'::text AS v", "TEXT", "abc")
		userOperatorValue(t, p, b, c, "SELECT NULL::int4 OPERATOR(public.#~#) 3 AS v", "INT4", nil)
		q := "SELECT $1::int4 OPERATOR(public.#~#) $2::int4 AS v"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		for _, tc := range []struct{ a, b, v any }{{2, 3, int64(5)}, {7, 4, int64(11)}, {nil, 3, nil}} {
			s := fixture.Select(q, []string{"v"}, []string{"INT4"}, [][]any{{tc.v}}, tc.a, tc.b)
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}, b.Recorder, s)
		}
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT i OPERATOR(public.#~#) 3 AS v FROM userop_inputs WHERE false", []string{"v"}, []string{"INT4"}, nil))
		userOperatorValue(t, p, b, c, "SELECT oprcode::oid::int8 AS v FROM pg_operator WHERE oid='public.#~#(int4,int4)'::regoperator", "INT8", int64(177))
		userOperatorValue(t, p, b, c, "SELECT oprleft::int8 AS v FROM pg_operator WHERE oid='public.##~(NONE,int4)'::regoperator", "INT8", int64(0))
		userOperatorValue(t, p, b, c, "SELECT oprresult::int8 AS v FROM pg_operator WHERE oid='public.#~#(int4,int4)'::regoperator", "INT8", int64(23))
		userOperatorValue(t, p, b, c, "SELECT oprowner = (SELECT oid FROM pg_roles WHERE rolname=current_user) AS v FROM pg_operator WHERE oid='public.#~#(int4,int4)'::regoperator", "BOOL", true)
		overflow := fixture.ErrorDiagnostic("SELECT 2147483647 OPERATOR(public.#~#) 1", fixture.ControlsReferenceDiagnostic("22003", "integer out of range"))
		overflow.SelectRows = true
		overflow.ErrorStage = "execute"
		fixture.RunStep(t, p.Ctx, c, b.Recorder, overflow)
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(overflow))
	})
}
func TestReferenceUserOperatorResolutionPrecedence(t *testing.T) {
	fixture.SchemaReferenceCleanup(t, "userop_first", "userop_later", "User Op")
	userOperatorScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		userOperatorExec(t, p, b, c, "CREATE SCHEMA userop_first", "CREATE SCHEMA userop_later", `CREATE SCHEMA "User Op"`,
			"CREATE OPERATOR userop_first.#~# (LEFTARG=int4,RIGHTARG=int4,FUNCTION=int4pl)",
			"CREATE OPERATOR userop_later.#~# (LEFTARG=int4,RIGHTARG=int4,FUNCTION=int4mi)",
			"CREATE OPERATOR userop_later.#~# (LEFTARG=int8,RIGHTARG=int8,FUNCTION=int8pl)",
			"CREATE OPERATOR userop_first.#/# (LEFTARG=int4,RIGHTARG=int4,FUNCTION=int4mi)",
			"CREATE OPERATOR userop_first.##~ (RIGHTARG=int4,FUNCTION=int4um)",
			"CREATE OPERATOR userop_first.##~ (RIGHTARG=int8,FUNCTION=int8um)",
			`CREATE OPERATOR "User Op".?- (LEFTARG=int4,RIGHTARG=int4,FUNCTION=int4pl)`,
			"CREATE OPERATOR userop_first.%- (LEFTARG=int4,RIGHTARG=int4,FUNCTION=int4pl)",
			"SET search_path TO userop_first,userop_later,public")
		for _, tc := range []struct {
			q string
			v int64
		}{
			{"SELECT 2 #~# 3 AS v", 5}, {"SELECT 2::int2 OPERATOR(userop_first.#~#) 3::int2 AS v", 5}, {"SELECT 2 #~# '3' AS v", 5},
			{"SELECT 2 OPERATOR(userop_later.#~#) 3 AS v", -1},
			{"SELECT 2 #~# 3*4 AS v", 14}, {"SELECT 2*3 #~# 4 AS v", 10}, {"SELECT 2 #~# 3 #/# 4 AS v", 1},
			{"SELECT 2 OPERATOR(pg_catalog.*) 3+4 AS v", 14}, {"SELECT 2*3+4 AS v", 10},
			{`SELECT 2 OPERATOR("User Op".?-) 3 AS v`, 5}, {"SELECT 2 %- 3 AS v", 5}, {"SELECT 2/*a*/#~#/*b*/3 AS v", 5},
		} {
			userOperatorValue(t, p, b, c, tc.q, "INT4", tc.v)
		}
		userOperatorValue(t, p, b, c, "SELECT 2::int8 #~# 3::int8 AS v", "INT8", int64(5))
		hint := "Could not choose a best candidate operator. You might need to add explicit type casts."
		userOperatorError(t, p, b, c, "SELECT 2::int2 #~# 3::int2", "42725", "operator is not unique: smallint #~# smallint", "", hint, "16", true)
		userOperatorError(t, p, b, c, "SELECT '2' #~# '3'", "42725", "operator is not unique: unknown #~# unknown", "", hint, "12", true)
		userOperatorError(t, p, b, c, "SELECT ##~ '3'", "42725", "operator is not unique: ##~ unknown", "", hint, "8", true)
		userOperatorExec(t, p, b, c, "CREATE OPERATOR userop_first.+ (LEFTARG=int4,RIGHTARG=int4,FUNCTION=int4mi)", "SET search_path TO userop_first,pg_catalog,public")
		userOperatorValue(t, p, b, c, "SELECT 2+3*4 AS v", "INT4", int64(-10))
		userOperatorValue(t, p, b, c, "SELECT 2 OPERATOR(pg_catalog.+) 3 AS v", "INT4", int64(5))
	})
}

// A released operator can have a real expression provider without a callable
// routine declaration. A custom overload must not turn that seed into a shell
// or force the routine identity inventory to become executable.
func TestReferenceUserOperatorLookupOnlySeedOverload(t *testing.T) {
	userOperatorScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		userOperatorExec(t, p, b, c, "CREATE OPERATOR public.? (LEFTARG=int4,RIGHTARG=int4,FUNCTION=pg_catalog.int4pl)")
		userOperatorValue(t, p, b, c, "SELECT 2 ? 3 AS v", "INT4", int64(5))
		userOperatorValue(t, p, b, c, `SELECT '{"k":1}'::jsonb ? 'k'::text AS v`, "BOOL", true)
		userOperatorValue(t, p, b, c, `SELECT '{"k":1}'::jsonb ? 'missing'::text AS v`, "BOOL", false)
		userOperatorValue(t, p, b, c, "SELECT NULL::jsonb ? 'k'::text AS v", "BOOL", nil)
		userOperatorValue(t, p, b, c, `SELECT '{"k":1}'::jsonb OPERATOR(pg_catalog.?) 'k'::text AS v`, "BOOL", true)
		userOperatorValue(t, p, b, c, "SELECT oprcode::oid::int8 AS v FROM pg_operator WHERE oid=3247", "INT8", int64(4047))
	})
}
