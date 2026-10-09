package reference_test

import "testing"

func TestReferenceArithmeticCastInputDiagnostics(t *testing.T) {
	p := newReferenceScenario(t)
	p.step(t, referenceExec("CREATE TABLE d(i INT)", 0))
	for _, tc := range []struct{ query, position string }{
		{"INSERT INTO d VALUES(CAST(i+1 AS int4))", "27"},
		{"INSERT INTO d VALUES((i+1)::int4)", "23"},
	} {
		native := referenceDiagnostic{Code: "0A000", Message: "column references are unsupported in scalar inputs", Position: tc.position}
		pg := referenceDiagnostic{Code: "42703", Message: `column "i" does not exist`, Detail: `There is a column named "i" in table "d", but it cannot be referenced from this part of the query.`, Position: tc.position}
		s := referenceImplicitError(tc.query, native)
		pg.Severity, pg.SeverityUnlocalized = "ERROR", "ERROR"
		s.postgresDiagnostic = &pg
		s.errorStage = "execute"
		referenceArithmeticFailure(t, p, s)
		s.errorStage = "prepare"
		referenceArithmeticFailure(t, p, referencePrepared(s))
	}
	const script = "CREATE TABLE prefix(i INT); INSERT INTO d VALUES((i+1)::int4)"
	native := referenceImplicitError(script, referenceDiagnostic{Code: "0A000", Message: "column references are unsupported in scalar inputs", Position: "51"})
	pg := referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42703", Message: `column "i" does not exist`, Detail: `There is a column named "i" in table "d", but it cannot be referenced from this part of the query.`, Position: "51"}
	native.postgresDiagnostic, native.errorStage = &pg, "execute"
	referenceArithmeticFailure(t, p, native)
	missing := referenceImplicitError("SELECT i FROM prefix", referenceDiagnostic{Code: "42P01", Message: `relation "prefix" does not exist`, Position: "15"})
	missing.selectRows, missing.errorStage = true, "execute"
	p.step(t, missing)
	missing.errorStage = "prepare"
	p.step(t, referencePrepared(missing))
	preparedPG := referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42601", Message: "cannot insert multiple commands into a prepared statement"}
	native.postgresDiagnostic, native.errorStage = &preparedPG, "prepare"
	referenceArithmeticFailure(t, p, referencePrepared(native))
	missing.errorStage = "execute"
	p.step(t, missing)
	missing.errorStage = "prepare"
	p.step(t, referencePrepared(missing))
	empty := referenceSelect("SELECT i FROM d", []string{"i"}, []string{"INT4"}, nil)
	p.step(t, empty)
	p.step(t, referencePrepared(empty))
}
