package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePgstathashindexMetadata(t *testing.T) {
	pgstathashindexCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		q := "SELECT p.proname::text AS name,p.pronargs::integer AS nargs,p.proargtypes::text AS args,p.prorettype::bigint AS result,p.proallargtypes::text AS allargs,p.proargmodes::text AS modes,p.proargnames::text AS names,p.proretset AS sets,p.proisstrict AS strict,p.provolatile::text AS volatility,p.proparallel::text AS parallel,p.prosrc AS source,p.probin AS binary,l.lanname::text AS language FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace JOIN pg_language l ON l.oid=p.prolang WHERE n.nspname='ph_part' AND p.proname='pgstathashindex'"
		// REL_18_6 pgstattuple--1.4--1.5.sql defines dead_items, not the
		// manual's inconsistent dead_tuples table label. There is no text slot.
		pgstathashindexBoth(t, p, b, c, fixture.Select(q,
			[]string{"name", "nargs", "args", "result", "allargs", "modes", "names", "sets", "strict", "volatility", "parallel", "source", "binary", "language"},
			[]string{"TEXT", "INT4", "TEXT", "INT8", "TEXT", "TEXT", "TEXT", "BOOL", "BOOL", "TEXT", "TEXT", "TEXT", "TEXT", "TEXT"},
			[][]any{{"pgstathashindex", int64(1), "2205", int64(2249), "{2205,23,20,20,20,20,20,20,701}", "{i,o,o,o,o,o,o,o,o}", "{relname,version,bucket_pages,overflow_pages,bitmap_pages,unused_pages,live_items,dead_items,free_percent}", false, true, "v", "s", "pgstathashindex", "$libdir/pgstattuple", "c"}}))
		pgstathashindexStats(t, p, b, c, "SELECT * FROM ph_part.pgstathashindex('ph_part.ph_index'::regclass)", 0, 0)
		emptyScalar, emptyBuckets := "(4,4,0,1,0,0,0,100)", int64(4)
		if b.PG {
			// Root tests-1 observed this fixture's fresh build: eight buckets.
			// The native compact model remains four, independently specified.
			emptyScalar, emptyBuckets = "(4,8,0,1,0,0,0,100)", 8
		}
		pgstathashindexBoth(t, p, b, c, fixture.Select("SELECT ph_part.pgstathashindex('ph_part.ph_index'::regclass) AS s", []string{"s"}, []string{"RECORD"}, [][]any{{emptyScalar}}))
		pgstathashindexStats(t, p, b, c, "SELECT * FROM ph_part.pgstathashindex($1)", 0, 0, "ph_part.ph_index")
		pgstathashindexBoth(t, p, b, c, pgstathashindexNull("SELECT * FROM ph_part.pgstathashindex($1)", nil))
		for _, arg := range []string{"NULL", "NULL::regclass", "$1::regclass"} {
			var args []any
			if arg == "$1::regclass" {
				args = []any{nil}
			}
			pgstathashindexBoth(t, p, b, c, pgstathashindexNull("SELECT * FROM ph_part.pgstathashindex("+arg+")", args...))
			pgstathashindexBoth(t, p, b, c, fixture.Select("SELECT ph_part.pgstathashindex("+arg+") AS s", []string{"s"}, []string{"RECORD"}, [][]any{{nil}}, args...))
		}
		pgstathashindexBoth(t, p, b, c, fixture.Select("SELECT (ph_part.pgstathashindex(relname => $1::regclass)).live_items AS n", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}, "ph_part.ph_index"))
		pgstathashindexBoth(t, p, b, c, fixture.Select("SELECT s.n FROM ph_part.pgstathashindex('ph_part.ph_index') AS s(v,b,o,m,u,n,d,f)", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
		for _, s := range []fixture.Step{
			fixture.Exec("CREATE TABLE ph_part.ph_inputs(id integer,n text)", 0),
			fixture.Exec("INSERT INTO ph_part.ph_inputs VALUES(1,'ph_part.ph_index'),(2,NULL),(3,'ph_part.ph_index')", 3),
		} {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
		}
		q = "SELECT i.id,s.live_items FROM ph_part.ph_inputs i CROSS JOIN LATERAL ph_part.pgstathashindex(i.n::regclass) s ORDER BY i.id"
		pgstathashindexBoth(t, p, b, c, fixture.Select(q, []string{"id", "live_items"}, []string{"INT4", "INT8"}, [][]any{{int64(1), int64(0)}, {int64(2), nil}, {int64(3), int64(0)}}))
		pgstathashindexBoth(t, p, b, c, fixture.Select("SELECT s.live_items FROM (SELECT n FROM ph_part.ph_inputs WHERE false) i CROSS JOIN LATERAL ph_part.pgstathashindex(i.n::regclass) s", []string{"live_items"}, []string{"INT8"}, nil))
		q = "SELECT * FROM ph_part.pgstathashindex($1::regclass)"
		st, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer st.Close()
		held := fixture.HeldStatement{Executor: c, Statement: st, SQL: q}
		for _, arg := range []any{"ph_part.ph_index", nil, "ph_part.ph_index"} {
			s := pgstathashindexNull(q, arg)
			if arg != nil {
				s.Rows = [][]any{{int64(4), emptyBuckets, int64(0), int64(1), int64(0), int64(0), int64(0), float64(100)}}
			}
			fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
		}
	})
}
