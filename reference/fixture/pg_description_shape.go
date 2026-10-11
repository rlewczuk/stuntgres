package fixture

import (
	"database/sql"
	"strconv"
	"testing"
)

// The expectation is numeric, independent of either driver's OID encoding.
// Preserve the fixture's raw codec and middleware assertions before comparing.
func PgDescriptionRows(t *testing.T, p *Pair, backend Backend, s Step) [][]any {
	t.Helper()
	return PgDescriptionExecutorRows(t, p, backend, backend.DB, s)
}

func PgDescriptionExecutorRows(t *testing.T, p *Pair, backend Backend, executor Executor, s Step) [][]any {
	t.Helper()
	want := make([][]any, len(s.Rows))
	for i, row := range s.Rows {
		want[i] = append([]any(nil), row...)
		for j, v := range row {
			if s.Types[j] == "OID" && v != nil && backend.PG {
				want[i][j] = strconv.FormatInt(v.(int64), 10)
			}
		}
	}
	s.Rows = want
	s.CheckScan = func(t *testing.T, rows *sql.Rows, index int) {
		cells, dest := make([]any, len(s.Types)), make([]any, len(s.Types))
		for i := range dest {
			dest[i] = &cells[i]
		}
		if err := rows.Scan(dest...); err != nil {
			t.Fatal(err)
		}
		for i, v := range cells {
			OidReferenceValue(t, backend.PG, s.Types[i], v)
		}
	}
	observed := RunStep(t, p.Ctx, executor, backend.Recorder, s)
	for _, row := range observed.Rows {
		for i, v := range row {
			if backend.PG && s.Types[i] == "OID" && v != nil {
				n, err := strconv.ParseUint(v.(string), 10, 32)
				if err != nil {
					t.Fatal(err)
				}
				row[i] = int64(n)
			}
		}
	}
	return observed.Rows
}
