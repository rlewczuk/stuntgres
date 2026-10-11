package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceLogicalConcurrentPreparedFrames(t *testing.T) {
	p := fixture.NewScenario(t)
	fixture.ScalarStep(t, p, fixture.Exec("CREATE TABLE t(id INT)", 0))
	fixture.ScalarStep(t, p, fixture.Exec("INSERT INTO t VALUES(1)", 1))
	q := "SELECT $1 AND $1,NOT $1 FROM t"
	for _, backend := range p.Backends() {
		held, err := backend.DB.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer func() {
			if err := held.Close(); err != nil {
				t.Error(err)
			}
		}()
		fixture.ScalarConcurrentFrames(t, p, backend, held, q, 12, []string{"?column?", "?column?"}, []string{"BOOL", "BOOL"}, func(n int) ([]any, [][]any) { arg := n%2 == 0; return []any{arg}, [][]any{{arg, !arg}} })
		if err := held.Close(); err != nil {
			t.Fatal(err)
		}
	}
}
