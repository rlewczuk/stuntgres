package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferencePgstatindexFeatureSmoke(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"ps_smoke_reader"})
	pgstatindexCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		pgstatindexEmpty(t, p, c, "SELECT * FROM ps_part.pgstatindex('ps_part.ps_index'::text)")
		q := "SELECT ps_part.pgstatindex($1::regclass) AS s"
		st, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer st.Close()
		held := fixture.HeldStatement{Executor: c, Statement: st, SQL: q}
		// Scalar transport fixes the independently published empty composite too.
		pgstatindexBoth(t, p, b, c, fixture.Select("SELECT ps_part.pgstatindex('ps_part.ps_index'::regclass) AS s", []string{"s"}, []string{"RECORD"}, [][]any{{"(4,0,8192,0,0,0,0,0,NaN,NaN)"}}))
		fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Select(q, []string{"s"}, []string{"RECORD"}, [][]any{{"(4,0,8192,0,0,0,0,0,NaN,NaN)"}}, "ps_part.ps_index"))
		fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Select(q, []string{"s"}, []string{"RECORD"}, [][]any{{nil}}, nil))
		pgstatindexBoth(t, p, b, c, pgstatindexNull("SELECT * FROM ps_part.pgstatindex(NULL::regclass)"))
		run(fixture.Exec("INSERT INTO ps_part.ps_heap VALUES(1),(2),(NULL)", 3))
		for _, typ := range []string{"text", "regclass"} {
			pgstatindexPopulated(t, p, c, b.Recorder, "ps_part.pgstatindex('ps_part.ps_index'::"+typ+")")
		}
		pgstatindexBoth(t, p, b, c, pgstatindexError("SELECT * FROM ps_part.pgstatindex('ps_part.ps_heap'::text)", "42809", "relation \"ps_heap\" is not a btree index"))
		for _, q := range []string{"CREATE ROLE ps_smoke_reader", "GRANT USAGE ON SCHEMA ps_part TO ps_smoke_reader", "ALTER TABLE ps_part.ps_heap OWNER TO ps_smoke_reader", "SET SESSION AUTHORIZATION ps_smoke_reader"} {
			run(fixture.Exec(q, 0))
		}
		defer c.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION")
		pgstatindexBoth(t, p, b, c, pgstatindexError("SELECT * FROM ps_part.pgstatindex(NULL::text)", "42501", "permission denied for function pgstatindex"))
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		// Remove ownership as well: function EXECUTE is sufficient without SELECT.
		run(fixture.Exec("ALTER TABLE ps_part.ps_heap OWNER TO CURRENT_USER", 0))
		run(fixture.Exec("GRANT EXECUTE ON FUNCTION ps_part.pgstatindex(text) TO ps_smoke_reader", 0))
		run(fixture.Exec("SET SESSION AUTHORIZATION ps_smoke_reader", 0))
		pgstatindexPopulated(t, p, c, b.Recorder, "ps_part.pgstatindex('ps_part.ps_index'::text)")
		pgstatindexBoth(t, p, b, c, pgstatindexError("SELECT k FROM ps_part.ps_heap", "42501", "permission denied for table ps_heap"))
		pgstatindexBoth(t, p, b, c, pgstatindexError("SELECT * FROM ps_part.pgstatindex(NULL::regclass)", "42501", "permission denied for function pgstatindex"))
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		run(fixture.Exec("CREATE VIEW ps_part.ps_smoke_view AS SELECT * FROM ps_part.pgstatindex('ps_part.ps_index'::regclass)", 0))
		if !b.PG {
			dump := fixture.ExtensionDump(t, p.Ctx, p.Native)
			if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(dump)); err != nil {
				t.Fatal(err)
			}
			pgstatindexPopulated(t, p, c, b.Recorder, "ps_part.ps_smoke_view")
			run(fixture.Exec("SET SESSION AUTHORIZATION ps_smoke_reader", 0))
			pgstatindexPopulated(t, p, c, b.Recorder, "ps_part.pgstatindex('ps_part.ps_index'::text)")
			run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		}
	})
}
