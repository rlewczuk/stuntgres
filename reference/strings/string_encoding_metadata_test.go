package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceStringEncodingMetadata(t *testing.T) {
	fixture.StringEncodingScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		q := "SELECT ascii($1),chr($2),to_ascii($1,$3),unistr($4),unicode_assigned($1)"
		names, types := []string{"ascii", "chr", "to_ascii", "unistr", "unicode_assigned"}, []string{"INT4", "TEXT", "TEXT", "TEXT", "BOOL"}
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		var retained [][]*sql.ColumnType
		for _, prepared := range []bool{false, true} {
			for _, tc := range []struct {
				args []any
				row  []any
			}{
				{[]any{"é", 128578, "LATIN1", `\D83D\DE42`}, []any{int64(233), "🙂", "AC", "🙂", true}},
				{[]any{nil, nil, nil, nil}, []any{nil, nil, nil, nil, nil}},
				{[]any{"A", 65, "LATIN1", `\0041`}, []any{int64(65), "A", "A", "A", true}},
			} {
				var rows *sql.Rows
				if prepared {
					rows, err = stmt.QueryContext(p.Ctx, tc.args...)
				} else {
					rows, err = c.QueryContext(p.Ctx, q, tc.args...)
				}
				if err != nil {
					t.Fatal(err)
				}
				ct, err := rows.ColumnTypes()
				if err != nil {
					rows.Close()
					t.Fatal(err)
				}
				fixture.StringEncodingColumns(t, ct, b.PG, names, types)
				fixture.LexicalRows(t, b.PG, rows, fixture.Select(q, names, types, [][]any{tc.row}), nil)
				fixture.StringEncodingColumns(t, ct, b.PG, names, types)
				retained = append(retained, ct)
			}
		}
		if err := stmt.Close(); err != nil {
			t.Fatal(err)
		}
		for _, ct := range retained {
			fixture.StringEncodingColumns(t, ct, b.PG, names, types)
		}
	})
}
