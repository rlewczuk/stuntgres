package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

// The notice connector supplies both owners, including prepared-registry cleanup.
func batchReviewControlsScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, fixture.ControlsReferenceFixture)) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			f := fixture.NewControlsReferenceFixture(t, p, b.PG)
			b.DB, b.Recorder = f.DB, f.Recorder
			body(t, p, b, f)
		})
	}
}

func batchRefreshSeed(t *testing.T, p *fixture.Pair, b fixture.Backend, x fixture.Executor) {
	t.Helper()
	batchExec(t, p, b, x, "CREATE TABLE batch_refresh_base(i INT,s TEXT)", 0)
	batchExec(t, p, b, x, "INSERT INTO batch_refresh_base VALUES(1,'a'),(1,'a'),(2,NULL)", 3)
	batchExec(t, p, b, x, "CREATE MATERIALIZED VIEW batch_refresh_mv AS SELECT i,s FROM batch_refresh_base", 3)
}

func batchRefreshSet(rows ...[]any) fixture.BatchSet {
	return fixture.BatchSet{[]string{"i", "s"}, []string{"INT4", "TEXT"}, rows, true}
}

func batchRefreshOld() [][]any { return [][]any{{int64(1), "a"}, {int64(1), "a"}, {int64(2), nil}} }

func batchRefreshRows(t *testing.T, p *fixture.Pair, b fixture.Backend, x fixture.Executor, name string, rows [][]any) {
	t.Helper()
	step := fixture.Select("SELECT i,s FROM "+name, []string{"i", "s"}, []string{"INT4", "TEXT"}, rows)
	step.Bag = true
	fixture.RunStep(t, p.Ctx, x, b.Recorder, step)
}

func batchRefreshPopulation(t *testing.T, p *fixture.Pair, b fixture.Backend, x fixture.Executor, name string, populated bool) {
	t.Helper()
	fixture.MaterializedViewPopulation(func(s fixture.Step) { fixture.RunStep(t, p.Ctx, x, b.Recorder, s) }, name, populated)
}
