package sqltype

import (
	"context"
	"errors"
	"strings"
	"stuntgres/internal/sqlstate"
	"testing"
)

func TestJSONPathPayloadAndCanonicalization(t *testing.T) {
	types, fresh := testTypes(), testTypes()
	d := types[JSONPath]
	ctx := context.Background()
	if d.ID() != 4072 || !d.Concrete() || d.Name() != "jsonpath" || d.DatabaseName() != "JSONPATH" || d.AssociatedArray() != 4073 || types[4073].Element() != d || !types[4073].Concrete() || d.Usage().Typmod != -1 || d.Usage().Collation.ID != 0 || IsCharacter(JSONPath) {
		t.Fatal("JSONPath identity", d)
	}
	cases := []struct{ input, want string }{
		{`lax (($))`, `$`}, {`strict $.a[*][0].b`, `strict $."a"[*][0]."b"`},
		{`$a.b`, `$"a"."b"`}, {`$.**{2 to 2}`, `$.**{2}`}, {`$.**{last to 5}`, `$.**{last to 5}`},
		{`$[-1,0.5,$x + 1,last - 1,1 to last]`, `$[-1,0.5,$"x" + 1,last - 1,1 to last]`},
		{`$[$?(@ == last)]`, `$[$?(@ == last)]`},
		{`$.g ? (exists(@.a?(@ == 1)))`, `$."g"?(exists (@."a"?(@ == 1)))`},
		{`$?(@ < 1 || @ <= 2 || @ > 3 || @ >= 4)`, `$?(((@ < 1 || @ <= 2) || @ > 3) || @ >= 4)`},
		{`$?(!(@ == 1) && (@ == 2) is unknown)`, `$?(!(@ == 1) && (@ == 2) is unknown)`},
		{`1 * 2 + 4 % -3 != false`, `(1 * 2 + 4 % -3 != false)`},
		{`($ + 1) * 2 / 3 % 4 - 5`, `(((($ + 1) * 2) / 3) % 4 - 5)`},
		{`+ - + 1`, `-1`}, {`-($ + 1)`, `(-($ + 1))`}, {`1/0`, `(1 / 0)`},
		{`1.e1`, `10`}, {`0.0010e-1`, `0.00010`}, {`0b100101`, `37`}, {`0o273`, `187`}, {`0x42F`, `1071`},
		{`1_000_000`, `1000000`}, {`0x1EEE_FFFF`, `518979583`}, {`1.2.type()`, `(1.2).type()`},
		{`"\x50\u0067\u{53}\u{051}\u{00004C}"`, `"PgSQL"`}, {`"\z"`, `"z"`},
		{`"\ud83d\ude04\ud83d\udc36"`, `"😄🐶"`}, {"\"a\t\n\r\x01b\"", `"a\t\n\r\u0001b"`},
		{`$.decimal(+10,-2)`, `$.decimal(10,-2)`}, {`$.datetime("YYYY")`, `$.datetime("YYYY")`},
		{`$?(@ like_regex "pattern" flag "isim")`, `$?(@ like_regex "pattern" flag "ism")`},
		{`true`, `true`}, {`false`, `false`}, {`null`, `null`}, {`"a'b"`, `"a'b"`},
	}
	for _, tc := range cases {
		v, err := d.Input(ctx, tc.input, -1)
		if err != nil {
			t.Fatalf("%q: %v", tc.input, err)
		}
		out, err := d.Output(ctx, v)
		if err != nil || out != tc.want {
			t.Fatalf("%q -> %q want %q (%v)", tc.input, out, tc.want, err)
		}
		again, err := d.Input(ctx, out, -1)
		if err != nil || !SameDatum(v, again) {
			t.Fatal("canonical identity", tc.input, err)
		}
		literal, err := d.SQLLiteral(ctx, v)
		if err != nil || literal != "'"+strings.ReplaceAll(out, "'", "''")+"'" {
			t.Fatal(literal, err)
		}
		copy, err := v.Retag(fresh[JSONPath])
		if err != nil || copy.jsonpath != v.jsonpath {
			t.Fatal("retag copied parse", err)
		}
		if _, err := v.Text(); err == nil {
			t.Fatal("character accessor admitted")
		}
	}
	for _, method := range []string{"type", "size", "abs", "floor", "double", "ceiling", "keyvalue", "bigint", "boolean", "date", "integer", "number", "string", "decimal", "datetime", "time", "time_tz", "timestamp", "timestamp_tz"} {
		input := "$." + method + "()"
		v, err := d.Input(ctx, input, -1)
		if err != nil {
			t.Fatal(input, err)
		}
		if out, err := d.Output(ctx, v); err != nil || out != input {
			t.Fatal(out, err)
		}
	}
	for _, v := range []Value{{typ: d}, Null(d)} {
		if v.IsNull() {
			continue
		}
		if _, err := d.Output(ctx, v); err == nil {
			t.Fatal("missing parse output")
		}
		if _, err := v.Retag(fresh[JSONPath]); err == nil {
			t.Fatal("missing parse retag")
		}
	}
	if SameDatum(Value{typ: d}, Value{typ: d}) {
		t.Fatal("invalid datum identity")
	}
}
func TestJSONPathParsedStructureAndComposition(t *testing.T) {
	types := testTypes()
	d := types[JSONPath]
	ctx := context.Background()
	v, err := d.Input(ctx, `strict $a.x[1,2 to last]?(@ like_regex "(a)\\1" flag "mi").decimal(10,-2)`, -1)
	if err != nil {
		t.Fatal(err)
	}
	p := v.jsonpath
	n := p.root
	if !p.strict || n.kind != "variable" || n.text != "a" || n.next.kind != "key" || n.next.text != "x" {
		t.Fatal("mode/root/key structure")
	}
	index := n.next.next
	if index.kind != "index" || len(index.args) != 2 || index.args[0].to != nil || index.args[1].to.kind != "last" {
		t.Fatal("index/range structure")
	}
	filter := index.next
	if filter.kind != "filter" || filter.left.kind != "like_regex" || filter.left.text != `(a)\1` || filter.left.flags != 5 || filter.left.left.kind != "@" {
		t.Fatal("predicate/regex structure")
	}
	method := filter.next
	if method.kind != "method" || method.text != "decimal" || numericOutput(method.left.number) != "10" || numericOutput(method.right.number) != "-2" {
		t.Fatal("method arguments")
	}
	domain, err := Domain(90000, "jp_domain", "jp_domain", d.Usage())
	if err != nil {
		t.Fatal(err)
	}
	wrapped, err := domain.Wrap(v)
	if err != nil || wrapped.jsonpath != p {
		t.Fatal("domain ownership", err)
	}
	base, err := wrapped.BaseValue()
	if err != nil || base != v {
		t.Fatal("domain base", err)
	}
	array, err := ArrayValue(types[4073], []ArrayDimension{{Lower: 1, Length: 2}}, []Value{v, Null(d)})
	if err != nil {
		t.Fatal(err)
	}
	fresh := testTypes()
	retagged, err := array.Retag(fresh[4073])
	if err != nil || retagged.array.elements[0].jsonpath != p || retagged.array.elements[0].typ != fresh[JSONPath] {
		t.Fatal("array ownership", err)
	}
	if _, err := BindGrouping(d.Usage()); err == nil {
		t.Fatal("SQL grouping admitted")
	}
	if _, err := BindOrdering(d.Usage()); err == nil {
		t.Fatal("SQL ordering admitted")
	}
}
func TestJSONPathDiagnosticContract(t *testing.T) {
	cases := []struct{ input, code, message, detail string }{
		{"", "22P02", `invalid input syntax for type jsonpath: ""`, ""},
		{"last", "42601", "LAST is allowed only in array subscripts", ""},
		{"@ + 1", "42601", "@ is not allowed in root expressions", ""},
		{"1.type()", "42601", `trailing junk after numeric literal at or near "1.t" of jsonpath input`, ""},
		{"1a", "42601", `trailing junk after numeric literal at or near "1a" of jsonpath input`, ""},
		{"0b0x", "42601", "syntax error at end of jsonpath input", ""},
		{"1__0", "42601", "syntax error at end of jsonpath input", ""},
		{"$[", "42601", "syntax error at end of jsonpath input", ""},
		{"$[]", "42601", `syntax error at or near "]" of jsonpath input`, ""},
		{"$?(true)", "42601", `syntax error at or near ")" of jsonpath input`, ""},
		{`"\u000g"`, "42601", `invalid Unicode escape sequence at or near "\u000" of jsonpath input`, ""},
		{`"\x0g"`, "42601", `invalid hexadecimal character sequence at or near "\x0" of jsonpath input`, ""},
		{`"\u0000"`, "22P05", "unsupported Unicode escape sequence", `\u0000 cannot be converted to text.`},
		{`"\ud83d\ud83d"`, "22P02", "invalid input syntax for type jsonpath", "Unicode high surrogate must not follow a high surrogate."},
		{`"\ud83dX"`, "22P02", "invalid input syntax for type jsonpath", "Unicode low surrogate must follow a high surrogate."},
		{`"\u{110000}"`, "42601", "invalid Unicode code point", ""},
		{"1e131072", "22003", "value overflows numeric format", ""},
		{"1e-16384", "22003", "value overflows numeric format", ""},
		{"$.**{2147483648}", "22003", `value "2147483648" is out of range for type integer`, ""},
		{"$.decimal(1,2,3)", "42601", "invalid input syntax for type jsonpath", ".decimal() can only have an optional precision[,scale]."},
		{`@?(@ like_regex "(")`, "2201B", "invalid regular expression: parentheses () not balanced", ""},
		{`$?(@ like_regex "(" flag "xa")`, "42601", "invalid input syntax for type jsonpath", `Unrecognized flag character "a" in LIKE_REGEX predicate.`},
		{`$?(@ like_regex "(" flag "x")`, "0A000", `XQuery "x" flag (expanded regular expressions) is not implemented`, ""},
	}
	d := testTypes()[JSONPath]
	for _, tc := range cases {
		_, err := d.Input(context.Background(), tc.input, -1)
		e, ok := err.(*sqlstate.Error)
		if !ok || e.Code != tc.code || e.Message != tc.message || e.Detail != tc.detail || e.Position != "" || e.Context != "" {
			t.Fatalf("%q: %#v want %s/%s/%s", tc.input, err, tc.code, tc.message, tc.detail)
		}
	}
	for _, input := range []string{"$ trailing", "$.lower()", "$.time(-1)", "$.decimal(1.0)", "$?(last > 0)", "exists(true == false)", "true == false == null", "string"} {
		if _, err := d.Input(context.Background(), input, -1); err == nil {
			t.Fatal("invalid grammar", input)
		}
	}
}
func TestJSONPathCancellationAndDepth(t *testing.T) {
	d := testTypes()[JSONPath]
	ctx := context.Background()
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := d.Input(canceled, "$", -1); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	for _, input := range []string{`$` + strings.Repeat(`.longkey`, 10000), `"` + strings.Repeat("a", 10000) + `"`, strings.Repeat("/* comment */", 10000) + "$", `$?(@ like_regex "` + strings.Repeat("a", 10000) + `")`} {
		c := &groupingCancelContext{Context: ctx, at: 100}
		if _, err := d.Input(c, input, -1); !errors.Is(err, context.Canceled) {
			t.Fatal("in-progress input cancellation", err)
		}
	}
	depth := 10000
	v, err := d.Input(ctx, strings.Repeat("(", depth)+"$"+strings.Repeat(")", depth), -1)
	if err != nil {
		t.Fatal(err)
	}
	if out, err := d.Output(ctx, v); err != nil || out != "$" {
		t.Fatal(out, err)
	}
	if _, err := d.Output(canceled, v); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	// A long left-deep arithmetic tree exercises the iterative scope/printer.
	v, err = d.Input(ctx, "$"+strings.Repeat(" + 1", depth), -1)
	if err != nil {
		t.Fatal(err)
	}
	c := &groupingCancelContext{Context: ctx, at: 100}
	if _, err := printJSONPath(c, v.jsonpath); !errors.Is(err, context.Canceled) {
		t.Fatal("renderer cancellation", err)
	}
}
func TestJSONPathCoercionBoundaries(t *testing.T) {
	types := testTypes()
	d := types[JSONPath]
	ctx := context.Background()
	for _, id := range []ID{Text, VarChar, BPChar} {
		p, err := PlanCoercion(types[id].Usage(), d.Usage(), Explicit)
		if err != nil {
			t.Fatal(err)
		}
		v, err := p.Apply(ctx, String(types[id], "$.a"), InputEnvironment{}, OutputEnvironment{})
		if err != nil || v.text != `$."a"` {
			t.Fatal(v, err)
		}
		if _, err := PlanCoercion(types[id].Usage(), d.Usage(), Assignment); err == nil {
			t.Fatal("typed character assignment admitted")
		}
		p, err = PlanCoercion(d.Usage(), types[id].Usage(), Explicit)
		if err != nil {
			t.Fatal(err)
		}
		out, err := p.Apply(ctx, v, InputEnvironment{}, OutputEnvironment{})
		if err != nil || out.text != `$."a"` {
			t.Fatal(out, err)
		}
	}
	for _, id := range []ID{Int4, Bool, JSON, JSONB} {
		if _, err := PlanCoercion(types[id].Usage(), d.Usage(), Explicit); err == nil {
			t.Fatal("unsupported input cast", id)
		}
	}
	if _, err := PlanCoercion(d.Usage(), types[JSON].Usage(), Explicit); err == nil {
		t.Fatal("JSON output cast admitted")
	}
	if e, ok := d.CheckModifierSupport().(*sqlstate.Error); !ok || e.Code != "42601" || e.Message != `type modifier is not allowed for type "jsonpath"` {
		t.Fatal(e)
	}
	for _, id := range []ID{VarChar, BPChar} {
		u := types[id].Usage()
		u.Typmod = 6
		p, err := PlanCoercion(d.Usage(), u, Explicit)
		if err != nil {
			t.Fatal(err)
		}
		v, err := d.Input(ctx, "$.a", -1)
		if err != nil {
			t.Fatal(err)
		}
		out, err := p.Apply(ctx, v, InputEnvironment{}, OutputEnvironment{})
		if err != nil || out.text != "$." {
			t.Fatal("explicit character truncation", out, err)
		}
	}
}

func TestJSONPathSQLCapabilitiesRejected(t *testing.T) {
	types := testTypes()
	domain, err := Domain(90001, "jp_domain", "jp_domain", types[JSONPath].Usage())
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range []*Descriptor{types[JSONPath], types[4073], domain} {
		t.Run(d.Name(), func(t *testing.T) {
			comparison, err := BindComparison(d, d, CollationRef{}, false)
			if d.IsArray() {
				if err != nil {
					t.Fatal(err)
				}
				empty, err := d.Input(context.Background(), "{}", -1)
				if err != nil {
					t.Fatal(err)
				}
				_, err = comparison.EqualValues(context.Background(), empty, empty, InputEnvironment{})
				var missing *sqlstate.Error
				if !errors.As(err, &missing) || missing.Code != "42883" || missing.Message != "could not identify an equality operator for type jsonpath" {
					t.Fatal(err)
				}
			} else if err == nil {
				t.Fatal("scalar SQL comparison admitted")
			}
			_, err = BindOrdering(d.Usage())
			var diagnostic *sqlstate.Error
			if !errors.As(err, &diagnostic) || diagnostic.Code != "42883" || diagnostic.Message != "could not identify an ordering operator for type "+d.DiagnosticName() || diagnostic.Hint != "Use an explicit ordering operator or modify the query." || diagnostic.Position != "" {
				t.Fatalf("ordering diagnostic: %#v (%v)", diagnostic, err)
			}
			_, err = BindGrouping(d.Usage())
			diagnostic = nil
			if !errors.As(err, &diagnostic) || diagnostic.Code != "42883" || diagnostic.Message != "could not identify an equality operator for type "+d.DiagnosticName() || diagnostic.Hint != "" || diagnostic.Position != "" {
				t.Fatalf("grouping diagnostic: %#v (%v)", diagnostic, err)
			}
			if _, ok := DefaultIndexClass(d); ok {
				t.Fatal("default B-tree class admitted")
			}
			if class, ok := DefaultHashIndexClass(d); ok {
				// Generic array class identity is not an executable provider.
				if _, err := BindIndexHash(d.Usage(), class); err == nil {
					t.Fatal("hash provider admitted")
				}
			}
		})
	}
}
