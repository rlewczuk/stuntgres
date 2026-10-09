package sqltype

import (
	"context"
	"errors"
	"stuntgres/internal/sqlstate"
	"testing"
)

func TestStringCoreProviderScalars(t *testing.T) {
	types := mathTypes()
	ctx := context.Background()
	text := types[Text].Usage()
	cases := []struct {
		id            ID
		input, output string
	}{
		{Bool, "true", "true"}, {Bytea, `\x41`, `\x41`}, {Char, `\377`, `\377`}, {Name, "name", "name"},
		{Int8, "9223372036854775807", "9223372036854775807"}, {Int2, "-2", "-2"}, {Int4, "42", "42"}, {Text, "a ", "a "}, {Oid, "42", "42"},
		{Float4, "1.5", "1.5"}, {Float8, "-2.25", "-2.25"}, {BPChar, "a  ", "a"}, {VarChar, "a ", "a "},
		{Date, "2001-02-03", "2001-02-03"}, {Time, "04:05:06", "04:05:06"}, {Timestamp, "2001-02-03 04:05:06", "2001-02-03 04:05:06"},
		{Timestamptz, "2001-02-03 04:05:06+00", "2001-02-03 04:05:06+00"}, {Interval, "1 day 02:03:04", "1 day 02:03:04"},
		{Timetz, "04:05:06+00", "04:05:06+00"}, {Numeric, "8.4100", "8.4100"}, {Regclass, "42", "seed"}, {Void, "", ""},
		{UUID, "00112233-4455-6677-8899-aabbccddeeff", "00112233-4455-6677-8899-aabbccddeeff"},
	}
	for _, tc := range cases {
		d := types[tc.id]
		var scalar Value
		var err error
		if tc.id == Void {
			scalar = VoidValue(d)
		} else {
			scalar, err = d.Input(ctx, tc.input, -1)
			if err != nil {
				t.Fatal(tc, err)
			}
		}
		for reverse := range 2 {
			args := []Usage{text, d.Usage()}
			values := [2]Value{String(types[Text], "|"), scalar}
			textual := [2]bool{true, StringTextInput(tc.id)}
			want := "|" + tc.output
			if reverse == 1 {
				args[0], args[1] = args[1], args[0]
				values[0], values[1] = values[1], values[0]
				textual[0], textual[1] = textual[1], textual[0]
				want = tc.output + "|"
			}
			plan, err := BindString("||", text, text, args, textual, false)
			if err != nil {
				t.Fatal(tc, err)
			}
			got, err := plan.Evaluate(ctx, values, InputEnvironment{}, OutputEnvironment{Regclass: stringRegclassFormatter{}})
			if err != nil {
				t.Fatal(tc, err)
			}
			s, err := got.Text()
			if err != nil || s != want || got.Type() != types[Text] {
				t.Fatal(tc, s, want, err)
			}
			values[reverse] = Null(args[reverse].Type)
			got, err = plan.Evaluate(ctx, values, InputEnvironment{}, OutputEnvironment{})
			if err != nil || !got.IsNull() || got.Type() != types[Text] {
				t.Fatal("typed strict NULL", tc, got, err)
			}
		}
	}
	ordinary, err := types[Bool].Output(ctx, Boolean(types[Bool], true))
	if err != nil || ordinary != "t" {
		t.Fatal("ordinary bool output changed", ordinary, err)
	}
	for _, tc := range []struct{ input, output string }{{"", ""}, {"A", "A"}, {`\000`, ""}, {`\200`, `\200`}, {`\377`, `\377`}} {
		v, err := types[Char].Input(ctx, tc.input, -1)
		if err != nil {
			t.Fatal(err)
		}
		got, err := types[Char].ConcatOutput(ctx, v, OutputEnvironment{})
		if err != nil || got != tc.output {
			t.Fatal(tc, got, err)
		}
	}
	if StringScalar(NameArray) || StringScalar(Unknown) || StringScalar(0) || StringCategory(Char) || !StringTextInput(Char) {
		t.Fatal("scalar/category boundary")
	}
}

func TestStringCoreProviderFunctions(t *testing.T) {
	types := mathTypes()
	text := types[Text].Usage()
	ctx := context.Background()
	for _, tc := range []struct {
		name, input, set, want string
		unary                  bool
	}{
		{"btrim", " x ", "", "x", true}, {"ltrim", " x ", "", "x ", true}, {"rtrim", " x ", "", " x", true},
		{"btrim", "🙂éxé🙂", "é🙂", "x", false}, {"btrim", "abxxba", "ab", "xx", false},
		{"btrim", "\t x\t ", "", "\t x\t", true}, {"btrim", " x ", "", " x ", false},
		{"normalize", "é", "NFC", "é", false}, {"normalize", "ﬃ", "NFKD", "ffi", false},
	} {
		args := []Usage{text, text}
		values := [2]Value{String(types[Text], tc.input), String(types[Text], tc.set)}
		if tc.unary {
			args = args[:1]
		}
		plan, err := BindString(tc.name, text, text, args, [2]bool{true, true}, false)
		if err != nil {
			t.Fatal(err)
		}
		got, err := plan.Evaluate(ctx, values, InputEnvironment{}, OutputEnvironment{})
		if err != nil {
			t.Fatal(tc, err)
		}
		s, _ := got.Text()
		if s != tc.want {
			t.Fatal(tc, s)
		}
	}
	plan, err := BindString("normalize", text, text, []Usage{text, text}, [2]bool{true, true}, false)
	if err != nil {
		t.Fatal(err)
	}
	_, err = plan.Evaluate(ctx, [2]Value{String(types[Text], "x"), String(types[Text], "NFC ")}, InputEnvironment{}, OutputEnvironment{})
	var diagnostic *sqlstate.Error
	if !errors.As(err, &diagnostic) || *diagnostic != (sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22023", Message: "invalid normalization form: NFC "}) {
		t.Fatal("full runtime form diagnostic", err)
	}
	got, err := plan.Evaluate(ctx, [2]Value{Null(types[Text]), String(types[Text], "bad")}, InputEnvironment{}, OutputEnvironment{})
	if err != nil || !got.IsNull() {
		t.Fatal("strict NULL bypasses form body", got, err)
	}
	for _, tc := range []struct {
		name   string
		result ID
		args   []Usage
		flags  [2]bool
	}{
		{"unknown", Text, []Usage{text}, [2]bool{true, true}},
		{"normalize", Bool, []Usage{text}, [2]bool{true, true}},
		{"is_normalized", Text, []Usage{text}, [2]bool{true, true}},
		{"btrim", Text, []Usage{types[Int4].Usage()}, [2]bool{true, true}},
		{"||", Text, []Usage{text, types[NameArray].Usage()}, [2]bool{true, false}},
	} {
		if _, err := BindString(tc.name, types[tc.result].Usage(), text, tc.args, tc.flags, false); err == nil {
			t.Fatal("accepted invalid signature", tc)
		}
	}
	c, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := plan.Evaluate(c, [2]Value{}, InputEnvironment{}, OutputEnvironment{}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	for _, name := range []string{"btrim", "ltrim", "rtrim"} {
		trim, err := BindString(name, text, text, []Usage{text}, [2]bool{true, true}, false)
		if err != nil {
			t.Fatal(err)
		}
		allocations := testing.AllocsPerRun(100, func() {
			_, err := trim.Evaluate(ctx, [2]Value{String(types[Text], " x ")}, InputEnvironment{}, OutputEnvironment{})
			if err != nil {
				panic(err)
			}
		})
		if allocations != 0 {
			t.Fatal("endpoint trim allocates", name, allocations)
		}
	}
}

// This unit checks the type-owned capability boundary, not a PostgreSQL oracle.
// Public/reference tests independently exercise real catalog/path presentation.
type stringRegclassFormatter struct{}

func (stringRegclassFormatter) FormatRegclass(id uint32) (string, error) {
	if id != 42 {
		return "", errors.New("unexpected regclass identity")
	}
	return "seed", nil
}

func TestStringCoreRegclassOutputRequiresSnapshot(t *testing.T) {
	types := mathTypes()
	value := String(types[Regclass], "42")
	if _, err := types[Regclass].ConcatOutput(context.Background(), value, OutputEnvironment{}); err == nil {
		t.Fatal("missing snapshot silently emitted an OID")
	}
	got, err := types[Regclass].ConcatOutput(context.Background(), value, OutputEnvironment{Regclass: stringRegclassFormatter{}})
	if err != nil || got != "seed" {
		t.Fatal(got, err)
	}
	canonical, err := value.Text()
	if err != nil || canonical != "42" {
		t.Fatal("output rewrote the canonical OID", canonical, err)
	}
}

func TestStringPlanSpecializedOutputStability(t *testing.T) {
	types := mathTypes()
	text := types[Text].Usage()
	for _, tc := range []struct {
		id     ID
		stable bool
	}{
		{Int2, false}, {Int4, false}, {Int8, false}, {Numeric, false}, {Float4, false}, {Float8, false}, {Bool, false}, {Bytea, false}, {Oid, false}, {UUID, false}, {Time, false}, {Timetz, false}, {Char, false}, {Name, false}, {Text, false}, {BPChar, false}, {VarChar, false}, {Void, false},
		{Date, true}, {Timestamp, true}, {Timestamptz, true}, {Interval, true}, {Regclass, true},
	} {
		plan, err := BindString("||", text, text, []Usage{types[tc.id].Usage(), text}, [2]bool{false, true}, false)
		if err != nil {
			t.Fatal(tc, err)
		}
		if plan.SessionDependent() != tc.stable {
			t.Fatal("wrong specialized output stability", tc, plan.SessionDependent())
		}
	}
	if !types[NameArray].OutputSessionDependent() {
		t.Fatal("array output declaration must remain stable")
	}
}
