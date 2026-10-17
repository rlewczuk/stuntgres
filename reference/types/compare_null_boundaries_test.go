package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceCompareNullQualifiedReferences(t *testing.T) {
	p := fixture.ExpressionReferenceSetup(t)
	for _, q := range []string{"SELECT extra.v IS NULL FROM t extra ORDER BY id", "SELECT extra.v IS NULL FROM t extra JOIN t rhs USING(id) ORDER BY id"} {
		step := fixture.Select(q, []string{"?column?"}, []string{"BOOL"}, [][]any{{false}, {true}, {false}, {false}})
		fixture.ScalarStep(t, p, step)
		fixture.ScalarStep(t, p, fixture.Prepared(step))
	}
}

func TestReferenceCompareNullFunctionBoundaries(t *testing.T) {
	p := fixture.ExpressionReferenceSetup(t)
	// Calls now share comparison and null-test leaves. Assert their consumer
	// results independently in both modes, including typed Boolean failures.
	for _, step := range []fixture.Step{
		fixture.Select("SELECT num_nulls(v) IS NULL FROM t", []string{"?column?"}, []string{"BOOL"}, [][]any{{false}, {false}, {false}, {false}}),
		fixture.Select("SELECT (num_nulls(v)) IS NULL FROM t", []string{"?column?"}, []string{"BOOL"}, [][]any{{false}, {false}, {false}, {false}}),
		fixture.Select("SELECT num_nulls(v)=1 FROM t", []string{"?column?"}, []string{"BOOL"}, [][]any{{false}, {true}, {false}, {false}}),
		fixture.Select("SELECT 1=num_nulls(v) FROM t", []string{"?column?"}, []string{"BOOL"}, [][]any{{false}, {true}, {false}, {false}}),
		fixture.Select("SELECT id FROM t WHERE num_nulls(v) IS NULL", []string{"id"}, []string{"INT4"}, nil),
		fixture.ImplicitError("SELECT num_nulls(v) AND TRUE FROM t", fixture.Diagnostic{Code: "42804", Message: "argument of AND must be type boolean, not type integer", Position: "8"}),
		fixture.ImplicitError("SELECT TRUE OR num_nulls(v) FROM t", fixture.Diagnostic{Code: "42804", Message: "argument of OR must be type boolean, not type integer", Position: "16"}),
	} {
		step.SelectRows = true
		step.ErrorStage = "execute"
		step.Bag = true
		fixture.ScalarStep(t, p, step)
		if step.Diagnostic != nil {
			step.ErrorStage = "prepare"
		}
		fixture.ScalarStep(t, p, fixture.Prepared(step))
	}
	// Predicate-valued call arguments remain outside native admission.
	for _, tc := range []struct {
		q    string
		step fixture.Step
	}{
		{"SELECT num_nulls(v IS NULL) FROM t", fixture.Select("SELECT num_nulls(v IS NULL) FROM t", []string{"num_nulls"}, []string{"INT4"}, [][]any{{int64(0)}, {int64(0)}, {int64(0)}, {int64(0)}})},
		{"SELECT num_nulls((v IS NULL)) FROM t", fixture.Select("SELECT num_nulls((v IS NULL)) FROM t", []string{"num_nulls"}, []string{"INT4"}, [][]any{{int64(0)}, {int64(0)}, {int64(0)}, {int64(0)}})},
	} {
		t.Run(tc.q, func(t *testing.T) {
			rows, err := p.Memory.QueryContext(p.Ctx, tc.q)
			if rows != nil {
				rows.Close()
				t.Fatal("native exclusion exposed cursor")
			}
			if err == nil {
				t.Fatal("native exclusion succeeded")
			}
			held, err := p.Memory.PrepareContext(p.Ctx, tc.q)
			if held != nil {
				held.Close()
				t.Fatal("native exclusion exposed handle")
			}
			if err == nil {
				t.Fatal("native exclusion prepared")
			}
			if p.Postgres != nil {
				step := tc.step
				step.SelectRows = true
				step.ErrorStage = "execute"
				step.Bag = true
				fixture.ScalarRunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, step)
				if step.Diagnostic != nil {
					step.ErrorStage = "prepare"
				}
				fixture.ScalarRunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, fixture.Prepared(step))
			}
		})
	}
}
