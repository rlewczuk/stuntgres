package sqltype

import (
	"context"
	"errors"
	"strings"
	"stuntgres/internal/sqlstate"
	"testing"
)

func TestJSONRetainedTextAndIdentity(t *testing.T) {
	types := testTypes()
	d := types[JSON]
	if d.ID() != 114 || d.Name() != "json" || d.SQLName() != "JSON" || d.DatabaseName() != "JSON" || d.AssociatedArray() != 199 || types[199].Element() != d || types[199].DatabaseName() != "_JSON" || d.Usage().Collation.ID != 0 || IsCharacter(JSON) {
		t.Fatal("incoherent JSON descriptor", d, types[199])
	}
	fresh := testTypes()
	if fresh[JSON] == d || fresh[199].Element() != fresh[JSON] {
		t.Fatal("shared descriptor generation")
	}
	ctx := context.Background()
	documents := []string{"null", "true", "false", "-0", "1.230E+004", " \t\n{\"z\":1.230e-5,\"a\":1,\"a\":2}\r\n ", `"apostrophe's\\path"`, `"\u0000"`, `"\uD800"`, `"\uDC00"`, `"\uD800\uD800"`, `"\uD800\u0041"`, `"\uaBcD"`, `"\uD800\uDC00"`, `"é\/\b\f\n\r\t\""`, `[{"a":[]},{}]`, strings.Repeat("9", 10000) + "e" + strings.Repeat("9", 10000), strings.Repeat("[", 10000) + "null" + strings.Repeat("]", 10000)}
	for _, text := range documents {
		v, err := d.Input(ctx, text, -1)
		if err != nil {
			t.Fatal(text, err)
		}
		got, err := v.JSONText()
		if err != nil || got != text || v.IsNull() {
			t.Fatal(got, err)
		}
		got, err = d.Output(ctx, v)
		if err != nil || got != text {
			t.Fatal(got, err)
		}
		got, err = d.SQLLiteral(ctx, v)
		if err != nil || got != "'"+strings.ReplaceAll(text, "'", "''")+"'" {
			t.Fatal(got, err)
		}
		if _, err := v.Text(); err == nil {
			t.Fatal("JSON became character data")
		}
		again, _ := d.Input(ctx, text, -1)
		if !SameDatum(v, again) {
			t.Fatal("representation identity lost")
		}
	}
	a, _ := d.Input(ctx, "{}", -1)
	b, _ := d.Input(ctx, " {}", -1)
	if SameDatum(a, b) || SameDatum(a, Null(d)) || !SameDatum(Null(d), Null(d)) {
		t.Fatal("raw identity/null conflated")
	}
	if _, err := Null(d).JSONText(); err == nil {
		t.Fatal("NULL accessor admitted")
	}
	if _, err := String(types[Text], "null").JSONText(); err == nil {
		t.Fatal("TEXT accessor admitted")
	}
	if _, err := BindComparison(d, d); err == nil {
		t.Fatal("JSON equality admitted")
	}
	if _, err := BindOrdering(d.Usage()); err == nil {
		t.Fatal("JSON ordering admitted")
	}
	if _, err := BindGrouping(d.Usage()); err == nil {
		t.Fatal("JSON grouping admitted")
	}
	if err := d.CheckModifierSupport(); err == nil {
		t.Fatal("JSON typmod admitted")
	}
}

func TestJSONSyntaxDiagnostics(t *testing.T) {
	cases := []struct{ text, detail, excerpt string }{
		{"", "The input string ended unexpectedly.", ""},
		{" \t\r\n ", "The input string ended unexpectedly.", " "},
		{"+1", `Token "+" is invalid.`, "+..."},
		{"01", `Token "01" is invalid.`, "01"},
		{"1f2", `Token "1f2" is invalid.`, "1f2"},
		{"0.x1", `Token "0.x1" is invalid.`, "0.x1"},
		{"1.3ex100", `Token "1.3ex100" is invalid.`, "1.3ex100"},
		{"1e", `Token "1e" is invalid.`, "1e"},
		{"tru", `Token "tru" is invalid.`, "tru"},
		{`"abc`, `Token ""abc" is invalid.`, `"abc`},
		{"true false", `Expected end of input, but found "false".`, "true false"},
		{"[,]", `Expected JSON value, but found ",".`, "[,..."},
		{"[1,2,]", `Expected JSON value, but found "]".`, "[1,2,]"},
		{"[1 2]", `Expected "," or "]", but found "2".`, "[1 2..."},
		{`{1:"abc"}`, `Expected string or "}", but found "1".`, "{1..."},
		{`{"a" 1}`, `Expected ":", but found "1".`, `{"a" 1...`},
		{`{"a":1,}`, `Expected string, but found "}".`, `{"a":1,}`},
		{`{"a":1 "b":2}`, `Expected "," or "}", but found ""b"".`, `{"a":1 "b"...`},
		{`"\v"`, `Escape sequence "\v" is invalid.`, `"\v...`},
		{`"\u"`, `"\u" must be followed by four hexadecimal digits.`, `"\u"`},
		{`"\u00"`, `"\u" must be followed by four hexadecimal digits.`, `"\u00"`},
		{`"\u000g"`, `"\u" must be followed by four hexadecimal digits.`, `"\u000g...`},
		{"\"a\tb\"", "Character with value 0x09 must be escaped.", `"a...`},
		{"\"a\nb\"", "Character with value 0x0a must be escaped.", `"a`},
		{"\"a\rb\"", "Character with value 0x0d must be escaped.", `"a`},
	}
	d := testTypes()[JSON]
	for _, tc := range cases {
		_, err := d.Input(context.Background(), tc.text, -1)
		var got *sqlstate.Error
		line := "1"
		if tc.text == " \t\r\n " {
			line = "2"
		}
		want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22P02", Message: "invalid input syntax for type json", Detail: tc.detail, Context: "JSON data, line " + line + ": " + tc.excerpt}
		if !errors.As(err, &got) || *got != want {
			t.Fatalf("%q: got %+v (%v) want %+v", tc.text, got, err, want)
		}
	}
	for _, text := range []string{"\xff", "\x00"} {
		_, err := d.Input(context.Background(), text, -1)
		var got *sqlstate.Error
		if !errors.As(err, &got) || got.Code != "22021" || got.Context != "" {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := d.Input(ctx, "null", -1); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestJSONExplicitTextCoercion(t *testing.T) {
	types := testTypes()
	ctx := context.Background()
	d := types[JSON]
	text := ` {"z":1,"z":2} `
	toJSON, err := PlanCoercion(types[Text].Usage(), d.Usage(), Explicit)
	if err != nil {
		t.Fatal(err)
	}
	v, err := toJSON.Apply(ctx, String(types[Text], text), InputEnvironment{}, OutputEnvironment{})
	if err != nil {
		t.Fatal(err)
	}
	toText, err := PlanCoercion(d.Usage(), types[Text].Usage(), Explicit)
	if err != nil {
		t.Fatal(err)
	}
	out, err := toText.Apply(ctx, v, InputEnvironment{}, OutputEnvironment{})
	got, _ := out.Text()
	if err != nil || got != text {
		t.Fatal(got, err)
	}
	if _, err := toJSON.Apply(ctx, String(types[Text], "bad"), InputEnvironment{}, OutputEnvironment{}); err == nil {
		t.Fatal("cast bypassed validation")
	}
	for _, id := range []ID{Text, Int4, Bool} {
		if _, err := PlanCoercion(types[id].Usage(), d.Usage(), Assignment); err == nil {
			t.Fatal("implicit JSON conversion", id)
		}
		if id != Text {
			if _, err := PlanCoercion(types[id].Usage(), d.Usage(), Explicit); err == nil {
				t.Fatal("JSON constructor admitted", id)
			}
		}
	}
}
