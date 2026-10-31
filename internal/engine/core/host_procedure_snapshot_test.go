package core_test

import (
	"context"
	"testing"

	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/sqltype"
)

func hostSnapshotRow(t *testing.T, e *core.Engine, query string) []sqltype.Value {
	t.Helper()
	result := enginetest.Run(t, e, query)
	if result.Rows == nil {
		t.Fatal("metadata query returned no row owner")
	}
	row := make([]sqltype.Value, len(result.Rows.Schema()))
	present, err := result.Rows.Next(context.Background(), row)
	if err != nil || !present {
		t.Fatal(query, present, err)
	}
	extra := make([]sqltype.Value, len(row))
	if present, err = result.Rows.Next(context.Background(), extra); err != nil || present {
		t.Fatal("extra metadata row", query, present, err)
	}
	return row
}
