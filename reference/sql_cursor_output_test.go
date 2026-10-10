package reference_test

import (
	"context"
	"database/sql"
	"testing"
)

func TestReferenceSQLCursorOutputCapture(t *testing.T) {
	sqlCursorScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		sqlCursorSetup(t, p, b, c)
		sqlCursorStep(t, p, b, c, referenceExec("BEGIN", 0))
		sqlCursorStep(t, p, b, c, referenceExec("DECLARE output_c NO SCROLL CURSOR FOR SELECT 'cursor_rows'::REGCLASS AS r,'integer'::REGTYPE AS typ FROM cursor_rows ORDER BY i", 0))
		query := "FETCH 1 FROM output_c"
		step := referenceSelect(query, []string{"r", "typ"}, []string{"REGCLASS", "REGTYPE"}, [][]any{{"cursor_rows", "integer"}})
		// REGTYPE and REGCLASS share their already-tested public text codec family.
		step.rawTypes = []string{"REGCLASS", "REGCLASS"}
		ctx := context.WithValue(p.ctx, referenceRecorderKey{}, b.recorder)
		rows, err := c.QueryContext(ctx, query)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		frozen := sequenceFrozenRows{referenceExecutor: c, rows: rows, query: query}
		if b.pg {
			sqlCursorStep(t, p, b, frozen, step)
		}
		sqlCursorStep(t, p, b, c, referenceExec("SET LOCAL search_path=pg_catalog", 0))
		sqlCursorStep(t, p, b, c, referenceExec("CLOSE output_c", 0))
		if !b.pg {
			sqlCursorStep(t, p, b, frozen, step)
		}
		sqlCursorError(t, p, b, c, "FETCH ALL FROM output_c", sqlCursorMissing("output_c"))
	})
}
