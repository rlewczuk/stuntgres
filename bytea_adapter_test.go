package stuntgres

import (
	"context"
	"database/sql/driver"
	"errors"
	"reflect"
	"stuntgres/internal/engine"
	"stuntgres/internal/sqltype"
	"testing"
)

type byteaValuer struct {
	value driver.Value
	err   error
	calls *int
}

func (v byteaValuer) Value() (driver.Value, error) { (*v.calls)++; return v.value, v.err }
func TestByteaAdapterOwnership(t *testing.T) {
	ctx := context.Background()
	e := engine.New()
	d, ok := e.CatalogRead().LookupType(sqltype.Bytea)
	if !ok {
		t.Fatal("OID17")
	}
	text, _ := e.CatalogRead().LookupType(sqltype.Text)
	src := []byte{0, 128, 255}
	normalized, err := normalizeArguments([]driver.NamedValue{{Ordinal: 1, Value: src}})
	if err != nil {
		t.Fatal(err)
	}
	src[0] = 99
	v, err := importParameter(ctx, normalized[0], d.Usage(), sqltype.InputEnvironment{})
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		exported, err := exportValue(ctx, v, sqltype.OutputEnvironment{})
		if err != nil || !reflect.DeepEqual(exported, []byte{0, 128, 255}) {
			t.Fatal(exported, err)
		}
		exported.([]byte)[0] = 77
	}
	for _, tc := range []struct {
		raw  any
		want any
	}{
		{nil, nil}, {[]byte(nil), nil}, {[]byte{}, []byte{}}, {"", []byte{}}, {`\x41`, []byte{92, 120, 52, 49}},
	} {
		args, err := normalizeArguments([]driver.NamedValue{{Ordinal: 1, Value: tc.raw}})
		if err != nil {
			t.Fatal(err)
		}
		v, err := importParameter(ctx, args[0], d.Usage(), sqltype.InputEnvironment{})
		if err != nil {
			t.Fatal(err)
		}
		got, err := exportValue(ctx, v, sqltype.OutputEnvironment{})
		if err != nil || !reflect.DeepEqual(got, tc.want) {
			t.Fatal(tc.raw, got, tc.want, err)
		}
	}
	args, _ := normalizeArguments([]driver.NamedValue{{Ordinal: 1, Value: []byte(nil)}})
	tv, err := importParameter(ctx, args[0], text.Usage(), sqltype.InputEnvironment{})
	if err != nil || tv.IsNull() {
		t.Fatal(tv, err)
	}
	if raw, err := tv.Text(); err != nil || raw != "" {
		t.Fatal(raw, err)
	}
	if scanType(d) != reflect.TypeFor[[]byte]() {
		t.Fatal(scanType(d))
	}
	for _, raw := range []driver.Value{`\000`, []byte{0, 255}} {
		calls := 0
		args, err := normalizeArguments([]driver.NamedValue{{Ordinal: 1, Value: byteaValuer{raw, nil, &calls}}})
		if err != nil || calls != 1 {
			t.Fatal(calls, err)
		}
		v, err := importParameter(ctx, args[0], d.Usage(), sqltype.InputEnvironment{})
		if err != nil {
			t.Fatal(err)
		}
		got, err := exportValue(ctx, v, sqltype.OutputEnvironment{})
		if err != nil {
			t.Fatal(err)
		}
		want := []byte{0, 255}
		if _, ok := raw.(string); ok {
			want = []byte{92, 48, 48, 48}
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatal(got, want)
		}
	}
	calls := 0
	sentinel := errors.New("valuer")
	_, err = normalizeArguments([]driver.NamedValue{{Ordinal: 1, Value: byteaValuer{nil, sentinel, &calls}}})
	if !errors.Is(err, sentinel) || calls != 1 {
		t.Fatal(err, calls)
	}
}
