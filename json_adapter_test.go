package stuntgres

import (
	"context"
	"database/sql/driver"
	"reflect"
	"stuntgres/internal/engine"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestJSONAdapterOwnedInputAndNulls(t *testing.T) {
	e := engine.New()
	d, ok := e.CatalogRead().LookupType(sqltype.JSON)
	if !ok {
		t.Fatal("missing JSON descriptor")
	}
	ctx := context.Background()
	bytes := []byte(` {"k":1,"k":2} `)
	normalized, err := normalizeArguments([]driver.NamedValue{{Ordinal: 1, Value: bytes}})
	if err != nil {
		t.Fatal(err)
	}
	bytes[0] = 'x'
	v, err := importParameter(ctx, normalized[0], d.Usage(), sqltype.InputEnvironment{})
	if err != nil {
		t.Fatal(err)
	}
	got, err := exportValue(ctx, v, sqltype.OutputEnvironment{})
	if err != nil || got != ` {"k":1,"k":2} ` || scanType(d) != reflect.TypeFor[string]() {
		t.Fatal(got, err, scanType(d))
	}
	for _, raw := range []driver.Value{nil, []byte(nil)} {
		v, err := importParameter(ctx, raw, d.Usage(), sqltype.InputEnvironment{})
		if err != nil || !v.IsNull() {
			t.Fatal(v, err)
		}
		got, err := exportValue(ctx, v, sqltype.OutputEnvironment{})
		if err != nil || got != nil {
			t.Fatal(got, err)
		}
	}
	for _, tc := range []struct {
		raw  driver.Value
		want string
	}{{"null", "null"}, {true, "true"}, {int64(42), "42"}, {1.25, "1.25"}} {
		v, err := importParameter(ctx, tc.raw, d.Usage(), sqltype.InputEnvironment{})
		if err != nil || v.IsNull() {
			t.Fatal(v, err)
		}
		got, err := exportValue(ctx, v, sqltype.OutputEnvironment{})
		if err != nil || got != tc.want {
			t.Fatal(got, err)
		}
	}
	empty, err := normalizeArguments([]driver.NamedValue{{Ordinal: 1, Value: []byte{}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := importParameter(ctx, empty[0], d.Usage(), sqltype.InputEnvironment{}); err == nil {
		t.Fatal("empty nonnil JSON bytes became SQL NULL")
	}
}
