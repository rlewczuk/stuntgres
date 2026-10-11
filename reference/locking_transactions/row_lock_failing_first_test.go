package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func rowLockFailingFirstSteps() []fixture.Step {
	var steps []fixture.Step
	for _, clause := range []string{"FOR KEY SHARE", "FOR SHARE", "FOR NO KEY UPDATE", "FOR UPDATE"} {
		steps = append(steps, fixture.Select("SELECT id,v FROM t WHERE id=$1 ORDER BY id "+clause, []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(10)}}, 1))
	}
	joined := fixture.Select("SELECT a.id,a.v FROM a INNER JOIN b ON a.id=b.id WHERE a.id=$1 FOR SHARE", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(10)}, {int64(1), int64(10)}}, 1)
	joined.Bag = true
	return append(steps, joined)
}

func TestReferenceRowLockFailingFirst(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, step := range []fixture.Step{
		fixture.Exec("CREATE TABLE t(id INT,v INT)", 0),
		fixture.Exec("INSERT INTO t VALUES(1,10)", 1),
		fixture.Exec("CREATE TABLE a(id INT,v INT)", 0),
		fixture.Exec("CREATE TABLE b(id INT)", 0),
		fixture.Exec("INSERT INTO a VALUES(1,10),(1,10),(2,20)", 3),
		fixture.Exec("INSERT INTO b VALUES(1),(3)", 2),
	} {
		p.Step(t, step)
	}
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			for _, step := range rowLockFailingFirstSteps() {
				t.Run(step.Query, func(t *testing.T) {
					t.Run("direct", func(t *testing.T) { fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, step) })
					t.Run("prepared", func(t *testing.T) { fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Prepared(step)) })
				})
			}
		})
	}
}
