package sqltype

import (
	"context"
	"errors"
	"runtime"
	"strings"
	"stuntgres/internal/sqlstate"
	"testing"
)

func substrUnit(t *testing.T, want any, raw ...any) {
	t.Helper()
	p, a := regexpUnitPlan(t, "regexp_substr", raw...)
	v, err := p.Evaluate(context.Background(), a, &PGRegexExecution{})
	if err != nil {
		t.Fatal(err)
	}
	if v.Type().ID() != Text {
		t.Fatal("not typed TEXT", v.Type())
	}
	if want == nil {
		if !v.IsNull() {
			t.Fatal("expected NULL", v)
		}
		return
	}
	if v.IsNull() || v.text != want.(string) {
		t.Fatal(v, "want", want)
	}
}
func TestRegexpSubstrSelection(t *testing.T) {
	for _, c := range []struct {
		args []any
		want any
	}{
		{[]any{"foobar", "o(.)b"}, "oob"}, {[]any{"a1b22c333", "[0-9]+", 1, 2}, "22"},
		{[]any{"ababa", "aba", 1, 2}, nil}, {[]any{"é🙂x", ".", 2}, "🙂"},
		{[]any{"abc", "^", 2}, nil}, {[]any{"abc", "(?<=a)b", 2}, "b"}, {[]any{"abc", `\mb`, 2}, nil},
		{[]any{"é🙂x", "", 4}, ""}, {[]any{"é🙂x", "", 5}, nil}, {[]any{"aaa", "a*", 1, 2}, ""},
	} {
		substrUnit(t, c.want, c.args...)
	}
}
func TestRegexpSubstrFlags(t *testing.T) {
	for _, c := range []struct {
		s, p, f string
		want    any
	}{
		{"ABCDEF", "c(.)(..)", "i", "CDEF"}, {"A", "a", "ic", nil}, {"A", "a", "ci", "A"},
		{"A", "(?c)a", "i", nil}, {"a.b", "a.b", "q", "a.b"}, {"axb", "a.b", "q", nil},
		{"a b", "a b", "x", nil}, {"a b", "a b", "xt", "a b"}, {"a\nb", "^b", "n", "b"},
		{"a\nb", "^b", "p", nil}, {"a\nb", "a.b", "w", "a\nb"}, {"aaab", "a+b", "e", nil},
	} {
		substrUnit(t, c.want, c.s, c.p, 1, 1, c.f)
	}
}
func TestRegexpSubstrCaptures(t *testing.T) {
	for i, w := range []any{"12345678", "123", "45678", "56", "78", nil} {
		substrUnit(t, w, "1234567890", "(123)(4(56)(78))", 1, 1, "", i)
	}
	substrUnit(t, nil, "b ab", "(a)?b", 1, 1, "", 1)
	substrUnit(t, "a", "b ab", "(a)?b", 1, 2, "", 1)
	substrUnit(t, "", "b", "(a*)b", 1, 1, "", 1)
	substrUnit(t, "b", "abc", "b", 1, 1, "", 1)
	substrUnit(t, nil, "abc", "b", 1, 1, "", 2)
	substrUnit(t, nil, "aba", "(a(b)?)+", 1, 1, "", 2)
}
func TestRegexpSubstrNullAndReset(t *testing.T) {
	p, a := regexpUnitPlan(t, "regexp_substr", "abc", "(b)", 1, 1, "", 1)
	state := PGRegexExecution{}
	if _, err := p.Evaluate(context.Background(), a, &state); err != nil {
		t.Fatal(err)
	}
	for i := range 6 {
		b := a
		b[i] = Null(a[i].Type())
		v, err := p.Evaluate(context.Background(), b, &state)
		if err != nil || !v.IsNull() || v.Type().ID() != Text || state.program != nil {
			t.Fatal(i, v, err, state.program)
		}
	}
	v, err := p.Evaluate(context.Background(), a, &state)
	if err != nil || v.text != "b" {
		t.Fatal(v, err)
	}
	state.Reset()
	if state.program != nil {
		t.Fatal("reset retained program")
	}
}
func TestRegexpSubstrDiagnostics(t *testing.T) {
	for _, c := range []struct {
		args      []any
		code, msg string
	}{
		{[]any{"a", "[", 0, 0, "z", -1}, "22023", `invalid value for parameter "start": 0`},
		{[]any{"a", "[", 1, 0, "z", -1}, "22023", `invalid value for parameter "n": 0`},
		{[]any{"a", "[", 1, 1, "z", -1}, "22023", `invalid value for parameter "subexpr": -1`},
		{[]any{"a", "[", 1, 1, "gz", 0}, "22023", `invalid regular expression option: "z"`},
		{[]any{"a", "[", 1, 1, "g", 0}, "22023", `regexp_substr() does not support the "global" option`},
		{[]any{"a", "[", 2147483647, 2147483647, "", 2147483647}, "2201B", `invalid regular expression: brackets [] not balanced`},
	} {
		p, a := regexpUnitPlan(t, "regexp_substr", c.args...)
		state := PGRegexExecution{}
		_, err := p.Evaluate(context.Background(), a, &state)
		var d *sqlstate.Error
		if !errors.As(err, &d) || d.Code != c.code || d.Message != c.msg || d.Hint != "" || d.Position != "" || state.program != nil {
			t.Fatal(err, state.program)
		}
	}
}
func TestRegexpSubstrDemandAndOwnership(t *testing.T) {
	p, a := regexpUnitPlan(t, "regexp_substr", "é🙂x", "(🙂)", 2, 1, "", 0)
	state := PGRegexExecution{}
	first, err := p.Evaluate(context.Background(), a, &state)
	if err != nil || first.text != "🙂" {
		t.Fatal(first, err)
	}
	match, err := state.program.MatchFrom(context.Background(), "é🙂x", 2, PGRegexWhole)
	if err != nil || match.Captures != nil || match.Whole != (PGRegexSpan{2, 6}) {
		t.Fatal(match, err)
	}
	q, b := regexpUnitPlan(t, "regexp_substr", "xyz", "(y)", 1, 1, "", 1)
	v, err := q.Evaluate(context.Background(), b, &state)
	if err != nil || v.text != "y" {
		t.Fatal(v, err)
	}
	state.Reset()
	bad, c := regexpUnitPlan(t, "regexp_substr", "a", "[", 2147483647)
	if _, err := bad.Evaluate(context.Background(), c, &state); err == nil || state.program != nil {
		t.Fatal(err)
	}
	if first.text != "🙂" {
		t.Fatal("result alias", first)
	}
	v, err = p.Evaluate(context.Background(), a, &state)
	if err != nil || v.text != "🙂" {
		t.Fatal(v, err)
	}
}

// Cancel only at the selected consumer or tagged matcher's real work checkpoint.
// Err always delegates to the real context; Done is closed by its CancelFunc.
type substrActualCancelContext struct {
	context.Context
	cancel  context.CancelFunc
	phase   string
	entered bool
}

func (c *substrActualCancelContext) Err() error {
	if !c.entered {
		var pcs [128]uintptr
		frames := runtime.CallersFrames(pcs[:runtime.Callers(2, pcs[:])])
		flags, consumer, step, tagged, provider := false, false, false, false, false
		for {
			frame, more := frames.Next()
			flags = flags || strings.HasSuffix(frame.Function, ".regexpFlags")
			consumer = consumer || strings.HasSuffix(frame.Function, ".RegexpPlan.evaluate")
			step = step || strings.HasSuffix(frame.Function, ".(*pgRegexWork).step")
			tagged = tagged || strings.HasSuffix(frame.Function, ".(*PGRegexProgram).matchPGRegexTagged")
			provider = provider || strings.Contains(frame.Function, "compilePGRegex") ||
				strings.Contains(frame.Function, "resolveProgram") ||
				strings.Contains(frame.Function, "PGRegexProgram).")
			if !more {
				break
			}
		}
		admitted := consumer && step && !provider
		switch c.phase {
		case "flags":
			admitted = flags && !provider
		case "tagged-provider":
			admitted = consumer && tagged && step
		}
		if admitted {
			c.entered = true
			c.cancel()
		}
	}
	return c.Context.Err()
}

func TestRegexpSubstrCancellation(t *testing.T) {
	for _, c := range []struct {
		name string
		args []any
		want any
	}{
		{"flags", []any{"a", "a", 1, 1, strings.Repeat("i", 20000)}, "a"},
		{"start", []any{strings.Repeat("é", 20000), "a", 20001}, nil},
		{"occurrence", []any{strings.Repeat("a", 20000), "", 1, 2147483647}, nil},
		{"tagged-provider", []any{strings.Repeat("a", 20000), "(a|aa)*", 1, 1}, "a"},
	} {
		t.Run(c.name, func(t *testing.T) {
			p, a := regexpUnitPlan(t, "regexp_substr", c.args...)
			state := PGRegexExecution{}
			base, cancel := context.WithCancel(context.Background())
			defer cancel()
			ctx := &substrActualCancelContext{Context: base, cancel: cancel, phase: c.name}
			if base.Err() != nil {
				t.Fatal("context canceled before invocation")
			}
			select {
			case <-base.Done():
				t.Fatal("Done closed before invocation")
			default:
			}
			v, err := p.Evaluate(ctx, a, &state)
			if !errors.Is(err, context.Canceled) || !ctx.entered || v != (Value{}) || state.program != nil {
				t.Fatal("actual regexp_substr cancellation", err, ctx.entered, v, state.program)
			}
			select {
			case <-base.Done():
			default:
				t.Fatal("real cancellation did not close embedded Done")
			}
			check := func(want any) {
				t.Helper()
				v, err := p.Evaluate(context.Background(), a, &state)
				if err != nil {
					t.Fatal("same-plan recovery", v, err)
				}
				if v.Type().ID() != Text || v.IsNull() != (want == nil) {
					t.Fatal("same-plan recovery type/null", v, want)
				}
				if want != nil && v.text != want.(string) {
					t.Fatal("same-plan recovery value", v, want)
				}
				if state.program != nil && (state.program.matchWork.ctx != nil || state.program.budget.work != nil) {
					t.Fatal("recovered program retained execution context")
				}
			}
			if c.name == "tagged-provider" {
				a[0] = String(a[0].Type(), "a")
				check("a")
				a[0] = String(a[0].Type(), "b")
				check("")
			} else {
				check(c.want)
			}
		})
	}
}
