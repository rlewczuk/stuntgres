package fixture

import (
	"fmt"
	"regexp"
	"testing"
)

func ExclusionReferenceScenario(t *testing.T, body func(*testing.T, *Pair, Backend)) {
	t.Helper()
	p := NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) { body(t, p, backend) })
	}
}

func ExclusionReferenceConflict(table, constraint, labels, proposed, existing string) Diagnostic {
	d := ControlsReferenceDiagnostic("23P01", fmt.Sprintf(`conflicting key value violates exclusion constraint "%s"`, constraint))
	d.Schema, d.Table, d.Constraint = "public", table, constraint
	d.Detail = fmt.Sprintf("Key (%s)=(%s) conflicts with existing key (%s)=(%s).", labels, proposed, labels, existing)
	return d
}

func ExclusionReferenceBuild(table, constraint, labels, value string) Diagnostic {
	d := ExclusionReferenceConflict(table, constraint, labels, value, value)
	d.Message = fmt.Sprintf(`could not create exclusion constraint "%s"`, constraint)
	d.Detail = fmt.Sprintf("Key (%s)=(%s) conflicts with key (%s)=(%s).", labels, value, labels, value)
	return d
}

func ExclusionReferenceRun(t *testing.T, p *Pair, b Backend, x Executor, s Step) {
	t.Helper()
	RunStep(t, p.Ctx, x, b.Recorder, s)
}

func ExclusionReferenceRows(q string, values ...int64) Step {
	return TransactionRows(false, q, values...)
}

// The release reports physical heap coordinates during exclusion peer waits.
// Native diagnostics cannot claim heap CTIDs; retain its logical empty Context.
// Validate the entire PostgreSQL context grammar instead of dropping the field.
func ExclusionReferenceWaitDiagnostic(t *testing.T, err error, code, message, table string, postgres bool) {
	t.Helper()
	got := ExtractDiagnostic(t, err)
	want := ControlsReferenceDiagnostic(code, message)
	if postgres {
		pattern := regexp.MustCompile(`^while checking exclusion constraint on tuple \([0-9]+,[1-9][0-9]*\) in relation "` + regexp.QuoteMeta(table) + `"$`)
		if !pattern.MatchString(got.Context) {
			t.Fatalf("exclusion physical tuple Context got %q", got.Context)
		}
		want.Context = got.Context
	}
	AssertDiagnostic(t, err, &want)
}
