package reference_test

import (
	"context"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePGStatioUserSequencesFeatureSmoke(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			producer, observer := fixture.LockConnections(t, p.Ctx, b.DB)
			statioSequenceExec(t, p, b, producer, "CREATE SEQUENCE public.statio_user_smoke CACHE 3")
			oid := statioSequenceOID(t, p, producer, "public.statio_user_smoke")
			stmt, err := observer.PrepareContext(p.Ctx, statioUserSequenceProbe)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := fixture.HeldStatement{Executor: observer, Statement: stmt, SQL: statioUserSequenceProbe}
			statioUserSequenceWitness(t, p, b, observer, held, oid, "public", "statio_user_smoke", 0, 0)
			statioSequenceExec(t, p, b, producer, "BEGIN")
			defer producer.ExecContext(context.Background(), "ROLLBACK")
			for n := int64(1); n <= 4; n++ {
				pgSequencesScalar(t, p, b, producer, "SELECT nextval('public.statio_user_smoke')", "nextval", n)
			}
			statioSequenceExec(t, p, b, producer, "COMMIT")
			statioSequenceRetire(t, producer)
			statioUserSequenceWitness(t, p, b, observer, held, oid, "public", "statio_user_smoke", 1, 1)
			// The simple user view rewrites to the non-updatable all view.
			s := fixture.ErrorDiagnostic("UPDATE pg_catalog.pg_statio_user_sequences SET blks_hit=0 WHERE false", statioSequenceWriteDiagnostic("update"))
			fixture.RunStep(t, p.Ctx, observer, b.Recorder, s)
			statioUserSequenceWitness(t, p, b, observer, held, oid, "public", "statio_user_smoke", 1, 1)
			statioSequenceExec(t, p, b, observer, "DROP SEQUENCE public.statio_user_smoke")
			statioUserSequenceWitness(t, p, b, observer, held, oid, "", "", 0, 0)
		})
	}
}
