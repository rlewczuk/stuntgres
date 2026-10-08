package sqltype

import (
	"context"
	"testing"
)

func TestMathScalarRejectedSignatures(t *testing.T) {
	types := mathTypes()
	for _, tc := range []struct {
		name   string
		result ID
		args   []ID
	}{
		{"abs", Numeric, []ID{Numeric, Numeric}}, {"ceil", Float8, []ID{Float4}}, {"ceiling", Int4, []ID{Int4}}, {"floor", Numeric, []ID{Numeric, Int4}}, {"exp", Numeric, []ID{Int4}},
		{"div", Float8, []ID{Float8, Float8}}, {"div", Numeric, []ID{Numeric}}, {"factorial", Numeric, []ID{Numeric}}, {"factorial", Int8, []ID{Int8}},
	} {
		var args []Usage
		for _, id := range tc.args {
			args = append(args, types[id].Usage())
		}
		if _, err := BindMath(tc.name, types[tc.result].Usage(), args...); err == nil {
			t.Fatal("accepted invalid exact signature", tc)
		}
	}
	for _, name := range []string{"abs", "ceil", "ceiling", "floor", "exp", "div", "factorial"} {
		id, result := Numeric, Numeric
		if name == "factorial" {
			id = Int8
		}
		u := types[id].Usage()
		u.Typmod = 123
		args := []Usage{u}
		if name == "div" {
			args = append(args, types[id].Usage())
		}
		if _, err := BindMath(name, types[result].Usage(), args...); err == nil {
			t.Fatal("accepted constrained argument", name)
		}
	}
}

func TestMathScalarExactMagnitudeBoundaries(t *testing.T) {
	for _, tc := range []struct {
		id          ID
		input, want string
	}{{Int2, "32767", "32767"}, {Int4, "2147483647", "2147483647"}, {Int8, "9223372036854775807", "9223372036854775807"}, {Numeric, "-9007199254740993.00100", "9007199254740993.00100"}} {
		p, v := scalarProvider(t, "abs", tc.id, tc.input)
		got, err := p.Evaluate(context.Background(), v[0], v[1])
		if err != nil {
			t.Fatal(err)
		}
		text, err := p.ResultUsage().Type.Output(context.Background(), got)
		if err != nil || text != tc.want {
			t.Fatal(tc, text, err)
		}
	}
	p, v := scalarProvider(t, "exp", Numeric, "5999.9999999999999999999999")
	_, err := p.Evaluate(context.Background(), v[0], v[1])
	scalarError(t, err, "22003", "value overflows numeric format")
}
