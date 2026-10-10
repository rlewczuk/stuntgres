package reference_test

import (
	"context"
	"testing"
)

func TestReferenceSQLCursorRegtypeWarnings(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			var notices []referenceDiagnostic
			_, conn, recorder := noticeReferencePool(t, p, backend.pg, &notices)
			defer conn.ExecContext(context.Background(), "ROLLBACK")
			run := func(step referenceStep) {
				t.Helper()
				regtypeRunStep(t, p.ctx, conn, recorder, step)
			}
			quiet := func(step referenceStep) {
				t.Helper()
				notices = nil
				run(step)
				regtypeAssertNotices(t, notices, nil)
			}
			quiet(referenceExec("CREATE TABLE cursor_regtype_rows(t TEXT)", 0))
			// Identical values need no row-order promise. There is no blocking
			// sort or literal cast that could emit warnings before FETCH demand.
			quiet(referenceExec("INSERT INTO cursor_regtype_rows VALUES('timestamp(7)'),('timestamp(7)'),('timestamp(7)'),('timestamp(7)')", 4))
			quiet(referenceExec("BEGIN", 0))
			quiet(referenceExec("SET LOCAL client_min_messages TO warning", 0))
			quiet(referenceSelect("DECLARE c NO SCROLL CURSOR FOR SELECT t::REGTYPE AS r FROM cursor_regtype_rows", []string{}, []string{}, [][]any{}))
			const query = "FETCH 1 FROM c"
			held, err := conn.PrepareContext(p.ctx, query)
			if err != nil {
				t.Fatal("quiet FETCH prepare", err)
			}
			defer held.Close()
			regtypeAssertNotices(t, notices, nil)
			warning := referenceDiagnostic{
				Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: "22023",
				Message: "TIMESTAMP(7) precision reduced to maximum allowed, 6",
			}
			for i, policy := range []string{"warning", "error", "notice", "warning"} {
				quiet(referenceExec("SET LOCAL client_min_messages TO "+policy, 0))
				notices = nil
				step := referenceSelect(query, []string{"r"}, []string{"REGTYPE"}, [][]any{{"timestamp without time zone"}})
				if i%2 == 0 {
					regtypeRunStep(t, p.ctx, referenceHeldStatement{referenceExecutor: conn, statement: held, query: query}, recorder, step)
				} else {
					run(step)
				}
				var want []referenceDiagnostic
				if policy != "error" {
					want = []referenceDiagnostic{warning}
				}
				// Full records include empty Position/Context and every other
				// logical diagnostic field; no planning/parameter warning is used.
				regtypeAssertNotices(t, notices, want)
			}
			quiet(referenceSelect("FETCH ALL FROM c", []string{"r"}, []string{"REGTYPE"}, [][]any{}))
			quiet(referenceSelect("FETCH ALL FROM c", []string{"r"}, []string{"REGTYPE"}, [][]any{}))
			quiet(referenceExec("CLOSE c", 0))
			quiet(referenceExec("COMMIT", 0))
		})
	}
}
