package stuntgres

import (
	"context"
	"database/sql/driver"
	"reflect"
	"stuntgres/internal/engine"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestJSONPathAdapterOwnedInputNullAndMetadata(t *testing.T) {
	e := engine.New()
	d, ok := e.CatalogRead().LookupType(sqltype.JSONPath)
	if !ok {
		t.Fatal("missing descriptor")
	}
	ctx := context.Background()
	bytes := []byte(`$.a.v`)
	args, err := normalizeArguments([]driver.NamedValue{{Ordinal: 1, Value: bytes}})
	if err != nil {
		t.Fatal(err)
	}
	bytes[0] = 'x'
	v, err := importParameter(ctx, args[0], d.Usage(), sqltype.InputEnvironment{})
	if err != nil {
		t.Fatal(err)
	}
	got, err := exportValue(ctx, v, sqltype.OutputEnvironment{})
	if err != nil || got != `$."a"."v"` || scanType(d) != reflect.TypeFor[string]() || d.DatabaseName() != "JSONPATH" {
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
		t.Fatal("empty bytes became NULL")
	}
}
func TestJSONPathAdapterSharedPortalDiagnostics(t *testing.T) {
	e := engine.New()
	d, _ := e.CatalogRead().LookupType(sqltype.JSONPath)
	for _, tc := range []struct {
		raw           driver.Value
		code, context string
	}{
		{"last", "42601", "unnamed portal parameter $1 = '...'"},
		{`$?(@ like_regex "(")`, "2201B", "unnamed portal parameter $1 = '...'"},
		{"", "22P02", "unnamed portal parameter $1 = ''"},
		{"\xff", "22021", "unnamed portal parameter $1"},
	} {
		importer := frameImporter([]driver.Value{tc.raw})
		err := importer(context.Background(), engine.ParameterInfo{Types: []sqltype.Usage{d.Usage()}}, make([]sqltype.Value, 1), sqltype.InputEnvironment{})
		diagnostic, ok := err.(*Error)
		if !ok || diagnostic.Code != tc.code || diagnostic.Context != tc.context || diagnostic.Position != "" {
			t.Fatal(err)
		}
	}
}
