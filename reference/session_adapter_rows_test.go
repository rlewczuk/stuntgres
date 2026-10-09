package reference_test

import (
	"context"
	"database/sql/driver"
	"io"
	"reflect"
	"testing"
)

// The original public adapter QueryContext/driver.Rows route, with its actual
// native codec assertions, is independent of the recorded database/sql companion.
func settingsDriverValue(t *testing.T, ctx context.Context, queryer driver.QueryerContext, query, column, sqlType string, want any) {
	t.Helper()
	rows, err := queryer.QueryContext(ctx, query, nil)
	if err != nil {
		t.Fatal(query, err)
	}
	defer rows.Close()
	if columns := rows.Columns(); !reflect.DeepEqual(columns, []string{column}) {
		t.Fatal(columns)
	}
	values := make([]driver.Value, 1)
	if err = rows.Next(values); err != nil {
		t.Fatal(err)
	}
	referenceAssertRawValue(t, false, sqlType, values[0])
	if !reflect.DeepEqual(values[0], want) {
		t.Fatal(query, values, want)
	}
	if err = rows.Next(values); err != io.EOF {
		t.Fatal("raw cursor EOF", err)
	}
	if err = rows.Close(); err != nil {
		t.Fatal("raw cursor close", err)
	}
}
