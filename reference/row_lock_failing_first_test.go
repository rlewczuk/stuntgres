package reference_test

import "testing"

func rowLockFailingFirstSteps() []referenceStep {
	var steps []referenceStep
	for _, clause := range []string{"FOR KEY SHARE", "FOR SHARE", "FOR NO KEY UPDATE", "FOR UPDATE"} {
		steps = append(steps, referenceSelect("SELECT id,v FROM t WHERE id=$1 ORDER BY id "+clause, []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(10)}}, 1))
	}
	joined := referenceSelect("SELECT a.id,a.v FROM a INNER JOIN b ON a.id=b.id WHERE a.id=$1 FOR SHARE", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(10)}, {int64(1), int64(10)}}, 1)
	joined.bag = true
	return append(steps, joined)
}

func TestReferenceRowLockFailingFirst(t *testing.T) {
	p := newReferenceScenario(t)
	for _, step := range []referenceStep{
		referenceExec("CREATE TABLE t(id INT,v INT)", 0),
		referenceExec("INSERT INTO t VALUES(1,10)", 1),
		referenceExec("CREATE TABLE a(id INT,v INT)", 0),
		referenceExec("CREATE TABLE b(id INT)", 0),
		referenceExec("INSERT INTO a VALUES(1,10),(1,10),(2,20)", 3),
		referenceExec("INSERT INTO b VALUES(1),(3)", 2),
	} {
		p.step(t, step)
	}
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			for _, step := range rowLockFailingFirstSteps() {
				t.Run(step.query, func(t *testing.T) {
					t.Run("direct", func(t *testing.T) { referenceRunStep(t, p.ctx, backend.db, backend.recorder, step) })
					t.Run("prepared", func(t *testing.T) { referenceRunStep(t, p.ctx, backend.db, backend.recorder, referencePrepared(step)) })
				})
			}
		})
	}
}
