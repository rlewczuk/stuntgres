package stuntgres

import (
	"database/sql/driver"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// Adapter-only descriptor/normalization coverage; public SQL witnesses remain
// in reference/extension_types, including empty and NULL-only result metadata.
func TestIntbigGkeyAdapter(t *testing.T) {
	typ, err := sqltype.IntbigGkey(65000, "intbig_gkey", "public.intbig_gkey")
	if err != nil {
		t.Fatal(err)
	}
	array, err := sqltype.Array(65001, "_intbig_gkey", typ)
	if err != nil {
		t.Fatal(err)
	}
	for _, descriptor := range []*sqltype.Descriptor{typ, array} {
		if got := scanType(descriptor); got != reflect.TypeFor[string]() {
			t.Fatalf("family metadata %s got=%v want=string", descriptor.Name(), got)
		}
		rawValues := []driver.Value{nil, []byte(nil)}
		for _, raw := range rawValues {
			value, err := importParameter(t.Context(), raw, descriptor.Usage(), sqltype.InputEnvironment{})
			if err != nil || value.Type() != descriptor || !value.IsNull() {
				t.Fatal("shared NULL import", descriptor.Name(), value, err)
			}
			if result, err := exportValue(t.Context(), value, sqltype.OutputEnvironment{}); err != nil || result != nil {
				t.Fatal("shared NULL export", descriptor.Name(), result, err)
			}
		}
	}
	for _, raw := range []any{"x", []byte("x"), "", []byte{}} {
		arguments, err := normalizeArguments([]driver.NamedValue{{Ordinal: 1, Value: raw}})
		if err != nil {
			t.Fatal(err)
		}
		value, err := importParameter(t.Context(), arguments[0], typ.Usage(), sqltype.InputEnvironment{})
		var diagnostic *sqlstate.Error
		want := &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "0A000", Message: "cannot accept a value of type intbig_gkey"}
		if value.Type() != nil || !errors.As(err, &diagnostic) || !reflect.DeepEqual(diagnostic, want) {
			t.Fatal("adapter bypassed genuine rejecting input", value, diagnostic, err)
		}
	}
	for _, text := range []string{"{}", "{NULL}"} {
		value, err := importParameter(t.Context(), text, array.Usage(), sqltype.InputEnvironment{})
		if err != nil {
			t.Fatal(err)
		}
		if result, err := exportValue(t.Context(), value, sqltype.OutputEnvironment{}); err != nil || result != text {
			t.Fatal("shared support array transport", result, err)
		}
	}
}
