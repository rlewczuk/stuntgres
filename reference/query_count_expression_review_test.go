package reference_test

import (
	"strings"
	"testing"
)

func countExpressionReviewRegressionSteps() []referenceStep {
	// Independently specified from PostgreSQL REL_18_0
	// parse_func.c:75-79,134-146,635-644,784-793 and parse_agg.c:675-698.
	// The root's pinned PostgreSQL 18.0 postgres_first direct/prepared checkpoints
	// validated every full record, including F4 namespace precedence.
	// Children precede the guard and namespace/signature lookup; only a
	// resolved aggregate receives the nesting check.
	// referenceImplicitError supplies ERROR/ERROR; all unlisted portable
	// fields must remain empty. PostgreSQL C source locations are nonportable.
	const hint = "No function matches the given name and argument types. You might need to add explicit type casts."
	args100 := strings.Repeat("1,", 99) + "1"
	args101 := strings.Repeat("1,", 100) + "1"
	var steps []referenceStep
	for _, tc := range []struct {
		call string
		want referenceDiagnostic
	}{
		{"pg_catalog.count()", referenceDiagnostic{Code: "42809", Message: "pg_catalog.count(*) must be used to call a parameterless aggregate function", Position: "8"}},
		{"/*é*/ pg_catalog.count()", referenceDiagnostic{Code: "42809", Message: "pg_catalog.count(*) must be used to call a parameterless aggregate function", Position: "14"}},
		{"count(count(*),missing)", referenceDiagnostic{Code: "42703", Message: `column "missing" does not exist`, Position: "23"}},
		{"count(count(*),1)", referenceDiagnostic{Code: "42883", Message: "function count(bigint, integer) does not exist", Hint: hint, Position: "8"}},
		{"count(count(*))", referenceDiagnostic{Code: "42803", Message: "aggregate function calls cannot be nested", Position: "14"}},
		{"count(" + args100 + ")", referenceDiagnostic{Code: "42883", Message: "function count(" + strings.Repeat("integer, ", 99) + "integer) does not exist", Hint: hint, Position: "8"}},
		{"count(" + args101 + ")", referenceDiagnostic{Code: "54023", Message: "cannot pass more than 100 arguments to a function", Position: "8"}},
		{"count(" + strings.Repeat("1,", 100) + "missing)", referenceDiagnostic{Code: "42703", Message: `column "missing" does not exist`, Position: "214"}},
		{"count(count(*)," + args100 + ")", referenceDiagnostic{Code: "54023", Message: "cannot pass more than 100 arguments to a function", Position: "8"}},
		{"count(count(*)," + strings.Repeat("1,", 99) + "missing)", referenceDiagnostic{Code: "42703", Message: `column "missing" does not exist`, Position: "221"}},
		{"count_missing_schema.count(missing)", referenceDiagnostic{Code: "42703", Message: `column "missing" does not exist`, Position: "35"}},
		{"count_missing_schema.count(" + args101 + ")", referenceDiagnostic{Code: "54023", Message: "cannot pass more than 100 arguments to a function", Position: "8"}},
		{"count_missing_schema.count(1)", referenceDiagnostic{Code: "3F000", Message: `schema "count_missing_schema" does not exist`, Position: "8"}},
		{"count_missing_schema.count(" + strings.Repeat("1,", 100) + "missing)", referenceDiagnostic{Code: "42703", Message: `column "missing" does not exist`, Position: "235"}},
	} {
		for _, source := range []string{"t", "empty", "t WHERE FALSE", "empty WHERE FALSE"} {
			step := referenceImplicitError("SELECT "+tc.call+" FROM "+source, tc.want)
			step.selectRows = true
			steps = append(steps, step)
		}
	}
	return steps
}

func TestReferenceCountExpressionReviewRegression(t *testing.T) {
	p := newReferenceScenario(t)
	for _, step := range []referenceStep{
		referenceExec("CREATE TABLE t(v INT)", 0),
		referenceExec("INSERT INTO t VALUES(1),(NULL),(3)", 3),
		referenceExec("CREATE TABLE empty(v INT)", 0),
	} {
		p.step(t, step)
	}
	steps := countExpressionReviewRegressionSteps()
	t.Run("paired", func(t *testing.T) {
		for _, step := range steps {
			t.Run(step.query, func(t *testing.T) {
				t.Run("direct", func(t *testing.T) { p.step(t, step) })
				t.Run("prepared", func(t *testing.T) { p.step(t, referencePrepared(step)) })
			})
		}
	})
}
