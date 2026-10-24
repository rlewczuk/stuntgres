package stuntgres

import (
	"context"
	"database/sql/driver"
	"reflect"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/routine"
	"stuntgres/internal/sqltype"
)

func TestRangeTypeAdapter(t *testing.T) {
	e := engine.New()
	view := e.CatalogRead()
	subtype, _ := view.LookupType(sqltype.Int4)
	boolean, _ := view.LookupType(sqltype.Bool)
	class, ok, err := view.DefaultIndexClass(403, subtype)
	if err != nil || !ok {
		t.Fatal("missing actual class", err)
	}
	class, err = routine.BindClassSupport(view, class)
	if err != nil {
		t.Fatal(err)
	}
	ordering, err := routine.BindIndexOrdering(view, subtype.Usage(), class)
	if err != nil {
		t.Fatal(err)
	}
	r, m, err := sqltype.NewRangeFamily(900001, "adapter_span", "public.adapter_span", 900002, "adapter_spans", "public.adapter_spans", sqltype.RangeDefinition{Subtype: subtype.Usage(), Class: class, Ordering: ordering}, boolean)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for _, tc := range []struct {
		d     *sqltype.Descriptor
		input string
	}{{r, "[1,3]"}, {m, "{[1,3]}"}} {
		if scanType(tc.d) != reflect.TypeFor[string]() {
			t.Fatal("custom ScanType", scanType(tc.d))
		}
		bytes := []byte(tc.input)
		args, err := normalizeArguments([]driver.NamedValue{{Ordinal: 1, Value: bytes}})
		if err != nil {
			t.Fatal(err)
		}
		for i := range bytes {
			bytes[i] = 'x'
		}
		value, err := importParameter(ctx, args[0], tc.d.Usage(), sqltype.InputEnvironment{})
		if err != nil {
			t.Fatal(err)
		}
		got, err := exportValue(ctx, value, sqltype.OutputEnvironment{})
		if err != nil || got != tc.input {
			t.Fatal("owned adapter payload", got, err)
		}
		for _, null := range []any{nil, []byte(nil)} {
			value, err := importParameter(ctx, null, tc.d.Usage(), sqltype.InputEnvironment{})
			if err != nil || !value.IsNull() {
				t.Fatal("typed null import", value, err)
			}
			got, err := exportValue(ctx, value, sqltype.OutputEnvironment{})
			if err != nil || got != nil {
				t.Fatal("null export", got, err)
			}
		}
	}
}
