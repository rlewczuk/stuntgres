package reference_test

import (
	"fmt"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePgStatStatementsNormalizationLargeMultirow(t *testing.T) {
	pgssCase(t, true, func(s *pgssScenario) {
		// A bounded real INSERT workload, not a timing assertion or a collector
		// shortcut. Each execution supplies 512 independently selected literals.
		const rowsPerInsert = 256
		const table = "pgss_part.pgss_norm_large_multirow"
		s.exec("CREATE TABLE " + table + "(k integer,v text)")
		pattern := "INSERT%INTO " + table + "(k,v) VALUES%"
		s.counts(pattern, 0, 0, 0)
		s.both(fixture.Select("SELECT k,v FROM "+table+" ORDER BY k", []string{"k", "v"}, []string{"INT4", "TEXT"}, nil))

		var direct, prepared, normalized strings.Builder
		firstPrefix := "INSERT /* pgss large 雪 */ INTO " + table + "(k,v) VALUES\n"
		direct.WriteString(firstPrefix)
		prepared.WriteString("INSERT /* later spelling */ INTO " + table + "(k,v) VALUES\n")
		normalized.WriteString(firstPrefix)
		for row := range rowsPerInsert {
			if row > 0 {
				direct.WriteString(",\n")
				prepared.WriteString(",\n")
				normalized.WriteString(",\n")
			}
			fmt.Fprintf(&direct, "(%d,'雪-direct')", row+1)
			fmt.Fprintf(&prepared, "(%d,'été-prepared')", rowsPerInsert+row+1)
			// Source-order placeholders are specified from the two-column row
			// shape, never recovered from either backend's representative.
			fmt.Fprintf(&normalized, "($%d,$%d)", 2*row+1, 2*row+2)
		}

		// The complete ordered data contract is independent of generated SQL:
		// keys 1..256 carry the first text and keys 257..512 the second text.
		stored := make([][]any, 2*rowsPerInsert)
		for row := range stored {
			text := "雪-direct"
			if row >= rowsPerInsert {
				text = "été-prepared"
			}
			stored[row] = []any{int64(row + 1), text}
		}
		assertStored := func(rows [][]any) {
			s.both(fixture.Select("SELECT k,v FROM "+table+" ORDER BY k", []string{"k", "v"}, []string{"INT4", "TEXT"}, rows))
		}

		s.work(fixture.Exec(direct.String(), rowsPerInsert))
		assertStored(stored[:rowsPerInsert])
		s.counts(pattern, 1, 1, rowsPerInsert)
		s.entry(normalized.String(), 1, rowsPerInsert)

		s.work(fixture.Prepared(fixture.Exec(prepared.String(), rowsPerInsert)))
		assertStored(stored)
		s.counts(pattern, 1, 2, 2*rowsPerInsert)
		s.entry(normalized.String(), 2, 2*rowsPerInsert)
	})
}
