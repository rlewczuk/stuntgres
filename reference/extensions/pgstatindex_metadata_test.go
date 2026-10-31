package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePgstatindexMetadata(t *testing.T) {
	pgstatindexCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		// Filter by the selected routine, not all extension routines: the real
		// module also supplies functions deliberately outside this bounded part.
		q := "SELECT p.proname::text AS name,p.pronargs::integer AS nargs,p.proargtypes::text AS args,p.prorettype::bigint AS result,p.proallargtypes::text AS allargs,p.proargmodes::text AS modes,p.proargnames::text AS names,p.proretset AS sets,p.proisstrict AS strict,p.provolatile::text AS volatility,p.proparallel::text AS parallel,p.prosrc AS source,p.probin AS binary,l.lanname::text AS language FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace JOIN pg_language l ON l.oid=p.prolang WHERE n.nspname='ps_part' AND p.proname='pgstatindex' ORDER BY p.proargtypes::text"
		pgstatindexBoth(t, p, b, c, fixture.Select(q,
			[]string{"name", "nargs", "args", "result", "allargs", "modes", "names", "sets", "strict", "volatility", "parallel", "source", "binary", "language"},
			[]string{"TEXT", "INT4", "TEXT", "INT8", "TEXT", "TEXT", "TEXT", "BOOL", "BOOL", "TEXT", "TEXT", "TEXT", "TEXT", "TEXT"},
			[][]any{
				{"pgstatindex", int64(1), "2205", int64(2249), "{2205,23,23,20,20,20,20,20,20,701,701}", "{i,o,o,o,o,o,o,o,o,o,o}", "{relname,version,tree_level,index_size,root_block_no,internal_pages,leaf_pages,empty_pages,deleted_pages,avg_leaf_density,leaf_fragmentation}", false, true, "v", "s", "pgstatindexbyid_v1_5", "$libdir/pgstattuple", "c"},
				{"pgstatindex", int64(1), "25", int64(2249), "{25,23,23,20,20,20,20,20,20,701,701}", "{i,o,o,o,o,o,o,o,o,o,o}", "{relname,version,tree_level,index_size,root_block_no,internal_pages,leaf_pages,empty_pages,deleted_pages,avg_leaf_density,leaf_fragmentation}", false, true, "v", "s", "pgstatindex_v1_5", "$libdir/pgstattuple", "c"},
			}))
		for _, typ := range []string{"text", "regclass"} {
			q := "SELECT * FROM ps_part.pgstatindex($1::" + typ + ")"
			pgstatindexBoth(t, p, b, c, pgstatindexNull(q, nil))
			empty := pgstatindexNull(q+" WHERE false", nil)
			empty.Rows = nil
			pgstatindexBoth(t, p, b, c, empty)
			pgstatindexEmpty(t, p, c, q, "ps_part.ps_index")
		}
	})
}
