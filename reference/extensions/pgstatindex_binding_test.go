package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePgstatindexBinding(t *testing.T) {
	pgstatindexCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("CREATE DOMAIN ps_part.ps_name AS text", 0))
		for _, arg := range []string{"'ps_part.ps_index'", "'ps_part.ps_index'::text", "'ps_part.ps_index'::regclass", "'ps_part.ps_index'::name", "'ps_part.ps_index'::varchar", "'ps_part.ps_index'::ps_part.ps_name", "relname => 'ps_part.ps_index'::text", "relname => 'ps_part.ps_index'::regclass"} {
			pgstatindexEmpty(t, p, c, "SELECT * FROM ps_part.pgstatindex("+arg+")")
		}
		var oid int64
		if err := c.QueryRowContext(p.Ctx, "SELECT 'ps_part.ps_index'::regclass::oid").Scan(&oid); err != nil {
			t.Fatal(err)
		}
		for _, typ := range []string{"oid", "integer", "bigint"} {
			pgstatindexEmpty(t, p, c, "SELECT * FROM ps_part.pgstatindex($1::"+typ+")", oid)
		}
		for _, cast := range []string{"", "::text", "::regclass"} {
			q := "SELECT * FROM ps_part.pgstatindex($1" + cast + ")"
			pgstatindexEmpty(t, p, c, q, "ps_part.ps_index")
			pgstatindexBoth(t, p, b, c, pgstatindexNull(q, nil))
			pgstatindexBoth(t, p, b, c, fixture.Select("SELECT ps_part.pgstatindex($1"+cast+") AS s", []string{"s"}, []string{"RECORD"}, [][]any{{nil}}, nil))
		}
		pgstatindexBoth(t, p, b, c, fixture.Select("SELECT (ps_part.pgstatindex(relname => $1::regclass)).version AS v", []string{"v"}, []string{"INT4"}, [][]any{{int64(4)}}, "ps_part.ps_index"))
		run(fixture.Exec("CREATE TABLE ps_part.ps_inputs(id integer, n text)", 0))
		run(fixture.Exec("INSERT INTO ps_part.ps_inputs VALUES(1,'ps_part.ps_index'),(2,NULL),(3,'ps_part.ps_index')", 3))
		pgstatindexBoth(t, p, b, c, fixture.Select("SELECT i.id,s.version FROM ps_part.ps_inputs i CROSS JOIN LATERAL ps_part.pgstatindex(i.n) s ORDER BY i.id", []string{"id", "version"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(4)}, {int64(2), nil}, {int64(3), int64(4)}}))
		// A sequence-backed argument detects accidental evaluation once per OUT field.
		run(fixture.Exec("CREATE SEQUENCE ps_part.ps_eval", 0))
		pgstatindexEmpty(t, p, c, "SELECT * FROM ps_part.pgstatindex(CASE WHEN nextval('ps_part.ps_eval') > 0 THEN 'ps_part.ps_index'::text ELSE NULL END)")
		run(fixture.Select("SELECT currval('ps_part.ps_eval') AS n", []string{"n"}, []string{"INT8"}, [][]any{{int64(2)}}))
		for _, tc := range []struct{ arg, signature string }{
			{"reloid => 'ps_part.ps_index'::text", "ps_part.pgstatindex(reloid => text)"},
			{"", "ps_part.pgstatindex()"},
			{"true", "ps_part.pgstatindex(boolean)"},
		} {
			q := "SELECT * FROM ps_part.pgstatindex(" + tc.arg + ")"
			d := fixture.ControlsReferenceDiagnostic("42883", "function "+tc.signature+" does not exist")
			d.Hint = "No function matches the given name and argument types. You might need to add explicit type casts."
			d.Position = "15"
			s := fixture.ImplicitError(q, d)
			s.SelectRows = true
			// Function resolution happens at parse/prepare, before execution.
			run(s)
			_, err := c.PrepareContext(p.Ctx, q)
			fixture.AssertDiagnostic(t, err, &d)
		}
	})
}
