//go:build reference

package reference_test

import "testing"

func queryAggregateRegressionSteps() []referenceStep {
	steps := []referenceStep{
		referenceSelect("SELECT k,count(*) FROM t GROUP BY k", []string{"k", "count"}, []string{"TEXT", "INT8"}, [][]any{{"a", int64(2)}, {nil, int64(1)}}),
		referenceSelect("SELECT count(*) FROM empty", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}),
		referenceSelect("SELECT k,count(*) FROM empty GROUP BY k", []string{"k", "count"}, []string{"TEXT", "INT8"}, nil),
		referenceSelect("SELECT k,count(*) FROM t GROUP BY k HAVING count(*)>1", []string{"k", "count"}, []string{"TEXT", "INT8"}, [][]any{{"a", int64(2)}}),
		referenceSelect("SELECT k FROM t GROUP BY k HAVING count(*)>1", []string{"k"}, []string{"TEXT"}, [][]any{{"a"}}),
		referenceSelect("SELECT 7 FROM empty HAVING TRUE", []string{"?column?"}, []string{"INT4"}, [][]any{{int64(7)}}),
		referenceSelect("SELECT (v+1)*2,count(*) FROM t GROUP BY v+1", []string{"?column?", "count"}, []string{"INT4", "INT8"}, [][]any{{int64(4), int64(1)}, {nil, int64(1)}, {int64(6), int64(1)}}),
	}
	for i := range steps {
		steps[i].bag = true
	}
	for _, tc := range []struct{ query, relation, position string }{
		{"SELECT v,count(*) FROM t", "t", "8"},
		{"SELECT /*é*/ v,count(*) FROM t", "t", "14"},
		{"SELECT v,count(*) FROM empty HAVING FALSE", "empty", "8"},
		{"SELECT v,count(*) FROM t GROUP BY v+1", "t", "8"},
	} {
		step := referenceImplicitError(tc.query, referenceDiagnostic{Code: "42803", Message: `column "` + tc.relation + `.v" must appear in the GROUP BY clause or be used in an aggregate function`, Position: tc.position})
		step.selectRows = true
		steps = append(steps, step)
	}
	return steps
}

func TestReferenceQueryAggregateRegression(t *testing.T) {
	p := newReferencePair(t)
	for _, step := range []referenceStep{
		referenceExec("CREATE TABLE t(k TEXT,v INT)", 0),
		referenceExec("INSERT INTO t VALUES('a',1),('a',NULL),(NULL,2)", 3),
		referenceExec("CREATE TABLE empty(k TEXT,v INT)", 0),
	} {
		p.step(t, step)
	}
	// Assert the pinned oracle before exercising the not-yet-implemented backend.
	// This uses the same recording/metadata/diagnostic checks as paired scenarios.
	t.Run("postgres_first", func(t *testing.T) {
		for _, step := range queryAggregateRegressionSteps() {
			t.Run(step.query, func(t *testing.T) {
				t.Run("direct", func(t *testing.T) {
					referenceRunStep(t, p.ctx, p.postgres, p.postgresRecord, step)
				})
				t.Run("prepared", func(t *testing.T) {
					referenceRunStep(t, p.ctx, p.postgres, p.postgresRecord, referencePrepared(step))
				})
			})
		}
	})
	t.Run("paired", func(t *testing.T) {
		for _, step := range queryAggregateRegressionSteps() {
			t.Run(step.query, func(t *testing.T) {
				t.Run("direct", func(t *testing.T) { p.step(t, step) })
				t.Run("prepared", func(t *testing.T) { p.step(t, referencePrepared(step)) })
			})
		}
	})
}
