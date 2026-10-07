package sqltype

import (
	"context"
	"errors"
	"stuntgres/internal/sqlstate"
	"testing"
)

func byteaDescriptor(t *testing.T) *Descriptor {
	t.Helper()
	for _, d := range Builtins() {
		if d.ID() == Bytea {
			return d
		}
	}
	t.Fatal("missing OID17")
	return nil
}
func TestByteaInput(t *testing.T) {
	d := byteaDescriptor(t)
	ctx := context.Background()
	if d.Name() != "bytea" || d.DatabaseName() != "BYTEA" || d.Usage().Typmod != -1 || d.Usage().Collation != (CollationRef{}) {
		t.Fatal(d, d.Usage())
	}
	if decl, err := d.Usage().Declaration(); err != nil || decl != "BYTEA" {
		t.Fatal(decl, err)
	}
	for _, tc := range []struct{ in, want string }{
		{"", ""}, {`\x`, ""}, {`\x00aAfF`, "\x00\xaa\xff"}, {"\\x \t00\nAA\rff ", "\x00\xaa\xff"},
		{`\000\177\200\377`, "\x00\x7f\x80\xff"}, {`\\`, "\\"}, {"'", "'"}, {"é", "\xc3\xa9"},
	} {
		v, err := d.Input(ctx, tc.in, -1)
		if err != nil {
			t.Fatal(tc.in, err)
		}
		got, err := v.Binary()
		if err != nil || got != tc.want || v.IsNull() {
			t.Fatalf("%q got %q %v", tc.in, got, err)
		}
	}
	for _, tc := range []struct{ in, code, msg string }{
		{`\x0`, "22023", "invalid hexadecimal data: odd number of digits"},
		{`\xGG`, "22023", `invalid hexadecimal digit: "G"`},
		{`\xé0`, "22023", `invalid hexadecimal digit: "é"`},
		{`\x0é`, "22023", `invalid hexadecimal digit: "é"`},
		{`\x界0`, "22023", `invalid hexadecimal digit: "界"`},
		{`\x0界`, "22023", `invalid hexadecimal digit: "界"`},
		{`\x😀0`, "22023", `invalid hexadecimal digit: "😀"`},
		{`\x0😀`, "22023", `invalid hexadecimal digit: "😀"`},
		{`\x0 0`, "22023", `invalid hexadecimal digit: " "`},
		{"\\x\f00", "22023", "invalid hexadecimal digit: \"\f\""},
		{"\\x\v00", "22023", "invalid hexadecimal digit: \"\v\""},
		{`\q`, "22P02", "invalid input syntax for type bytea"},
		{`\X41`, "22P02", "invalid input syntax for type bytea"},
		{`\400`, "22P02", "invalid input syntax for type bytea"},
		{`\08`, "22P02", "invalid input syntax for type bytea"},
		{`\00`, "22P02", "invalid input syntax for type bytea"},
		{`\`, "22P02", "invalid input syntax for type bytea"},
	} {
		_, err := d.Input(ctx, tc.in, -1)
		var e *sqlstate.Error
		if !errors.As(err, &e) || e.Code != tc.code || e.Message != tc.msg || e.Position != "" {
			t.Fatal(tc.in, err)
		}
	}
	for _, raw := range []string{"\x00", "\xff"} {
		if _, err := d.Input(ctx, raw, -1); err == nil {
			t.Fatal("text validation bypassed")
		}
		if v, err := Binary(d, raw); err != nil || v.text != raw {
			t.Fatal(v, err)
		}
	}
	if _, err := Binary(nil, ""); err == nil {
		t.Fatal("nil descriptor")
	}
	if _, err := (Value{}).Binary(); err == nil {
		t.Fatal("zero value")
	}
	if _, err := Null(d).Binary(); err == nil {
		t.Fatal("null accessor")
	}
}
func TestByteaOutputAndCompare(t *testing.T) {
	d := byteaDescriptor(t)
	ctx := context.Background()
	for _, tc := range []struct{ raw, hex, escape string }{
		{"", "\\x", ""},
		{"\x00 '\\~\x7f\x80\xff", `\x0020275c7e7f80ff`, "\\000 '\\\\~\\177\\200\\377"},
		{"\x1f \x7e\x7f", `\x1f207e7f`, `\037 ~\177`},
	} {
		v, _ := Binary(d, tc.raw)
		if got, err := d.Output(ctx, v); err != nil || got != tc.hex {
			t.Fatal(got, err)
		}
		for _, mode := range []ByteaOutput{ByteaHex, ByteaEscape} {
			want := tc.hex
			if mode == ByteaEscape {
				want = tc.escape
			}
			if got, err := d.OutputWithEnvironment(ctx, v, OutputEnvironment{ByteaOutput: mode}); err != nil || got != want {
				t.Fatalf("got %q want %q: %v", got, want, err)
			}
			if got, err := d.SQLLiteral(ctx, v); err != nil || got != "'"+tc.hex+"'" {
				t.Fatal(got, err)
			}
		}
	}
	if got, err := d.SQLLiteral(ctx, Null(d)); err != nil || got != "NULL" {
		t.Fatal(got, err)
	}
	if _, err := d.Output(ctx, Null(d)); err == nil {
		t.Fatal("null output")
	}
	v, _ := Binary(d, "")
	if _, err := d.OutputWithEnvironment(ctx, v, OutputEnvironment{ByteaOutput: 99}); err == nil {
		t.Fatal("invalid mode")
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := d.OutputWithEnvironment(canceled, v, OutputEnvironment{}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	cmp, err := BindComparison(d, d)
	if err != nil {
		t.Fatal(err)
	}
	corpus := []string{"", "\x00", "\x00\x00", "\x7f", "\x80", "\xff"}
	for i, a := range corpus {
		for j, b := range corpus {
			av, _ := Binary(d, a)
			bv, _ := Binary(d, b)
			got, err := cmp.Compare(ctx, av, bv, InputEnvironment{})
			want := 0
			if i < j {
				want = -1
			}
			if i > j {
				want = 1
			}
			if err != nil || got != want {
				t.Fatal(i, j, got, err)
			}
		}
	}
}
