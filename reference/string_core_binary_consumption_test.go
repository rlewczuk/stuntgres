package reference_test

import "testing"

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
func stringCoreReviewBinaryConsumptionReference(t *testing.T, p *referencePair) {
	run := func(t *testing.T, s referenceStep) {
		t.Helper()
		p.step(t, s)
	}
	run(t, referenceExec("CREATE TABLE t(v BYTEA,s TEXT)", 0))
	run(t, referenceExec(`INSERT INTO t VALUES('\x41',' x ')`, 1))
	for _, tc := range stringCoreBinaryConsumptionCases() {
		for _, prepared := range []bool{false, true} {
			s := stringCoreSelect(tc)
			if prepared {
				s = referencePrepared(s)
			}
			run(t, s)
		}
	}
}

func TestReferenceStringCoreBinaryConsumptionSourceConsumerRoutes(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			stringCoreControlExec(t, backend.db, "CREATE TABLE t(v BYTEA,s TEXT)")
			stringCoreControlExec(t, backend.db, `INSERT INTO t VALUES('\x41',' x ')`)
			for _, tc := range stringCoreBinaryConsumptionCases() {
				step := stringCoreSelect(tc)
				step.nonContext = true
				referenceRunStep(t, p.ctx, backend.db, backend.recorder, step)
				referenceRunStep(t, p.ctx, backend.db, backend.recorder, referencePrepared(step))
			}
		})
	}
}
