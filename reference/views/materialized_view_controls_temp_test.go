package reference_test

import (
	"bytes"
	"database/sql"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceMaterializedViewRestoreTemporary(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			conn, run := fixture.QueryTableCreateSession(t, p, b)
			materializedViewSeed(run)
			run(fixture.Exec("CREATE MATERIALIZED VIEW public.mv_collision AS SELECT i,s FROM public.mv_base", 3))
			run(fixture.Exec("INSERT INTO public.mv_base VALUES(3,'b')", 1))
			run(fixture.Exec("CREATE TEMP TABLE mv_collision(i INT)", 0))
			run(fixture.Exec("INSERT INTO pg_temp.mv_collision VALUES(77)", 1))
			var retained *sql.Rows
			if !b.PG {
				var err error
				retained, err = conn.QueryContext(p.Ctx, "SELECT i,s FROM public.mv_collision ORDER BY i")
				if err != nil {
					t.Fatal(err)
				}
				defer retained.Close()
			}
			check := func() {
				run(materializedViewBag("public.mv_collision"))
				run(fixture.Select("SELECT i FROM pg_temp.mv_collision", []string{"i"}, []string{"INT4"}, [][]any{{int64(77)}}))
				run(fixture.Select("SELECT count(*) AS n FROM public.mv_base", []string{"n"}, []string{"INT8"}, [][]any{{int64(4)}}))
			}
			check()
			if !b.PG {
				for range 2 {
					var dump bytes.Buffer
					if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
						t.Fatal(err)
					}
					if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
						t.Fatal(err)
					}
					check()
				}
				materializedViewRetainedBag(t, retained)
				// A second pool on the same public connector sees the imported stale rows.
				peer := sql.OpenDB(p.NativeConnector)
				defer peer.Close()
				rows, err := peer.QueryContext(p.Ctx, "SELECT i,s FROM public.mv_collision ORDER BY i")
				if err != nil {
					t.Fatal(err)
				}
				materializedViewRetainedBag(t, rows)
			}
			run(fixture.Exec("DROP TABLE pg_temp.mv_collision", 0))
			run(fixture.Exec("DROP MATERIALIZED VIEW public.mv_collision", 0))
		})
	}
}
