package reference_test

import (
	"context"
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceSQLCursorOutputCapture(t *testing.T) {
	sqlCursorScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		sqlCursorSetup(t, p, b, c)
		sqlCursorStep(t, p, b, c, fixture.Exec("BEGIN", 0))
		sqlCursorStep(t, p, b, c, fixture.Exec("DECLARE output_c NO SCROLL CURSOR FOR SELECT 'cursor_rows'::REGCLASS AS r,'integer'::REGTYPE AS typ FROM cursor_rows ORDER BY i", 0))
		query := "FETCH 1 FROM output_c"
		step := fixture.Select(query, []string{"r", "typ"}, []string{"REGCLASS", "REGTYPE"}, [][]any{{"cursor_rows", "integer"}})
		// REGTYPE and REGCLASS share their already-tested public text codec family.
		step.RawTypes = []string{"REGCLASS", "REGCLASS"}
		ctx := context.WithValue(p.Ctx, fixture.RecorderKey{}, b.Recorder)
		rows, err := c.QueryContext(ctx, query)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		frozen := fixture.SequenceFrozenRows{Executor: c, Rows: rows, Query: query}
		if b.PG {
			sqlCursorStep(t, p, b, frozen, step)
		}
		sqlCursorStep(t, p, b, c, fixture.Exec("SET LOCAL search_path=pg_catalog", 0))
		sqlCursorStep(t, p, b, c, fixture.Exec("CLOSE output_c", 0))
		if !b.PG {
			sqlCursorStep(t, p, b, frozen, step)
		}
		sqlCursorError(t, p, b, c, "FETCH ALL FROM output_c", sqlCursorMissing("output_c"))
	})
}
