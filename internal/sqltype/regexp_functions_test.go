package sqltype

import (
	"context"
	"errors"
	"runtime"
	"strings"
	"stuntgres/internal/sqlstate"
	"testing"
)

func regexpUnitPlan(t *testing.T, name string, raw ...any) (RegexpPlan, [7]Value) {
	t.Helper()
	types := make(map[ID]*Descriptor)
	for _, d := range Builtins() {
		types[d.ID()] = d
	}
	var values [7]Value
	usages := make([]Usage, len(raw))
	for i, v := range raw {
		switch v := v.(type) {
		case string:
			values[i] = String(types[Text], v)
		case int:
			var err error
			values[i], err = Integer(types[Int4], int64(v))
			if err != nil {
				t.Fatal(err)
			}
		case Value:
			values[i] = v
		default:
			t.Fatalf("invalid unit value %T", v)
		}
		usages[i] = values[i].Type().Usage()
	}
	id := Text
	switch RegexpFunctionKind(name) {
	case RegexpCount, RegexpInstr:
		id = Int4
	case RegexpLike:
		id = Bool
	case RegexpMatch, RegexpMatches, RegexpSplitArray:
		id = 1009
	}
	p, err := BindRegexpFunction(name, usages, types[id].Usage(), CollationRef{ID: 950}, false)
	if err != nil {
		t.Fatal(err)
	}
	return p, values
}
func regexpUnitOutput(t *testing.T, name, want string, raw ...any) Value {
	t.Helper()
	p, a := regexpUnitPlan(t, name, raw...)
	state := PGRegexExecution{}
	value, err := p.Evaluate(context.Background(), a, &state)
	if err != nil {
		t.Fatal(err)
	}
	got, err := value.Type().Output(context.Background(), value)
	if err != nil || got != want {
		t.Fatalf("%s: %q, %v; want %q", name, got, err, want)
	}
	return value
}
func TestRegexpFunctionsFlags(t *testing.T) {
	for _, c := range []struct{ s, p, f, w string }{
		{"A", "a", "ic", "f"}, {"A", "a", "ci", "t"}, {"aaab", "a+b", "e", "f"}, {"aaab", "(?e)a+b", "", "t"}, {"aaab", "***:a+b", "e", "t"},
		{"(?i)a", "(?i)a", "q", "t"}, {"a", "(?i)a", "q", "f"}, {"a b", "a b", "xqt", "t"}, {"a\nb", "^b", "n", "t"}, {"a\nb", "^b", "p", "f"},
	} {
		regexpUnitOutput(t, "regexp_like", c.w, c.s, c.p, c.f)
	}
	for _, flags := range []string{"qx", "qn", "qp", "qw"} {
		p, a := regexpUnitPlan(t, "regexp_like", "a", "a", flags)
		_, err := p.Evaluate(context.Background(), a, &PGRegexExecution{})
		var d *sqlstate.Error
		if !errors.As(err, &d) || d.Code != "2201B" || d.Message != "invalid regular expression: invalid argument to regex function" {
			t.Fatal(flags, err)
		}
	}
}
func TestRegexpFunctionsOriginalStart(t *testing.T) {
	for _, c := range []struct{ pattern, want string }{{"^", "0"}, {`\A`, "0"}, {`\mb`, "0"}, {"(?<=a)b", "1"}} {
		regexpUnitOutput(t, "regexp_count", c.want, "abc", c.pattern, 2)
	}
	regexpUnitOutput(t, "regexp_count", "1", "é🙂x", "", 4)
	regexpUnitOutput(t, "regexp_count", "0", "é🙂x", "", 5)
	regexpUnitOutput(t, "regexp_instr", "0", "é🙂x", "", 5)
	regexpUnitOutput(t, "regexp_replace", "é🙂x", "é🙂x", "", "X", 5, 0)
	for _, name := range []string{"regexp_count", "regexp_instr", "regexp_replace"} {
		args := []any{"abc", "[", 5}
		if name == "regexp_replace" {
			args = []any{"abc", "[", "X", 5}
		}
		plan, values := regexpUnitPlan(t, name, args...)
		state := PGRegexExecution{}
		_, err := plan.Evaluate(context.Background(), values, &state)
		var diagnostic *sqlstate.Error
		if !errors.As(err, &diagnostic) || diagnostic.Code != "2201B" || state.program != nil {
			t.Fatal(name, err, state.program)
		}
	}
	program, err := CompilePGRegex(context.Background(), "(🙂)", PGRegexOptions{}, CollationRef{ID: 950})
	if err != nil {
		t.Fatal(err)
	}
	match, err := program.MatchFrom(context.Background(), "é🙂x", 2, PGRegexWhole)
	if err != nil || match.Whole != (PGRegexSpan{2, 6}) || match.Captures != nil {
		t.Fatal(match, err)
	}
	boolean, err := program.Match(context.Background(), "é🙂x", false)
	if err != nil || !boolean.Matched || boolean.Whole != (PGRegexSpan{-1, -1}) || boolean.Captures != nil {
		t.Fatal(boolean, err)
	}
}
func TestRegexpFunctionsIteration(t *testing.T) {
	regexpUnitOutput(t, "regexp_count", "4", "é🙂x", "")
	regexpUnitOutput(t, "regexp_count", "1", "é🙂x", "", 4)
	regexpUnitOutput(t, "regexp_count", "0", "é🙂x", "", 5)
	regexpUnitOutput(t, "regexp_count", "1", "aaa", "aa")
	regexpUnitOutput(t, "regexp_instr", "3", "é🙂x", "🙂", 1, 1, 1)
	regexpUnitOutput(t, "regexp_instr", "2", "abc", "b", 1, 1, 0, "", 1)
	p, a := regexpUnitPlan(t, "regexp_matches", "é🙂", "", "g")
	values, err := p.EvaluateSet(context.Background(), a, &PGRegexExecution{})
	if err != nil || len(values) != 3 {
		t.Fatal(values, err)
	}
	for _, v := range values {
		if v.ArrayLen() != 1 || v.ArrayElement(0).text != "" {
			t.Fatal(v)
		}
	}
}
func TestRegexpFunctionsCaptures(t *testing.T) {
	for _, c := range []struct{ s, p, w string }{{"x", "(a)|(x)", "{NULL,x}"}, {"b", "(a*)b", `{""}`}, {"aba", "(a(b)?)+", "{a,NULL}"}, {"abcdefghij", "(a)(b)(c)(d)(e)(f)(g)(h)(i)(j)", "{a,b,c,d,e,f,g,h,i,j}"}} {
		v := regexpUnitOutput(t, "regexp_match", c.w, c.s, c.p)
		if v.ArrayRank() != 1 || v.ArrayDimension(0).Lower != 1 {
			t.Fatal(v)
		}
	}
	p, a := regexpUnitPlan(t, "regexp_match", "abc", "z")
	v, err := p.Evaluate(context.Background(), a, &PGRegexExecution{})
	if err != nil || !v.IsNull() {
		t.Fatal(v, err)
	}
}
func TestRegexpFunctionsReplacement(t *testing.T) {
	regexpUnitOutput(t, "regexp_replace", "XaXbX", "ab", "", "X", "g")
	regexpUnitOutput(t, "regexp_replace", "abcaXc", "abcabc", "b", "X", 1, 2, "g")
	regexpUnitOutput(t, "regexp_replace", "aXcaXc", "abcabc", "b", "X", 1, 0)
	regexpUnitOutput(t, "regexp_replace", `a0//\q/\0/$1`, "a", "(a)", `\10/\9/\q/\0/$1`)
	regexpUnitOutput(t, "regexp_replace", "<><b>", "b", "(a)?b", `<\1><\&>`)
	regexpUnitOutput(t, "regexp_replace", "abc", "abc", "^", "X", 2)
}
func TestRegexpFunctionsSplit(t *testing.T) {
	for _, c := range []struct{ s, p, w string }{{"é🙂x", "", "{é,🙂,x}"}, {"abc", "x*", "{a,b,c}"}, {"a b", `\s*`, "{a,b}"}, {"a,,b,", ",", `{a,"",b,""}`}, {"", "", `{""}`}, {"a1b2c", "([0-9])", "{a,b,c}"}} {
		regexpUnitOutput(t, "regexp_split_to_array", c.w, c.s, c.p)
	}
	p, a := regexpUnitPlan(t, "regexp_split_to_table", "a,,b,", ",")
	values, err := p.EvaluateSet(context.Background(), a, &PGRegexExecution{})
	if err != nil || len(values) != 4 {
		t.Fatal(values, err)
	}
	for i, w := range []string{"a", "", "b", ""} {
		if values[i].text != w || values[i].IsNull() {
			t.Fatal(values)
		}
	}
}
func TestRegexpFunctionsOwnership(t *testing.T) {
	p, a := regexpUnitPlan(t, "regexp_matches", "aba", "(a(b)?)", "g")
	state := PGRegexExecution{}
	first, err := p.EvaluateSet(context.Background(), a, &state)
	if err != nil {
		t.Fatal(err)
	}
	next, b := regexpUnitPlan(t, "regexp_match", "z", "(z)")
	if _, err = next.Evaluate(context.Background(), b, &state); err != nil {
		t.Fatal(err)
	}
	state.Reset()
	for i, w := range []string{"{ab,b}", "{a,NULL}"} {
		got, err := first[i].Type().Output(context.Background(), first[i])
		if err != nil || got != w {
			t.Fatal(first, err)
		}
	}
}
func TestRegexpFunctionsBudget(t *testing.T) {
	p, a := regexpUnitPlan(t, "regexp_replace", "a", "a", strings.Repeat("x", 1024*1024), "g")
	state := PGRegexExecution{limit: 256 * 1024}
	if _, err := p.Evaluate(context.Background(), a, &state); err == nil || state.program != nil || state.limit != 256*1024 {
		t.Fatal(err, state.program, state.limit)
	}
	q, b := regexpUnitPlan(t, "regexp_count", "a", "a")
	v, err := q.Evaluate(context.Background(), b, &state)
	if err != nil || v.integer != 1 {
		t.Fatal(v, err)
	}
}
func TestRegexpFunctionsLoopCancellation(t *testing.T) {
	for _, tc := range []struct {
		name  string
		raw   []any
		phase string
		poll  bool
	}{
		{"regexp_like", []any{"a", "a", strings.Repeat("i", 20000)}, "regexpFlags", false},
		{"regexp_count", []any{strings.Repeat("é", 20000), "a", 20001}, "RegexpPlan.evaluate", true},
		{"regexp_replace", []any{"a", "a", strings.Repeat("x", 20000)}, "RegexpPlan.evaluate.func", false},
	} {
		p, a := regexpUnitPlan(t, tc.name, tc.raw...)
		state := PGRegexExecution{}
		ctx := &regexpConsumerPhaseContext{Context: context.Background(), phase: tc.phase, poll: tc.poll}
		_, err := p.Evaluate(ctx, a, &state)
		if !errors.Is(err, context.Canceled) || !ctx.entered || state.program != nil {
			t.Fatal(tc.name, err, ctx.entered, state.program)
		}
		q, b := regexpUnitPlan(t, "regexp_count", "aba", "a")
		v, err := q.Evaluate(context.Background(), b, &state)
		if err != nil || v.integer != 2 {
			t.Fatal(v, err)
		}
	}
}

// Reject compiler/matcher frames: these cases must enter the selected function's
// own active consumer work rather than satisfying cancellation at a nested gate.
type regexpConsumerPhaseContext struct {
	context.Context
	phase         string
	poll, entered bool
}

func (c *regexpConsumerPhaseContext) Err() error {
	var pcs [32]uintptr
	frames := runtime.CallersFrames(pcs[:runtime.Callers(2, pcs[:])])
	found, polled, provider := false, false, false
	for {
		frame, more := frames.Next()
		found = found || strings.Contains(frame.Function, c.phase)
		polled = polled || strings.Contains(frame.Function, "(*pgRegexWork).step")
		provider = provider || strings.Contains(frame.Function, "compilePGRegex") || strings.Contains(frame.Function, "resolveProgram") || strings.Contains(frame.Function, "PGRegexProgram).")
		if !more {
			break
		}
	}
	if found && !provider && (!c.poll || polled) {
		c.entered = true
		return context.Canceled
	}
	return nil
}
