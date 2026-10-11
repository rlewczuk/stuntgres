package fixture

import (
	"testing"
)

func CatalogControlSQLWitness(t *testing.T, p *Pair) {
	t.Helper()
	for _, backend := range p.Backends() {
		t.Run(backend.Name+"/committed-private-rollback", func(t *testing.T) {
			_, a, b := IsolationReferenceConnections(t, p, backend.PG)
			run := func(x Executor, s Step) { RunStep(t, p.Ctx, x, backend.Recorder, s) }
			run(b, Exec("CREATE TABLE control_witness(v INT NOT NULL)", 0))
			run(b, Exec("INSERT INTO control_witness VALUES(77)", 1))
			run(a, Exec("BEGIN", 0))
			run(a, Exec("INSERT INTO control_witness VALUES(1)", 1))
			run(a, Select("SELECT v FROM control_witness ORDER BY v", []string{"v"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(77)}}))
			run(b, Select("SELECT v FROM control_witness", []string{"v"}, []string{"INT4"}, [][]any{{int64(77)}}))
			run(a, Exec("ROLLBACK", 0))
			run(b, Prepared(Select("SELECT v FROM control_witness", []string{"v"}, []string{"INT4"}, [][]any{{int64(77)}})))
			run(b, Exec("DROP TABLE control_witness", 0))
		})
	}
}
func AssertControlDiagnostic(t *testing.T, err error, want Diagnostic) {
	t.Helper()
	AssertDiagnostic(t, err, &want)
}
