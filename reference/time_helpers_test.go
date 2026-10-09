package reference_test

import (
	"testing"
	"time"
)

// Independently expect pq's Jan1 anchor and Jan2 endpoint, rather than dropping
// dates from arbitrary time.Time cells or changing timestamp normalization.
func referenceTimeRows(t *testing.T, postgres bool, types []string, rows [][]any) [][]any {
	t.Helper()
	if !postgres {
		return rows
	}
	result := make([][]any, len(rows))
	for i, row := range rows {
		result[i] = append([]any(nil), row...)
		for j, typ := range types {
			if typ != "TIME" || row[j] == nil {
				continue
			}
			clock := row[j].(string)
			if clock == "24:00:00" {
				result[i][j] = time.Date(0, time.January, 2, 0, 0, 0, 0, time.UTC)
				continue
			}
			value, err := time.Parse("15:04:05.999999", clock)
			if err != nil {
				t.Fatal("invalid independent clock expectation", clock, err)
			}
			result[i][j] = value
		}
	}
	return result
}
