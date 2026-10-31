package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferencePgstathashindexFeatureSmoke(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"ph_smoke_owner", "ph_smoke_reader"})
	pgstathashindexCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		// Root tests-1 observed eight buckets for this PostgreSQL build.
		// Native's independent stateless compact model has four.
		buckets := int64(4)
		if b.PG {
			buckets = 8
		}
		pgstathashindexBoth(t, p, b, c, fixture.Select("SELECT * FROM ph_part.pgstathashindex('ph_part.ph_index')", pgstathashindexNames, pgstathashindexTypes, [][]any{{int64(4), buckets, int64(0), int64(1), int64(0), int64(0), int64(0), float64(100)}}))
		pgstathashindexSeed(t, p, b, c)
		for _, tc := range []struct{ name string; live int64 }{{"ph_index", 4}, {"ph_sibling", 4}, {"ph_expression", 5}} {
			pgstathashindexStats(t, p, b, c, "SELECT * FROM ph_part.pgstathashindex($1::regclass)", tc.live, 0, "ph_part."+tc.name)
		}
		q := "SELECT live_items,dead_items FROM ph_part.pgstathashindex($1::regclass)"
		st, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer st.Close()
		held := fixture.HeldStatement{Executor: c, Statement: st, SQL: q}
		for _, tc := range []struct{ arg any; row []any }{{"ph_part.ph_index", []any{int64(4), int64(0)}}, {nil, []any{nil, nil}}, {"ph_part.ph_expression", []any{int64(5), int64(0)}}} {
			fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Select(q, []string{"live_items", "dead_items"}, []string{"INT8", "INT8"}, [][]any{tc.row}, tc.arg))
		}
		pgstathashindexBoth(t, p, b, c, pgstathashindexNull("SELECT * FROM ph_part.pgstathashindex(NULL::regclass)"))
		for _, q := range []string{"CREATE INDEX ph_btree ON ph_part.ph_heap(k)", "CREATE ROLE ph_smoke_owner", "CREATE ROLE ph_smoke_reader", "GRANT USAGE ON SCHEMA ph_part TO ph_smoke_owner,ph_smoke_reader", "ALTER TABLE ph_part.ph_heap OWNER TO ph_smoke_owner", "CREATE VIEW ph_part.ph_smoke_view AS SELECT (ph_part.pgstathashindex('ph_part.ph_expression'::regclass)).live_items AS n"} {
			run(fixture.Exec(q, 0))
		}
		pgstathashindexBoth(t, p, b, c, pgstatindexError("SELECT * FROM ph_part.pgstathashindex('ph_part.ph_btree'::regclass)", "42809", "relation \"ph_btree\" is not a hash index"))
		defer c.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION")
		run(fixture.Exec("SET SESSION AUTHORIZATION ph_smoke_owner", 0))
		for _, arg := range []any{nil, "ph_part.ph_index"} {
			pgstathashindexBoth(t, p, b, c, pgstatindexError(q, "42501", "permission denied for function pgstathashindex", arg))
		}
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		run(fixture.Exec("GRANT EXECUTE ON FUNCTION ph_part.pgstathashindex(regclass) TO ph_smoke_reader", 0))
		run(fixture.Exec("SET SESSION AUTHORIZATION ph_smoke_reader", 0))
		pgstathashindexBoth(t, p, b, c, pgstatindexError("SELECT k FROM ph_part.ph_heap", "42501", "permission denied for table ph_heap"))
		pgstathashindexBoth(t, p, b, c, pgstathashindexCounts(q, 5, 0, "ph_part.ph_expression"))
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		check := fixture.Select("SELECT n FROM ph_part.ph_smoke_view", []string{"n"}, []string{"INT8"}, [][]any{{int64(5)}})
		pgstathashindexBoth(t, p, b, c, check)
		if !b.PG {
			dump := fixture.ExtensionDump(t, p.Ctx, p.Native)
			if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(dump)); err != nil {
				t.Fatal(err)
			}
			pgstathashindexBoth(t, p, b, c, check)
			fixture.RunStep(t, p.Ctx, held, b.Recorder, pgstathashindexCounts(q, 5, 0, "ph_part.ph_expression"))
		}
	})
}
