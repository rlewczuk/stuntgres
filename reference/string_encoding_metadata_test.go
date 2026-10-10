package reference_test

import (
	"database/sql"
	"testing"
)

func TestReferenceStringEncodingMetadata(t *testing.T) {
	stringEncodingScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		q := "SELECT ascii($1),chr($2),to_ascii($1,$3),unistr($4),unicode_assigned($1)"
		names, types := []string{"ascii", "chr", "to_ascii", "unistr", "unicode_assigned"}, []string{"INT4", "TEXT", "TEXT", "TEXT", "BOOL"}
		stmt, err := c.PrepareContext(p.ctx, q)
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
					rows, err = stmt.QueryContext(p.ctx, tc.args...)
				} else {
					rows, err = c.QueryContext(p.ctx, q, tc.args...)
				}
				if err != nil {
					t.Fatal(err)
				}
				ct, err := rows.ColumnTypes()
				if err != nil {
					rows.Close()
					t.Fatal(err)
				}
				stringEncodingColumns(t, ct, b.pg, names, types)
				lexicalRows(t, b.pg, rows, referenceSelect(q, names, types, [][]any{tc.row}), nil)
				stringEncodingColumns(t, ct, b.pg, names, types)
				retained = append(retained, ct)
			}
		}
		if err := stmt.Close(); err != nil {
			t.Fatal(err)
		}
		for _, ct := range retained {
			stringEncodingColumns(t, ct, b.pg, names, types)
		}
	})
}
