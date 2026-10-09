package reference_test

import "testing"

func TestReferenceCompareNullQualifiedReferences(t *testing.T) {
	p := expressionReferenceSetup(t)
	for _, q := range []string{"SELECT extra.v IS NULL FROM t extra ORDER BY id", "SELECT extra.v IS NULL FROM t extra JOIN t rhs USING(id) ORDER BY id"} {
		step := referenceSelect(q, []string{"?column?"}, []string{"BOOL"}, [][]any{{false}, {true}, {false}, {false}})
		scalarStep(t, p, step)
		scalarStep(t, p, referencePrepared(step))
	}
}

func TestReferenceCompareNullFunctionBoundaries(t *testing.T) {
	p := expressionReferenceSetup(t)
	for _, tc := range []struct {
		q    string
		step referenceStep
	}{
		{"SELECT num_nulls(v) IS NULL FROM t", referenceSelect("SELECT num_nulls(v) IS NULL FROM t", []string{"?column?"}, []string{"BOOL"}, [][]any{{false}, {false}, {false}, {false}})},
		{"SELECT num_nulls(v IS NULL) FROM t", referenceSelect("SELECT num_nulls(v IS NULL) FROM t", []string{"num_nulls"}, []string{"INT4"}, [][]any{{int64(0)}, {int64(0)}, {int64(0)}, {int64(0)}})},
		{"SELECT (num_nulls(v)) IS NULL FROM t", referenceSelect("SELECT (num_nulls(v)) IS NULL FROM t", []string{"?column?"}, []string{"BOOL"}, [][]any{{false}, {false}, {false}, {false}})},
		{"SELECT num_nulls((v IS NULL)) FROM t", referenceSelect("SELECT num_nulls((v IS NULL)) FROM t", []string{"num_nulls"}, []string{"INT4"}, [][]any{{int64(0)}, {int64(0)}, {int64(0)}, {int64(0)}})},
		{"SELECT num_nulls(v)=1 FROM t", referenceSelect("SELECT num_nulls(v)=1 FROM t", []string{"?column?"}, []string{"BOOL"}, [][]any{{false}, {true}, {false}, {false}})},
		{"SELECT 1=num_nulls(v) FROM t", referenceSelect("SELECT 1=num_nulls(v) FROM t", []string{"?column?"}, []string{"BOOL"}, [][]any{{false}, {true}, {false}, {false}})},
		{"SELECT num_nulls(v) AND TRUE FROM t", referenceImplicitError("SELECT num_nulls(v) AND TRUE FROM t", referenceDiagnostic{Code: "42804", Message: "argument of AND must be type boolean, not type integer", Position: "8"})},
		{"SELECT TRUE OR num_nulls(v) FROM t", referenceImplicitError("SELECT TRUE OR num_nulls(v) FROM t", referenceDiagnostic{Code: "42804", Message: "argument of OR must be type boolean, not type integer", Position: "16"})},
		{"SELECT id FROM t WHERE num_nulls(v) IS NULL", referenceSelect("SELECT id FROM t WHERE num_nulls(v) IS NULL", []string{"id"}, []string{"INT4"}, nil)},
	} {
		t.Run(tc.q, func(t *testing.T) {
			rows, err := p.memory.QueryContext(p.ctx, tc.q)
			if rows != nil {
				rows.Close()
				t.Fatal("native exclusion exposed cursor")
			}
			if err == nil {
				t.Fatal("native exclusion succeeded")
			}
			held, err := p.memory.PrepareContext(p.ctx, tc.q)
			if held != nil {
				held.Close()
				t.Fatal("native exclusion exposed handle")
			}
			if err == nil {
				t.Fatal("native exclusion prepared")
			}
			if p.postgres != nil {
				step := tc.step
				step.selectRows = true
				step.errorStage = "execute"
				step.bag = true
				scalarRunStep(t, p.ctx, p.postgres, p.postgresRecord, step)
				if step.diagnostic != nil {
					step.errorStage = "prepare"
				}
				scalarRunStep(t, p.ctx, p.postgres, p.postgresRecord, referencePrepared(step))
			}
		})
	}
}
