package reference_test

import (
	"context"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceSQLCursorRegtypeWarnings(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			var notices []fixture.Diagnostic
			_, conn, recorder := fixture.NoticeReferencePool(t, p, backend.PG, &notices)
			defer conn.ExecContext(context.Background(), "ROLLBACK")
			run := func(step fixture.Step) {
				t.Helper()
				fixture.RegtypeRunStep(t, p.Ctx, conn, recorder, step)
			}
			quiet := func(step fixture.Step) {
				t.Helper()
				notices = nil
				run(step)
				fixture.RegtypeAssertNotices(t, notices, nil)
			}
			quiet(fixture.Exec("CREATE TABLE cursor_regtype_rows(t TEXT)", 0))
			// Identical values need no row-order promise. There is no blocking
			// sort or literal cast that could emit warnings before FETCH demand.
			quiet(fixture.Exec("INSERT INTO cursor_regtype_rows VALUES('timestamp(7)'),('timestamp(7)'),('timestamp(7)'),('timestamp(7)')", 4))
			quiet(fixture.Exec("BEGIN", 0))
			quiet(fixture.Exec("SET LOCAL client_min_messages TO warning", 0))
			quiet(fixture.Select("DECLARE c NO SCROLL CURSOR FOR SELECT t::REGTYPE AS r FROM cursor_regtype_rows", []string{}, []string{}, [][]any{}))
			const query = "FETCH 1 FROM c"
			held, err := conn.PrepareContext(p.Ctx, query)
			if err != nil {
				t.Fatal("quiet FETCH prepare", err)
			}
			defer held.Close()
			fixture.RegtypeAssertNotices(t, notices, nil)
			warning := fixture.Diagnostic{
				Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: "22023",
				Message: "TIMESTAMP(7) precision reduced to maximum allowed, 6",
			}
			for i, policy := range []string{"warning", "error", "notice", "warning"} {
				quiet(fixture.Exec("SET LOCAL client_min_messages TO "+policy, 0))
				notices = nil
				step := fixture.Select(query, []string{"r"}, []string{"REGTYPE"}, [][]any{{"timestamp without time zone"}})
				if i%2 == 0 {
					fixture.RegtypeRunStep(t, p.Ctx, fixture.HeldStatement{Executor: conn, Statement: held, SQL: query}, recorder, step)
				} else {
					run(step)
				}
				var want []fixture.Diagnostic
				if policy != "error" {
					want = []fixture.Diagnostic{warning}
				}
				// Full records include empty Position/Context and every other
				// logical diagnostic field; no planning/parameter warning is used.
				fixture.RegtypeAssertNotices(t, notices, want)
			}
			quiet(fixture.Select("FETCH ALL FROM c", []string{"r"}, []string{"REGTYPE"}, [][]any{}))
			quiet(fixture.Select("FETCH ALL FROM c", []string{"r"}, []string{"REGTYPE"}, [][]any{}))
			quiet(fixture.Exec("CLOSE c", 0))
			quiet(fixture.Exec("COMMIT", 0))
		})
	}
}
