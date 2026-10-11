package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func stringCoreBinaryConsumptionCases() []stringCoreReferenceCase {
	return []stringCoreReferenceCase{
		{"SELECT btrim(NULL,NULL) AS v", nil, "TEXT"},
		{"SELECT ltrim(' x ') AS v", "x ", "TEXT"},
		{"SELECT rtrim(s) AS v FROM t", " x", "TEXT"},
		{"SELECT v||s AS v FROM t", `\x41 x `, "TEXT"},
		{"SELECT s||v AS v FROM t", ` x \x41`, "TEXT"},
		{"SELECT 'a'||'b' AS v FROM t", "ab", "TEXT"},
	}
}

// Only inherited exclusions made obsolete by this string-family cutover are
// migrated here. Existing binary positive and unrelated exclusion suites remain.
func stringCoreReviewBinaryConsumptionReference(t *testing.T, p *fixture.Pair) {
	run := func(t *testing.T, s fixture.Step) {
		t.Helper()
		p.Step(t, s)
	}
	run(t, fixture.Exec("CREATE TABLE t(v BYTEA,s TEXT)", 0))
	run(t, fixture.Exec(`INSERT INTO t VALUES('\x41',' x ')`, 1))
	for _, tc := range stringCoreBinaryConsumptionCases() {
		for _, prepared := range []bool{false, true} {
			s := stringCoreSelect(tc)
			if prepared {
				s = fixture.Prepared(s)
			}
			run(t, s)
		}
	}
}

func TestReferenceStringCoreBinaryConsumptionSourceConsumerRoutes(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			fixture.StringCoreControlExec(t, backend.DB, "CREATE TABLE t(v BYTEA,s TEXT)")
			fixture.StringCoreControlExec(t, backend.DB, `INSERT INTO t VALUES('\x41',' x ')`)
			for _, tc := range stringCoreBinaryConsumptionCases() {
				step := stringCoreSelect(tc)
				step.NonContext = true
				fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, step)
				fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Prepared(step))
			}
		})
	}
}
