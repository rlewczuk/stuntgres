package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePGStatioAllSequencesFeatureSmoke(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			producer, observer := fixture.LockConnections(t, p.Ctx, b.DB)
			statioSequenceExec(t, p, b, producer, "CREATE SEQUENCE statio_smoke CACHE 3")
			oid := statioSequenceOID(t, p, producer, "public.statio_smoke")
			q := "SELECT * FROM pg_catalog.pg_statio_all_sequences WHERE relname=$1 AND schemaname='public'"
			stmt, err := observer.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := fixture.HeldStatement{Executor: observer, Statement: stmt, SQL: q}
			for _, e := range []fixture.Executor{observer, held} {
				got := statioSequenceRows(t, p, b, e, q, "statio_smoke")
				if len(got) != 1 || got[0].oid != oid || got[0].schema != "public" || got[0].name != "statio_smoke" {
					t.Fatal("smoke membership", got)
				}
			}
			// Await real ordinary publication of this explicit encounter batch.
			// Returning a pooled Conn is not a terminal boundary.
			statioSequenceExec(t, p, b, producer, "BEGIN")
			for n := int64(1); n <= 4; n++ {
				pgSequencesScalar(t, p, b, producer, "SELECT nextval('statio_smoke')", "nextval", n)
			}
			statioSequenceExec(t, p, b, producer, "COMMIT")
			statioSequenceCheck(t, p, b, observer, oid, "public", "statio_smoke", 1, 1)
			s := fixture.ErrorDiagnostic("UPDATE pg_catalog.pg_statio_all_sequences SET blks_hit=0 WHERE false", statioSequenceWriteDiagnostic("update"))
			fixture.RunStep(t, p.Ctx, observer, b.Recorder, s)
			statioSequenceExec(t, p, b, producer, "DROP SEQUENCE statio_smoke")
			if got := statioSequenceRows(t, p, b, held, q, "statio_smoke"); len(got) != 0 {
				t.Fatal("smoke drop recapture", got)
			}
		})
	}
}
