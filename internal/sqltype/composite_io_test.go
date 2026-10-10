package sqltype

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"stuntgres/internal/sqlstate"
)

func TestCompositeScannerCanonicalMatrix(t *testing.T) {
	b := compositeBuiltins()
	tt := compositeForTest(t, 50000, CompositeMember{1, "a", b[Text].Usage()}, CompositeMember{2, "b", b[Text].Usage()})
	cases := []struct {
		input, output string
		firstNull     bool
		first         string
	}{
		{`(,)`, `(,)`, true, ""}, {`("",)`, `("",)`, false, ""},
		{`(NULL,"NULL")`, `(NULL,NULL)`, false, "NULL"},
		{`( ,)`, `(" ",)`, false, " "}, {" \t(a,b)\r\n", "(a,b)", false, "a"},
		{`("a,b","c(d)")`, `("a,b","c(d)")`, false, "a,b"},
		{`("a""b",c)`, `("a""b",c)`, false, `a"b`},
		{`(a\,b,c)`, `("a,b",c)`, false, "a,b"},
		{`(a\(b,c)`, `("a(b",c)`, false, "a(b"},
		{`(a(b,c)`, `("a(b",c)`, false, "a(b"},
		{`(a" b "c,d)`, `("a b c",d)`, false, "a b c"},
		{`("a\\b",c)`, `("a\\b",c)`, false, `a\b`},
		{`(a\)b,c)`, `("a)b",c)`, false, "a)b"},
		{`("""",x)`, `("""",x)`, false, `"`},
		{"(\v\f\r\n\t ,x)", "(\"\v\f\r\n\t \",x)", false, "\v\f\r\n\t "},
		{"(\u00a0,x)", "(\u00a0,x)", false, "\u00a0"},
	}
	for _, test := range cases {
		t.Run(test.input, func(t *testing.T) {
			v := compositeInputForTest(t, tt, test.input)
			field := v.CompositeField(0)
			if field.Type() != b[Text] || field.IsNull() != test.firstNull || !field.IsNull() && field.text != test.first {
				t.Fatalf("field = %#v", field)
			}
			if got := compositeOutputForTest(t, v); got != test.output {
				t.Fatalf("output %q, want %q", got, test.output)
			}
			roundtrip := compositeInputForTest(t, tt, test.output)
			if !SameDatum(v, roundtrip) {
				t.Fatal("typed IO not inverse")
			}
		})
	}
	it := compositeForTest(t, 50100, CompositeMember{1, "i", b[Int4].Usage()}, CompositeMember{2, "t", b[Text].Usage()})
	if got := compositeOutputForTest(t, compositeInputForTest(t, it, "( +0007 ,)")); got != "(7,)" {
		t.Fatal(got)
	}
	nested := compositeForTest(t, 50200, CompositeMember{1, "p", it.Usage()}, CompositeMember{2, "t", b[Text].Usage()})
	for _, text := range []string{`("(1,foo)",bar)`, `("(,)",)`, `(,)`} {
		v := compositeInputForTest(t, nested, text)
		if compositeOutputForTest(t, v) != text || !SameDatum(v, compositeInputForTest(t, nested, text)) {
			t.Fatal("nested IO", text)
		}
	}
	allNull := compositeInputForTest(t, nested, `("(,)",)`)
	null := compositeInputForTest(t, nested, `(,)`)
	if allNull.CompositeField(0).IsNull() || !null.CompositeField(0).IsNull() || SameDatum(allNull, null) {
		t.Fatal("nested all-NULL collapsed")
	}
}

func TestCompositeScannerDiagnostics(t *testing.T) {
	b := compositeBuiltins()
	d := compositeForTest(t, 50000, CompositeMember{1, "i", b[Int4].Usage()}, CompositeMember{2, "t", b[Text].Usage()})
	cases := []struct{ input, detail string }{
		{"", "Missing left parenthesis."}, {"[]", "Missing left parenthesis."}, {"1,x)", "Missing left parenthesis."}, {"\u00a0(1,x)", "Missing left parenthesis."},
		{"(1)", "Too few columns."}, {"()", "Too few columns."},
		{"(1,x,y)", "Too many columns."}, {"(1,,)", "Too many columns."}, {`(1,"",)`, "Too many columns."},
		{"(1,x", "Unexpected end of input."}, {`(1,"x)`, "Unexpected end of input."}, {`(1,x\`, "Unexpected end of input."}, {"(", "Unexpected end of input."},
		{"(1,x) junk", "Junk after right parenthesis."}, {"(1,x),", "Junk after right parenthesis."},
	}
	for _, test := range cases {
		_, err := d.Input(context.Background(), test.input, -1)
		want := &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22P02", Message: `malformed record literal: "` + test.input + `"`, Detail: test.detail}
		var got *sqlstate.Error
		if !errors.As(err, &got) || !reflect.DeepEqual(got, want) {
			t.Fatalf("input %q: %#v, want %#v", test.input, err, want)
		}
	}
	for _, text := range []string{"oops", "2147483648", "NULL", "", " "} {
		input := "(" + text + ",x,y)"
		if text == "" {
			input = `("",x,y)`
		}
		_, expected := b[Int4].Input(context.Background(), text, -1)
		_, actual := d.Input(context.Background(), input, -1)
		if !reflect.DeepEqual(actual, expected) {
			t.Fatalf("child-before-arity %q: %#v vs %#v", input, actual, expected)
		}
	}
	varchar := b[VarChar].Usage()
	varchar.Typmod = 6
	short := compositeForTest(t, 50100, CompositeMember{1, "v", varchar})
	_, expected := b[VarChar].Input(context.Background(), "abcd", 6)
	_, actual := short.Input(context.Background(), "(abcd,extra)", -1)
	if !reflect.DeepEqual(actual, expected) {
		t.Fatal("child typmod error replaced", actual, expected)
	}
	nested := compositeForTest(t, 50200, CompositeMember{1, "inner", d.Usage()})
	_, expected = d.Input(context.Background(), "(1)", -1)
	_, actual = nested.Input(context.Background(), `("(1)")`, -1)
	if !reflect.DeepEqual(actual, expected) {
		t.Fatal("nested error rewrapped", actual, expected)
	}
	empty := compositeForTest(t, 50300)
	for _, text := range []string{"(x)", "(,)", "("} {
		_, err := empty.Input(context.Background(), text, -1)
		var got *sqlstate.Error
		want := &sqlstate.Error{
			Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22P02",
			Message: `malformed record literal: "` + text + `"`, Detail: "Too many columns.",
		}
		if !errors.As(err, &got) || !reflect.DeepEqual(got, want) {
			t.Fatalf("zero-field input %q: %#v, want %#v", text, err, want)
		}
	}
}

func TestCompositeChildCodecsAndEnvironment(t *testing.T) {
	b := compositeBuiltins()
	array, err := Array(50000, "ints", b[Int4])
	if err != nil {
		t.Fatal(err)
	}
	enum, err := Enum(50100, "choice", "choice", []string{"a,b", "x"})
	if err != nil {
		t.Fatal(err)
	}
	domain, err := Domain(50200, "plain", "plain", b[Text].Usage())
	if err != nil {
		t.Fatal(err)
	}
	char := b[BPChar].Usage()
	char.Typmod = 7
	numeric, _, err := b[Numeric].ResolveModifiers(4, 1)
	if err != nil {
		t.Fatal(err)
	}
	d := compositeForTest(t, 50300, CompositeMember{1, "array", array.Usage()}, CompositeMember{2, "enum", enum.Usage()}, CompositeMember{3, "domain", domain.Usage()}, CompositeMember{4, "bool", b[Bool].Usage()}, CompositeMember{5, "char", char}, CompositeMember{6, "num", numeric})
	v := compositeInputForTest(t, d, `("{1,NULL,2}","a,b",plain,true,x,12.34)`)
	want := `("{1,NULL,2}","a,b",plain,t,"x  ",12.3)`
	if compositeOutputForTest(t, v) != want || !SameDatum(v, compositeInputForTest(t, d, want)) || v.CompositeField(2).Type() != domain {
		t.Fatal("delegated child codecs")
	}
	bytes := compositeForTest(t, 50400, CompositeMember{1, "b", b[Bytea].Usage()})
	raw, err := Binary(b[Bytea], "A\\\x01")
	if err != nil {
		t.Fatal(err)
	}
	v, err = CompositeValue(bytes, []Value{raw})
	if err != nil {
		t.Fatal(err)
	}
	text, err := bytes.OutputWithEnvironment(context.Background(), v, OutputEnvironment{ByteaOutput: ByteaEscape})
	if err != nil || text != `("A\\\\\\001")` {
		t.Fatal("captured bytea escape output", text, err)
	}
	nested := compositeForTest(t, 50500, CompositeMember{1, "inner", bytes.Usage()})
	outer, err := CompositeValue(nested, []Value{v})
	if err != nil {
		t.Fatal(err)
	}
	out, err := nested.OutputWithEnvironment(context.Background(), outer, OutputEnvironment{ByteaOutput: ByteaEscape})
	if err != nil {
		t.Fatal(err)
	}
	roundtrip := compositeInputForTest(t, nested, out)
	if !SameDatum(roundtrip, outer) {
		t.Fatal("nested environment was not delegated", out)
	}
}

func TestCompositeCancellationAndMalformedBoundaries(t *testing.T) {
	b := compositeBuiltins()
	d := compositeForTest(t, 50000, CompositeMember{1, "t", b[Text].Usage()})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := d.Input(ctx, "(x)", -1); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	v := compositeInputForTest(t, d, "(x)")
	if _, err := d.Output(ctx, v); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := d.SQLLiteral(ctx, v); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	// The escaped scan used to be particularly susceptible to checks at offsets
	// skipped by two-byte escapes. Exercise a real cancellation and join it.
	large := "(" + strings.Repeat(`\x`, 1<<22) + ")"
	ctx, cancel = context.WithCancel(context.Background())
	started, done := make(chan struct{}), make(chan error, 1)
	go func() { close(started); _, err := d.Input(ctx, large, -1); done <- err }()
	<-started
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("record scan ignored cancellation")
	}
	for _, text := range []string{"(", `(\`, `("`, `("\`, `(x"`, `(x\`, "(x)", "()", `(a"b"c)`, `("a""b")`} {
		v, err := d.Input(context.Background(), text, -1)
		if err == nil {
			out := compositeOutputForTest(t, v)
			if !SameDatum(v, compositeInputForTest(t, d, out)) {
				t.Fatal("boundary roundtrip", text)
			}
		}
	}
}

func FuzzCompositeScanner(f *testing.F) {
	for _, text := range []string{"()", `("",)`, `(a" b "c,d)`, `(a\,b,c)`, `("a""b",c)`, `(x\`, `(,)`, "(x,y) junk"} {
		f.Add(text)
	}
	b := compositeBuiltins()
	d, err := Composite(50000, "fuzz", "fuzz", 50001, []CompositeMember{{1, "a", b[Text].Usage()}, {2, "b", b[Text].Usage()}})
	if err != nil {
		f.Fatal(err)
	}
	f.Fuzz(func(t *testing.T, text string) {
		v, err := d.Input(context.Background(), text, -1)
		if err != nil {
			return
		}
		out := compositeOutputForTest(t, v)
		if !SameDatum(v, compositeInputForTest(t, d, out)) {
			t.Fatal("record codec lost typed fields")
		}
	})
}

func TestCompositeEnumDiagnosticEnvironment(t *testing.T) {
	enum, err := Enum(50000, "mood", "public.mood", []string{"ok"})
	if err != nil {
		t.Fatal(err)
	}
	enumDomain, err := Domain(50100, "mood_domain", "public.mood_domain", enum.Usage())
	if err != nil {
		t.Fatal(err)
	}
	record := compositeForTest(t, 50200, CompositeMember{1, "m", enum.Usage()})
	nested := compositeForTest(t, 50300, CompositeMember{1, "r", record.Usage()})
	recordDomain, err := Domain(50400, "record_domain", "public.record_domain", nested.Usage())
	if err != nil {
		t.Fatal(err)
	}
	domainChild := compositeForTest(t, 50500, CompositeMember{1, "m", enumDomain.Usage()})
	array, err := Array(50600, "mood_domains", enumDomain)
	if err != nil {
		t.Fatal(err)
	}
	arrayChild := compositeForTest(t, 50700, CompositeMember{1, "a", array.Usage()})
	for _, test := range []struct {
		name string
		typ  *Descriptor
		bad  string
	}{
		{"enum", enum, "nope"},
		{"member", record, "(nope)"},
		{"nested", nested, `("(nope)")`},
		{"record domain", recordDomain, `("(nope)")`},
		{"enum domain member", domainChild, "(nope)"},
		{"array domain member", arrayChild, `("{nope}")`},
	} {
		t.Run(test.name, func(t *testing.T) {
			for _, spelling := range []string{"mood", "hidden.mood", `"Quoted schema"."Mood"`} {
				env := InputEnvironment{TypeDiagnosticName: func(id ID) string {
					if id == enum.ID() {
						return spelling
					}
					return ""
				}}
				_, err := test.typ.InputWithEnvironment(context.Background(), test.bad, -1, env)
				want := &sqlstate.Error{
					Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22P02",
					Message: `invalid input value for enum ` + spelling + `: "nope"`,
				}
				var got *sqlstate.Error
				if !errors.As(err, &got) || !reflect.DeepEqual(got, want) {
					t.Fatalf("recursive enum diagnostic: %#v, want %#v", err, want)
				}
			}
			_, err := test.typ.Input(context.Background(), test.bad, -1)
			want := &sqlstate.Error{
				Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22P02",
				Message: `invalid input value for enum public.mood: "nope"`,
			}
			var got *sqlstate.Error
			if !errors.As(err, &got) || !reflect.DeepEqual(got, want) {
				t.Fatalf("descriptor diagnostic fallback: %#v, want %#v", err, want)
			}
			emptyEnvironment := InputEnvironment{TypeDiagnosticName: func(ID) string { return "" }}
			_, err = test.typ.InputWithEnvironment(context.Background(), test.bad, -1, emptyEnvironment)
			if !errors.As(err, &got) || !reflect.DeepEqual(got, want) {
				t.Fatalf("empty captured spelling fallback: %#v, want %#v", err, want)
			}
		})
	}
	if enum.SQLName() != "public.mood" || enum.DiagnosticName() != "public.mood" {
		t.Fatal("diagnostic environment mutated enum descriptor")
	}
}
