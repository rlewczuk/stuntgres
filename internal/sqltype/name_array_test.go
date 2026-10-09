package sqltype

import (
	"context"
	"errors"
	"strings"
	"stuntgres/internal/sqlstate"
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
	if value, err := d.Input(context.Background(), "{public}", -1); err != nil {
		t.Fatal(err)
	} else if text, err := d.Output(context.Background(), value); err != nil || text != "{public}" {
		t.Fatal(text, err)
	}
	if _, err := Names(d, []string{"invalid\x00"}); err == nil {
		t.Fatal("invalid NAME element")
	}
}

func TestNameArrayInputCanonicalShapeAndDiagnostics(t *testing.T) {
	d := testTypes()[NameArray]
	ctx := context.Background()
	for _, tc := range []struct{ input, want string }{
		{"{}", "{}"},
		{`{NULL,"NULL","",alpha}`, `{NULL,"NULL","",alpha}`},
		{`{\NULL}`, `{"NULL"}`},
		{`{N\ULL}`, `{"NULL"}`},
		{`{NUL\L}`, `{"NULL"}`},
		{`{\N\U\L\L}`, `{"NULL"}`},
		{`{\null}`, `{"null"}`},
		{`{\NULL,NULL,"NULL",N\ULL,NUL\L}`, `{"NULL",NULL,"NULL","NULL","NULL"}`},
		{"{{}}", "{}"},
		{"{{},{}}", "{}"},
		{`{{a,b},{c,NULL}}`, `{{a,b},{c,NULL}}`},
		{`[0:1]={a,b}`, `[0:1]={a,b}`},
		{`[1:2]={a,b}`, `{a,b}`},
		{`{"a,b","a{b}","a b","a\"b","a\\b"}`, `{"a,b","a{b}","a b","a\"b","a\\b"}`},
		{`{ a , b }`, `{a,b}`},
	} {
		value, err := d.Input(ctx, tc.input, -1)
		if err != nil {
			t.Fatal(tc, err)
		}
		text, err := d.Output(ctx, value)
		if err != nil || text != tc.want {
			t.Fatal(tc, text, err)
		}
	}
	for _, tc := range []struct{ input, detail string }{
		{`{{a},{b,c}}`, "Multidimensional arrays must have sub-arrays with matching dimensions."},
		{`{a,,b}`, "Unexpected \",\" character."},
		{`[0:2]={a,b}`, "Specified array dimensions do not match array contents."},
		{`{"a}`, "Unexpected end of input."},
	} {
		_, err := d.Input(ctx, tc.input, -1)
		var diagnostic *sqlstate.Error
		if !errors.As(err, &diagnostic) || diagnostic.Code != "22P02" || diagnostic.Message != `malformed array literal: "`+tc.input+`"` || diagnostic.Detail != tc.detail {
			t.Fatal(tc, err)
		}
	}
	value, err := d.Input(ctx, "{"+strings.Repeat("é", 32)+"}", -1)
	if err != nil {
		t.Fatal(err)
	}
	text, err := d.Output(ctx, value)
	if err != nil || text != "{"+strings.Repeat("é", 31)+"}" {
		t.Fatal(text, err)
	}
}
