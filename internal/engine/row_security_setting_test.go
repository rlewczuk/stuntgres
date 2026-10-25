package engine

import (
	"testing"

	"stuntgres/internal/sqltype"
)

func settingsMetadataRow(t *testing.T, rows [][]sqltype.Value, name string) []sqltype.Value {
	t.Helper()
	for _, row := range rows {
		if got, _ := row[0].Text(); got == name {
			return row
		}
	}
	t.Fatalf("missing setting %s", name)
	return nil
}
