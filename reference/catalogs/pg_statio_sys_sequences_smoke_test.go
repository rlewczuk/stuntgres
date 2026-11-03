package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePGStatioSysSequencesFeatureSmoke(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			producer, observer := fixture.LockConnections(t, p.Ctx, b.DB)
			statioSequenceExec(t, p, b, producer, "CREATE SEQUENCE statio_sys_smoke CACHE 3")
			oid := statioSequenceOID(t, p, producer, "public.statio_sys_smoke")
			stmt, err := observer.PrepareContext(p.Ctx, statioSysSequenceProbe)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := fixture.HeldStatement{Executor: observer, Statement: stmt, SQL: statioSysSequenceProbe}
			if got := statioSequenceRows(t, p, b, observer, "SELECT * FROM pg_catalog.pg_statio_sys_sequences"); len(got) != 0 {
				t.Fatal("stock system inventory", got)
			}
			statioSysSequenceEmpty(t, p, b, held, oid)
			statioSequenceExec(t, p, b, producer, "BEGIN")
			for n := int64(1); n <= 4; n++ {
				pgSequencesScalar(t, p, b, producer, "SELECT nextval('statio_sys_smoke')", "nextval", n)
			}
			statioSequenceExec(t, p, b, producer, "COMMIT")
			statioSysUserWitness(t, p, b, observer, oid, "public", "statio_sys_smoke", 1, 1)
			statioSysSequenceEmpty(t, p, b, held, oid)
			s := fixture.ErrorDiagnostic("UPDATE pg_catalog.pg_statio_sys_sequences SET blks_hit=0 WHERE false", statioSequenceWriteDiagnostic("update"))
			fixture.RunStep(t, p.Ctx, observer, b.Recorder, s)
			statioSequenceExec(t, p, b, producer, "DROP SEQUENCE statio_sys_smoke")
			statioSysSequenceEmpty(t, p, b, held, oid)
			statioSysUserWitness(t, p, b, observer, oid, "", "", 0, 0)
		})
	}
}
