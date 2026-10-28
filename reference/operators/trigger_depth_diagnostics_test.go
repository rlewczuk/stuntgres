package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceTriggerDepthDiagnostics(t *testing.T) {
	triggerScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, tc := range []struct {
			q, signature string
			position     int
		}{
			{"SELECT pg_trigger_depth(1)", "pg_trigger_depth(integer)", 8},
			{"SELECT pg_trigger_depth(true)", "pg_trigger_depth(boolean)", 8},
			{"SELECT pg_trigger_depth(NULL)", "pg_trigger_depth(unknown)", 8},
			{"SELECT pg_trigger_depth(NULL::text)", "pg_trigger_depth(text)", 8},
			{"SELECT pg_trigger_depth(1,2)", "pg_trigger_depth(integer, integer)", 8},
			{"SELECT pg_trigger_depth(x => 1)", "pg_trigger_depth(x => integer)", 8},
			{"SELECT public.pg_trigger_depth()", "public.pg_trigger_depth()", 8},
			{"SELECT \"PG_TRIGGER_DEPTH\"()", "PG_TRIGGER_DEPTH()", 8},
			{"SELECT 'λ', pg_trigger_depth(1)", "pg_trigger_depth(integer)", 13},
		} {
			want := fixture.ControlsReferenceDiagnostic("42883", "function "+tc.signature+" does not exist")
			want.Position = fixture.Position(tc.position)
			want.Hint = "No function matches the given name and argument types. You might need to add explicit type casts."
			depthBindingError(t, p, c, tc.q, want)
		}
		for _, tc := range []struct{ suffix, message string }{
			{"(*)", "pg_trigger_depth(*) specified, but pg_trigger_depth is not an aggregate function"},
			{"() FILTER (WHERE true)", "FILTER specified, but pg_trigger_depth is not an aggregate function"},
			{"() OVER ()", "OVER specified, but pg_trigger_depth is not a window function nor an aggregate function"},
		} {
			want := fixture.ControlsReferenceDiagnostic("42809", tc.message)
			want.Position = fixture.Position(8)
			depthBindingError(t, p, c, "SELECT pg_trigger_depth"+tc.suffix, want)
		}
		want := fixture.ControlsReferenceDiagnostic("42703", `column "td_missing" does not exist`)
		want.Position = fixture.Position(25)
		depthBindingError(t, p, c, "SELECT pg_trigger_depth(td_missing)", want)
		fixture.RunStep(t, p.Ctx, c, b.Recorder, depthZero())
		q := `SELECT oid::bigint AS oid,proname::text AS name,pronargs::bigint AS nargs,
prorettype::bigint AS result,proisstrict AS strict,provolatile::text AS volatility,
proparallel::text AS parallel,proretset AS is_set,prorows::text AS rows,
procost::text AS cost,proargtypes::text AS inputs,proallargtypes::text AS alltypes,
proargmodes::text AS modes,proargnames::text AS names,pronargdefaults::bigint AS defaults,
prosecdef AS definer,proleakproof AS leakproof,provariadic::bigint AS variadic,
prosupport::oid::bigint AS support,prolang::bigint AS language,prosrc AS source
FROM pg_proc WHERE oid=3163`
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select(q,
			[]string{"oid", "name", "nargs", "result", "strict", "volatility", "parallel", "is_set", "rows", "cost", "inputs", "alltypes", "modes", "names", "defaults", "definer", "leakproof", "variadic", "support", "language", "source"},
			[]string{"INT8", "TEXT", "INT8", "INT8", "BOOL", "TEXT", "TEXT", "BOOL", "TEXT", "TEXT", "TEXT", "TEXT", "TEXT", "TEXT", "INT8", "BOOL", "BOOL", "INT8", "INT8", "INT8", "TEXT"},
			[][]any{{int64(3163), "pg_trigger_depth", int64(0), int64(23), true, "s", "r", false, "0", "1", "", nil, nil, nil, int64(0), false, false, int64(0), int64(0), int64(12), "pg_trigger_depth"}}))
	})
}

func depthBindingError(t *testing.T, p *fixture.Pair, c *sql.Conn, q string, want fixture.Diagnostic) {
	t.Helper()
	rows, err := c.QueryContext(p.Ctx, q)
	if rows != nil {
		rows.Close()
		t.Fatal("failed query published rows")
	}
	fixture.AssertDiagnostic(t, err, &want)
	s, err := c.PrepareContext(p.Ctx, q)
	if s != nil {
		s.Close()
		t.Fatal("failed prepare published statement")
	}
	fixture.AssertDiagnostic(t, err, &want)
}
