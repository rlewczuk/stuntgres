package reference_test

import "testing"

func catalogControlSQLWitness(t *testing.T, p *referencePair) {
	t.Helper()
	for _, backend := range p.backends() {
		t.Run(backend.name+"/committed-private-rollback", func(t *testing.T) {
			_, a, b := isolationReferenceConnections(t, p, backend.pg)
			run := func(x referenceExecutor, s referenceStep) { referenceRunStep(t, p.ctx, x, backend.recorder, s) }
			run(b, referenceExec("CREATE TABLE control_witness(v INT NOT NULL)", 0))
			run(b, referenceExec("INSERT INTO control_witness VALUES(77)", 1))
			run(a, referenceExec("BEGIN", 0))
			run(a, referenceExec("INSERT INTO control_witness VALUES(1)", 1))
			run(a, referenceSelect("SELECT v FROM control_witness ORDER BY v", []string{"v"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(77)}}))
			run(b, referenceSelect("SELECT v FROM control_witness", []string{"v"}, []string{"INT4"}, [][]any{{int64(77)}}))
			run(a, referenceExec("ROLLBACK", 0))
			run(b, referencePrepared(referenceSelect("SELECT v FROM control_witness", []string{"v"}, []string{"INT4"}, [][]any{{int64(77)}})))
			run(b, referenceExec("DROP TABLE control_witness", 0))
		})
	}
}
func referenceAssertControlDiagnostic(t *testing.T, err error, want referenceDiagnostic) {
	t.Helper()
	referenceAssertDiagnostic(t, err, &want)
}
