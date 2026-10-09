package reference_test

import (
	"testing"
)

func TestReferenceSequenceProbeDefaultsSessionsSetval(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			a, b := referenceLockConnections(t, p.ctx, backend.db)
			sequenceProbeExec(t, p.ctx, a, "CREATE SEQUENCE s")
			sequenceProbeExec(t, p.ctx, a, "CREATE SEQUENCE t")
			for _, tc := range []struct{ q, msg string }{{"SELECT currval('s')", `currval of sequence "s" is not yet defined in this session`}, {"SELECT lastval()", "lastval is not yet defined in this session"}} {
				want := controlsReferenceDiagnostic("55000", tc.msg)
				sequenceProbeError(t, p.ctx, a, tc.q, &want)
			}
			stmt, err := a.PrepareContext(p.ctx, "SELECT nextval('s')")
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := referenceHeldStatement{referenceExecutor: a, statement: stmt, query: "SELECT nextval('s')"}
			for _, want := range []int64{1, 2} {
				referenceRunStep(t, p.ctx, held, backend.recorder, referenceStep{query: "SELECT nextval('s')", selectRows: true, columns: []string{"nextval"}, types: []string{"INT8"}, rows: [][]any{{want}}})
			}
			sequenceProbeValue(t, p.ctx, b, "SELECT nextval('s')", 3)
			sequenceProbeValue(t, p.ctx, a, "SELECT currval('s')", 2)
			sequenceProbeValue(t, p.ctx, a, "SELECT setval('s',42)", 42)
			sequenceProbeValue(t, p.ctx, a, "SELECT nextval('s')", 43)
			sequenceProbeValue(t, p.ctx, a, "SELECT setval('s',42,false)", 42)
			sequenceProbeValue(t, p.ctx, a, "SELECT currval('s')", 43)
			sequenceProbeValue(t, p.ctx, a, "SELECT nextval('s')", 42)
			sequenceProbeValue(t, p.ctx, a, "SELECT setval('t',80,true)", 80)
			sequenceProbeValue(t, p.ctx, a, "SELECT lastval()", 42)
			sequenceProbeValue(t, p.ctx, a, "SELECT setval('s',90,true)", 90)
			sequenceProbeValue(t, p.ctx, a, "SELECT lastval()", 90)
			sequenceProbeExec(t, p.ctx, a, "BEGIN")
			sequenceProbeValue(t, p.ctx, a, "SELECT nextval('s')", 91)
			sequenceProbeExec(t, p.ctx, a, "ROLLBACK")
			sequenceProbeValue(t, p.ctx, b, "SELECT nextval('s')", 92)
			sequenceProbeExec(t, p.ctx, a, "BEGIN")
			sequenceProbeValue(t, p.ctx, a, "SELECT setval('s',100,false)", 100)
			sequenceProbeExec(t, p.ctx, a, "ROLLBACK")
			sequenceProbeValue(t, p.ctx, b, "SELECT nextval('s')", 100)
			for _, q := range []string{"SELECT nextval(NULL)", "SELECT currval(NULL)", "SELECT setval(NULL,42)", "SELECT setval('s',NULL)", "SELECT setval('s',42,NULL)"} {
				var got any
				if err := a.QueryRowContext(p.ctx, q).Scan(&got); err != nil || got != nil {
					t.Fatal(q, got, err)
				}
			}
		})
	}
}
