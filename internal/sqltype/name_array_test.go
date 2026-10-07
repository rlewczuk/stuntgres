package sqltype

import (
	"context"
	"testing"
)

func TestNameArrayResultIdentityAndEncoding(t *testing.T) {
	var d *Descriptor
	for _, typ := range Builtins() {
		if typ.ID() == NameArray {
			d = typ
		}
	}
	if d == nil || d.ID() != 1003 || d.Name() != "_name" || d.SQLName() != "NAME[]" || d.DatabaseName() != "_NAME" {
		t.Fatal(d)
	}
	for _, tc := range []struct {
		names []string
		want  string
	}{
		{nil, "{}"},
		{[]string{"pg_catalog", "public"}, "{pg_catalog,public}"},
		{[]string{"", "NULL", "null", "a,b", "a{b}", "a b", "a\"b", "a\\b"}, `{"","NULL","null","a,b","a{b}","a b","a\"b","a\\b"}`},
	} {
		v, err := Names(d, tc.names)
		if err != nil {
			t.Fatal(err)
		}
		got, err := d.Output(context.Background(), v)
		if err != nil || got != tc.want {
			t.Fatal(got, tc.want, err)
		}
		if len(tc.names) > 0 {
			tc.names[0] = "changed"
			again, _ := v.NamesOutput()
			if again != tc.want {
				t.Fatal("mutable result", again)
			}
		}
	}
	if !Null(d).IsNull() || Null(d).Type() != d {
		t.Fatal("typed NULL")
	}
	if _, err := d.Input(context.Background(), "{public}", -1); err == nil {
		t.Fatal("result-only array admitted input")
	}
	if _, err := Names(d, []string{"invalid\x00"}); err == nil {
		t.Fatal("invalid NAME element")
	}
}
