package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceArithmeticCastInputDiagnostics(t *testing.T) {
	p := fixture.NewScenario(t)
	p.Step(t, fixture.Exec("CREATE TABLE d(i INT)", 0))
	for _, tc := range []struct{ query, position string }{
		{"INSERT INTO d VALUES(CAST(i+1 AS int4))", "27"},
		{"INSERT INTO d VALUES((i+1)::int4)", "23"},
	} {
		pg := fixture.Diagnostic{Code: "42703", Message: `column "i" does not exist`, Detail: `There is a column named "i" in table "d", but it cannot be referenced from this part of the query.`, Position: tc.position}
		s := fixture.ImplicitError(tc.query, pg)
		s.ErrorStage = "execute"
		referenceArithmeticFailure(t, p, s)
		s.ErrorStage = "prepare"
		referenceArithmeticFailure(t, p, fixture.Prepared(s))
	}
	const script = "CREATE TABLE prefix(i INT); INSERT INTO d VALUES((i+1)::int4)"
	native := fixture.ImplicitError(script, fixture.Diagnostic{Code: "42703", Message: `column "i" does not exist`, Detail: `There is a column named "i" in table "d", but it cannot be referenced from this part of the query.`, Position: "51"})
	native.ErrorStage = "execute"
	referenceArithmeticFailure(t, p, native)
	missing := fixture.ImplicitError("SELECT i FROM prefix", fixture.Diagnostic{Code: "42P01", Message: `relation "prefix" does not exist`, Position: "15"})
	missing.SelectRows, missing.ErrorStage = true, "execute"
	p.Step(t, missing)
	missing.ErrorStage = "prepare"
	p.Step(t, fixture.Prepared(missing))
	preparedPG := fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42601", Message: "cannot insert multiple commands into a prepared statement"}
	prepared := fixture.ImplicitError(script, preparedPG)
	prepared.ErrorStage = "prepare"
	referenceArithmeticFailure(t, p, fixture.Prepared(prepared))
	missing.ErrorStage = "execute"
	p.Step(t, missing)
	missing.ErrorStage = "prepare"
	p.Step(t, fixture.Prepared(missing))
	empty := fixture.Select("SELECT i FROM d", []string{"i"}, []string{"INT4"}, nil)
	p.Step(t, empty)
	p.Step(t, fixture.Prepared(empty))
}
