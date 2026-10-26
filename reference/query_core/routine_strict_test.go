package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceRoutineCoreStrictDeclarations(t *testing.T) {
	routineScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, tc := range []struct{ name, declaration string }{
			{"routine_strict_before", "CREATE FUNCTION routine_strict_before(integer) RETURNS integer LANGUAGE SQL STRICT AS $$SELECT coalesce($1,91)+1$$"},
			{"routine_strict_after", "CREATE FUNCTION routine_strict_after(integer) RETURNS integer AS $$SELECT coalesce($1,91)+1$$ LANGUAGE SQL STRICT"},
			{"routine_strict_standard", "CREATE FUNCTION routine_strict_standard(integer) RETURNS integer LANGUAGE SQL STRICT RETURN coalesce($1,91)+1"},
		} {
			routineExec(t, p, b, c, tc.declaration)
			// If the NULL input reached the body, it would return 92, not NULL.
			routineValue(t, p, b, c, "SELECT "+tc.name+"(NULL::integer) AS v", "v", "INT4", nil)
			routineValue(t, p, b, c, "SELECT "+tc.name+"(4) AS v", "v", "INT4", int64(5))
			routineValue(t, p, b, c, "SELECT "+tc.name+"($1::integer) AS v", "v", "INT4", nil, nil)
			routineValue(t, p, b, c, "SELECT "+tc.name+"($1::integer) AS v", "v", "INT4", int64(8), int64(7))
			routineStrictMetadata(t, p, b, c, tc.name+"(integer)", true)
		}
		routineExec(t, p, b, c,
			"CREATE FUNCTION routine_strict_pair(integer,integer) RETURNS integer LANGUAGE SQL STRICT AS $$SELECT coalesce($1,10)+coalesce($2,20)$$",
			"CREATE FUNCTION routine_nonstrict_control(integer) RETURNS integer LANGUAGE SQL AS $$SELECT coalesce($1,91)+1$$")
		for _, args := range []string{"NULL::integer,2", "1,NULL::integer", "NULL::integer,NULL::integer"} {
			routineValue(t, p, b, c, "SELECT routine_strict_pair("+args+") AS v", "v", "INT4", nil)
		}
		routineValue(t, p, b, c, "SELECT routine_strict_pair(1,2) AS v", "v", "INT4", int64(3))
		routineValue(t, p, b, c, "SELECT routine_nonstrict_control(NULL::integer) AS v", "v", "INT4", int64(92))
		routineStrictMetadata(t, p, b, c, "routine_strict_pair(integer,integer)", true)
		routineStrictMetadata(t, p, b, c, "routine_nonstrict_control(integer)", false)
	})
}

func routineStrictMetadata(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, signature string, strict bool) {
	t.Helper()
	step := fixture.Select("SELECT proisstrict AS is_strict FROM pg_catalog.pg_proc WHERE oid=$1::regprocedure", []string{"is_strict"}, []string{"BOOL"}, [][]any{{strict}}, signature)
	fixture.RunStep(t, p.Ctx, c, b.Recorder, step)
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(step))
}

func TestReferenceRoutineCoreReplacementAttributes(t *testing.T) {
	for _, tc := range []struct{ name, body string }{
		{"language_before", "LANGUAGE SQL STRICT VOLATILE AS $$SELECT coalesce($1,91)+2$$"},
		{"language_after", "AS $$SELECT coalesce($1,91)+2$$ LANGUAGE SQL VOLATILE STRICT"},
		{"standard_return", "LANGUAGE SQL STRICT VOLATILE RETURN coalesce($1,91)+2"},
		{"standard_atomic", "LANGUAGE SQL VOLATILE STRICT BEGIN ATOMIC SELECT coalesce($1,91)+2; END"},
	} {
		for _, prepared := range []bool{false, true} {
			route := "direct"
			if prepared {
				route = "prepared"
			}
			t.Run(tc.name+"/"+route, func(t *testing.T) {
				routineScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
					run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
					routineExec(t, p, b, c, "CREATE FUNCTION routine_replace_attr(integer) RETURNS integer LANGUAGE SQL AS $$SELECT coalesce($1,91)+1$$")
					identity := "SELECT oid FROM pg_catalog.pg_proc WHERE oid='routine_replace_attr(integer)'::regprocedure"
					var before, after int64
					if err := c.QueryRowContext(p.Ctx, identity).Scan(&before); err != nil || before <= 0 {
						t.Fatal("original function identity", before, err)
					}
					query := "SELECT routine_replace_attr($1::integer) AS v"
					stmt, err := c.PrepareContext(p.Ctx, query)
					if err != nil {
						t.Fatal(err)
					}
					defer stmt.Close()
					held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: query}
					fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Select(query, []string{"v"}, []string{"INT4"}, [][]any{{int64(92)}}, nil))
					step := fixture.Exec("CREATE OR REPLACE FUNCTION routine_replace_attr(integer) RETURNS integer "+tc.body, 0)
					if prepared {
						step = fixture.Prepared(step)
					}
					run(step)
					if err := c.QueryRowContext(p.Ctx, identity).Scan(&after); err != nil || after != before {
						t.Fatal("replacement function identity", before, after, err)
					}
					attributes := fixture.Select("SELECT proisstrict,provolatile FROM pg_catalog.pg_proc WHERE oid='routine_replace_attr(integer)'::regprocedure", []string{"proisstrict", "provolatile"}, []string{"BOOL", "CHAR"}, [][]any{{true, "v"}})
					run(attributes)
					run(fixture.Prepared(attributes))
					for _, input := range []struct{ arg, want any }{{nil, nil}, {int64(4), int64(6)}} {
						result := fixture.Select(query, []string{"v"}, []string{"INT4"}, [][]any{{input.want}}, input.arg)
						run(result)
						run(fixture.Prepared(result))
						fixture.RunStep(t, p.Ctx, held, b.Recorder, result)
					}
				})
			})
		}
	}
}
