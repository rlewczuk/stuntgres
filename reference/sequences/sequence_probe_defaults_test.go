package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceSequenceProbeDefaultsSessionsSetval(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			a, b := fixture.LockConnections(t, p.Ctx, backend.DB)
			fixture.SequenceProbeExec(t, p.Ctx, a, "CREATE SEQUENCE s")
			fixture.SequenceProbeExec(t, p.Ctx, a, "CREATE SEQUENCE t")
			for _, tc := range []struct{ q, msg string }{{"SELECT currval('s')", `currval of sequence "s" is not yet defined in this session`}, {"SELECT lastval()", "lastval is not yet defined in this session"}} {
				want := fixture.ControlsReferenceDiagnostic("55000", tc.msg)
				fixture.SequenceProbeError(t, p.Ctx, a, tc.q, &want)
			}
			stmt, err := a.PrepareContext(p.Ctx, "SELECT nextval('s')")
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := fixture.HeldStatement{Executor: a, Statement: stmt, SQL: "SELECT nextval('s')"}
			for _, want := range []int64{1, 2} {
				fixture.RunStep(t, p.Ctx, held, backend.Recorder, fixture.Step{Query: "SELECT nextval('s')", SelectRows: true, Columns: []string{"nextval"}, Types: []string{"INT8"}, Rows: [][]any{{want}}})
			}
			fixture.SequenceProbeValue(t, p.Ctx, b, "SELECT nextval('s')", 3)
			fixture.SequenceProbeValue(t, p.Ctx, a, "SELECT currval('s')", 2)
			fixture.SequenceProbeValue(t, p.Ctx, a, "SELECT setval('s',42)", 42)
			fixture.SequenceProbeValue(t, p.Ctx, a, "SELECT nextval('s')", 43)
			fixture.SequenceProbeValue(t, p.Ctx, a, "SELECT setval('s',42,false)", 42)
			fixture.SequenceProbeValue(t, p.Ctx, a, "SELECT currval('s')", 43)
			fixture.SequenceProbeValue(t, p.Ctx, a, "SELECT nextval('s')", 42)
			fixture.SequenceProbeValue(t, p.Ctx, a, "SELECT setval('t',80,true)", 80)
			fixture.SequenceProbeValue(t, p.Ctx, a, "SELECT lastval()", 42)
			fixture.SequenceProbeValue(t, p.Ctx, a, "SELECT setval('s',90,true)", 90)
			fixture.SequenceProbeValue(t, p.Ctx, a, "SELECT lastval()", 90)
			fixture.SequenceProbeExec(t, p.Ctx, a, "BEGIN")
			fixture.SequenceProbeValue(t, p.Ctx, a, "SELECT nextval('s')", 91)
			fixture.SequenceProbeExec(t, p.Ctx, a, "ROLLBACK")
			fixture.SequenceProbeValue(t, p.Ctx, b, "SELECT nextval('s')", 92)
			fixture.SequenceProbeExec(t, p.Ctx, a, "BEGIN")
			fixture.SequenceProbeValue(t, p.Ctx, a, "SELECT setval('s',100,false)", 100)
			fixture.SequenceProbeExec(t, p.Ctx, a, "ROLLBACK")
			fixture.SequenceProbeValue(t, p.Ctx, b, "SELECT nextval('s')", 100)
			for _, q := range []string{"SELECT nextval(NULL)", "SELECT currval(NULL)", "SELECT setval(NULL,42)", "SELECT setval('s',NULL)", "SELECT setval('s',42,NULL)"} {
				var got any
				if err := a.QueryRowContext(p.Ctx, q).Scan(&got); err != nil || got != nil {
					t.Fatal(q, got, err)
				}
			}
		})
	}
}
