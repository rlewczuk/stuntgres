package reference_test

import (
	"fmt"
	"regexp"
	"testing"
)

func exclusionReferenceScenario(t *testing.T, body func(*testing.T, *referencePair, referenceBackend)) {
	t.Helper()
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) { body(t, p, backend) })
	}
}

func exclusionReferenceConflict(table, constraint, labels, proposed, existing string) referenceDiagnostic {
	d := controlsReferenceDiagnostic("23P01", fmt.Sprintf(`conflicting key value violates exclusion constraint "%s"`, constraint))
	d.Schema, d.Table, d.Constraint = "public", table, constraint
	d.Detail = fmt.Sprintf("Key (%s)=(%s) conflicts with existing key (%s)=(%s).", labels, proposed, labels, existing)
	return d
}

func exclusionReferenceBuild(table, constraint, labels, value string) referenceDiagnostic {
	d := exclusionReferenceConflict(table, constraint, labels, value, value)
	d.Message = fmt.Sprintf(`could not create exclusion constraint "%s"`, constraint)
	d.Detail = fmt.Sprintf("Key (%s)=(%s) conflicts with key (%s)=(%s).", labels, value, labels, value)
	return d
}

func exclusionReferenceRun(t *testing.T, p *referencePair, b referenceBackend, x referenceExecutor, s referenceStep) {
	t.Helper()
	referenceRunStep(t, p.ctx, x, b.recorder, s)
}

func exclusionReferenceRows(q string, values ...int64) referenceStep {
	return referenceTransactionRows(false, q, values...)
}

// The release reports physical heap coordinates during exclusion peer waits.
// Native diagnostics cannot claim heap CTIDs; retain its logical empty Context.
// Validate the entire PostgreSQL context grammar instead of dropping the field.
func exclusionReferenceWaitDiagnostic(t *testing.T, err error, code, message string, postgres bool) {
	t.Helper()
	got := referenceExtractDiagnostic(t, err)
	want := controlsReferenceDiagnostic(code, message)
	if postgres {
		pattern := regexp.MustCompile(`^while checking exclusion constraint on tuple \([0-9]+,[1-9][0-9]*\) in relation "t"$`)
		if !pattern.MatchString(got.Context) {
			t.Fatalf("exclusion physical tuple Context got %q", got.Context)
		}
		want.Context = got.Context
	}
	referenceAssertDiagnostic(t, err, &want)
}
