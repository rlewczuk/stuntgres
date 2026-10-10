package reference_test

import (
	"bytes"
	"database/sql"
	"stuntgres"
	"testing"
)

func TestReferenceMaterializedViewRestoreTemporary(t *testing.T) {
	p := newReferenceScenario(t)
	for _, b := range p.backends() {
		t.Run(b.name, func(t *testing.T) {
			conn, run := queryTableCreateSession(t, p, b)
			materializedViewSeed(run)
			run(referenceExec("CREATE MATERIALIZED VIEW public.mv_collision AS SELECT i,s FROM public.mv_base", 3))
			run(referenceExec("INSERT INTO public.mv_base VALUES(3,'b')", 1))
			run(referenceExec("CREATE TEMP TABLE mv_collision(i INT)", 0))
			run(referenceExec("INSERT INTO pg_temp.mv_collision VALUES(77)", 1))
			var retained *sql.Rows
			if !b.pg {
				var err error
				retained, err = conn.QueryContext(p.ctx, "SELECT i,s FROM public.mv_collision ORDER BY i")
				if err != nil {
					t.Fatal(err)
				}
				defer retained.Close()
			}
			check := func() {
				run(materializedViewBag("public.mv_collision"))
				run(referenceSelect("SELECT i FROM pg_temp.mv_collision", []string{"i"}, []string{"INT4"}, [][]any{{int64(77)}}))
				run(referenceSelect("SELECT count(*) AS n FROM public.mv_base", []string{"n"}, []string{"INT8"}, [][]any{{int64(4)}}))
			}
			check()
			if !b.pg {
				for range 2 {
					var dump bytes.Buffer
					if err := stuntgres.Dump(p.ctx, p.native, &dump); err != nil {
						t.Fatal(err)
					}
					if err := stuntgres.Restore(p.ctx, p.native, bytes.NewReader(dump.Bytes())); err != nil {
						t.Fatal(err)
					}
					check()
				}
				materializedViewRetainedBag(t, retained)
				// A second pool on the same public connector sees the imported stale rows.
				peer := sql.OpenDB(p.nativeConnector)
				defer peer.Close()
				rows, err := peer.QueryContext(p.ctx, "SELECT i,s FROM public.mv_collision ORDER BY i")
				if err != nil {
					t.Fatal(err)
				}
				materializedViewRetainedBag(t, rows)
			}
			run(referenceExec("DROP TABLE pg_temp.mv_collision", 0))
			run(referenceExec("DROP MATERIALIZED VIEW public.mv_collision", 0))
		})
	}
}
