package sqltype

import (
	"context"
	"errors"
	"runtime"
	"strings"
	"stuntgres/internal/sqlstate"
	"testing"
	"unsafe"
)

// Cancel only from a named provider phase. A pre-entry or final-return context
// check cannot satisfy execution cases, which require the primitive work poll.
type pgRegexPhaseContext struct {
	context.Context
	phase   string
	poll    bool
	entered bool
}

func (c *pgRegexPhaseContext) Err() error {
	var pcs [32]uintptr
	n := runtime.Callers(2, pcs[:])
	frames := runtime.CallersFrames(pcs[:n])
	found, polled := false, false
	for {
		f, more := frames.Next()
		found = found || strings.Contains(f.Function, c.phase)
		polled = polled || strings.Contains(f.Function, "(*pgRegexWork).step")
		if !more {
			break
		}
	}
	if found && (!c.poll || polled) {
		c.entered = true
		return context.Canceled
	}
	return nil
}
func TestPGRegexInFlightCompilationCancellation(t *testing.T) {
	for _, tc := range []struct {
		phase, pattern string
		poll           bool
	}{
		{"(*pgRegexScanner).prefix", "(?" + strings.Repeat("i", 20000) + ")a", false},
		{"(*pgRegexScanner).skip", "(?x)" + strings.Repeat(" ", 20000) + "a", false},
		{"(*pgRegexScanner).digits", `\x` + strings.Repeat("1", 255), false},
		{"(*pgRegexScanner).bracketToken", "[" + strings.Repeat("a", 20000) + "]", false},
		{"(*pgRegexScanner).bracketPart", "[[." + strings.Repeat("a", 20000) + ".]]", false},
		{"(*pgRegexScanner).bound", "a{" + strings.Repeat("0", 20000) + "1}", false},
		{"(*pgRegexParser).lower", "(?:a{255}){255}", true},
	} {
		t.Run(tc.phase, func(t *testing.T) {
			ctx := &pgRegexPhaseContext{Context: context.Background(), phase: tc.phase, poll: tc.poll}
			if _, err := CompilePGRegex(ctx, tc.pattern, PGRegexOptions{}, CollationRef{ID: 950}); !errors.Is(err, context.Canceled) || !ctx.entered {
				t.Fatal(err, ctx.entered)
			}
		})
	}
}
func TestPGRegexInFlightMatchCancellation(t *testing.T) {
	for _, tc := range []struct {
		phase, pattern, source string
		captures               bool
	}{
		{"(*PGRegexProgram).matchPGRegexStates", `z`, strings.Repeat("a", 20000), false},
		{"(*PGRegexProgram).matchPGRegexTagged", `(a*)b`, strings.Repeat("a", 20000), true},
		{"(*PGRegexProgram).taggedInsert", `(a|aa)*b`, strings.Repeat("a", 20000), true},
		{"(*PGRegexProgram).evaluateLook", `(?=a*b)a`, strings.Repeat("a", 20000), false},
		{"(*PGRegexProgram).matchesClass", "[" + strings.Repeat("a", 20000) + "]", "z", false},
		{"(*PGRegexProgram).matchBackreference", `^(a{255}a{255}a{255}a{255}a{255})\1$`, strings.Repeat("a", 2550), true},
	} {
		t.Run(tc.phase+tc.pattern, func(t *testing.T) {
			p, err := CompilePGRegex(context.Background(), tc.pattern, PGRegexOptions{}, CollationRef{ID: 950})
			if err != nil {
				t.Fatal(err)
			}
			ctx := &pgRegexPhaseContext{Context: context.Background(), phase: tc.phase, poll: true}
			if _, err = p.Match(ctx, tc.source, tc.captures); !errors.Is(err, context.Canceled) || !ctx.entered {
				t.Fatal(err, ctx.entered)
			}
			// Reusing the same checked program after a cancelled call is healthy.
			if _, err = p.Match(context.Background(), "b", tc.captures); err != nil {
				t.Fatal(err)
			}
		})
	}
}
func TestPGRegexBudgetsAndReplacement(t *testing.T) {
	collation := CollationRef{ID: 950}
	if _, err := compilePGRegexBudget(context.Background(), "a", PGRegexOptions{}, collation, 1, false); err == nil {
		t.Fatal("compile allowance ignored")
	}
	for _, pattern := range []string{"a", "(a)", "(?=a)a"} {
		p, err := CompilePGRegex(context.Background(), pattern, PGRegexOptions{}, collation)
		if err != nil {
			t.Fatal(err)
		}
		p.budget.limit = p.budget.used + 1
		_, err = p.Match(context.Background(), "a", true)
		var e *sqlstate.Error
		if !errors.As(err, &e) || e.Code != "2201B" || e.Message != "regular expression failed: regular expression is too complex" {
			t.Fatal(pattern, err)
		}
	}
	state := PGRegexExecution{limit: 64 << 10}
	for _, options := range []PGRegexOptions{{}, {CaseInsensitive: true}, {}} {
		got, err := state.regexMatch(context.Background(), "a", "A", options, collation, false)
		if err != nil || got.Matched != options.CaseInsensitive {
			t.Fatal(got, err, options)
		}
	}
	if _, err := state.regexMatch(context.Background(), "[", "a", PGRegexOptions{}, collation, true); err == nil {
		t.Fatal("malformed replacement accepted")
	}
	if state != (PGRegexExecution{limit: 64 << 10}) {
		t.Fatal("failed replacement retained ownership")
	}
	if got, err := state.regexMatch(context.Background(), "(b)", "b", PGRegexOptions{}, collation, true); err != nil || !got.Matched || got.Captures[0] != (PGRegexSpan{0, 1}) {
		t.Fatal(got, err)
	}
	state.Reset()
	if state != (PGRegexExecution{limit: 64 << 10}) {
		t.Fatal("Reset retained cache")
	}
}
func TestPGRegexDeepLookaroundContinuations(t *testing.T) {
	pattern := strings.Repeat("(?=", 5000) + "a" + strings.Repeat(")", 5000) + "a"
	p, err := CompilePGRegex(context.Background(), pattern, PGRegexOptions{}, CollationRef{ID: 950})
	if err != nil {
		t.Fatal(err)
	}
	got, err := p.Match(context.Background(), "a", false)
	if err != nil || !got.Matched {
		t.Fatal(got, err)
	}
}

func TestPGRegexRangeClosureCancellation(t *testing.T) {
	ctx := &pgRegexPhaseContext{Context: context.Background(), phase: "(*PGRegexProgram).compileClassRanges", poll: true}
	if _, err := CompilePGRegex(ctx, `(?i)[A-\uffff]`, PGRegexOptions{}, CollationRef{ID: 811}); !errors.Is(err, context.Canceled) || !ctx.entered {
		t.Fatal("range closure compilation did not cancel in its primitive poll", err, ctx.entered)
	}
	// The same execution owner must drop failed compilation and recover.
	state := PGRegexExecution{}
	ctx = &pgRegexPhaseContext{Context: context.Background(), phase: "(*PGRegexProgram).compileClassRanges", poll: true}
	if _, err := state.regexMatch(ctx, `(?i)[A-\uffff]`, "𐐨", PGRegexOptions{}, CollationRef{ID: 811}, false); !errors.Is(err, context.Canceled) || !ctx.entered {
		t.Fatal(err, ctx.entered)
	}
	if state != (PGRegexExecution{}) {
		t.Fatal("cancelled closure compilation retained ownership")
	}
	if got, err := state.regexMatch(context.Background(), `(?i)[ǅ-ǅ]`, "ǅ", PGRegexOptions{}, CollationRef{ID: 811}, false); err != nil || !got.Matched {
		t.Fatal("closure compilation recovery", got, err)
	}
}

func TestPGRegexLookaroundBudgetOwnership(t *testing.T) {
	p, err := CompilePGRegex(context.Background(), `(?=a*$)a`, PGRegexOptions{}, CollationRef{ID: 950})
	if err != nil {
		t.Fatal(err)
	}
	if got, err := p.Match(context.Background(), "a", false); err != nil || !got.Matched {
		t.Fatal(got, err)
	}
	// Warm the parent state-set and one assertion, then disallow any scratch
	// growth. A longer assertion must count its replacement against the same
	// owner while its parent allocations remain live.
	p.budget.limit = p.budget.used
	_, err = p.Match(context.Background(), strings.Repeat("a", 2000), false)
	var e *sqlstate.Error
	if !errors.As(err, &e) || e.Code != "2201B" || e.Message != "regular expression failed: regular expression is too complex" {
		t.Fatal(err)
	}
}

func TestPGRegexCompiledRangeClosures(t *testing.T) {
	const pattern = `(?i)[A-Z][ǅ-ǅ][σ-σ]`
	p, err := CompilePGRegex(context.Background(), pattern, PGRegexOptions{}, CollationRef{ID: 811})
	if err != nil {
		t.Fatal(err)
	}
	if len(p.classes) != 3 {
		t.Fatal("expected three independent range constructors", p.classes)
	}
	want := [][]pgRegexRange{
		{{'A', 'Z'}, {'a', 'z'}},
		{{'Ǆ', 'ǆ'}},             // Original titlecase plus its lower/upper, coalesced.
		{{'Σ', 'Σ'}, {'σ', 'σ'}}, // Final sigma is not a transitive addition.
	}
	for at, e := range p.classes {
		got := p.classRanges[e.rangeLo:e.rangeHi]
		if len(got) != len(want[at]) {
			t.Fatalf("range %d: %+v want %+v", at, got, want[at])
		}
		for k, r := range got {
			if r != want[at][k] {
				t.Fatalf("range %d: %+v want %+v", at, got, want[at])
			}
		}
	}
	owned := int(unsafe.Sizeof(PGRegexProgram{})) + len(pattern) + cap(p.code)*int(unsafe.Sizeof(pgRegexInst{})) + cap(p.classes)*int(unsafe.Sizeof(pgRegexClassEntry{})) + cap(p.classRanges)*int(unsafe.Sizeof(pgRegexRange{})) + cap(p.preferences)
	if p.budget.used != owned {
		t.Fatal("compiled range storage absent from persistent budget", p.budget.used, owned)
	}
	// Warm state-set scratch, then a long repeated nonmember must not construct
	// case closure again or grow scratch as a function of input length.
	source := strings.Repeat("𐐨", 20000)
	if got, err := p.Match(context.Background(), source, false); err != nil || got.Matched {
		t.Fatal(got, err)
	}
	used := p.budget.used
	if allocated := testing.AllocsPerRun(3, func() {
		got, err := p.Match(context.Background(), source, false)
		if err != nil || got.Matched {
			t.Fatal(got, err)
		}
	}); allocated != 0 {
		t.Fatal("compiled range reuse allocated", allocated)
	}
	if p.budget.used != used {
		t.Fatal("matching grew immutable range construction storage")
	}
}

func TestPGRegexRangeClosureConstructionBudget(t *testing.T) {
	// A-Z contributes its original interval and 26 lowercase singletons before
	// coalescing. Checked capacity grows 8 -> 16 -> 32; old+new peak is 48.
	width := int(unsafe.Sizeof(pgRegexRange{}))
	for _, limit := range []int{48*width - 1, 48 * width} {
		work := pgRegexWork{ctx: context.Background()}
		budget := pgRegexBudget{limit: limit, work: &work}
		p := PGRegexProgram{
			options:   PGRegexOptions{CaseInsensitive: true},
			collation: CollationRef{ID: 811},
			classes:   []pgRegexClassEntry{{lo: 'A', hi: 'Z'}},
		}
		err := p.compileClassRanges(&budget)
		if limit == 48*width-1 {
			var e *sqlstate.Error
			if !errors.As(err, &e) || e.Code != "2201B" || e.Message != "invalid regular expression: regular expression is too complex" {
				t.Fatal("closure growth ignored simultaneous old/new allowance", err)
			}
		} else if err != nil || budget.used != 32*width || len(p.classRanges) != 2 {
			t.Fatal("exact closure construction allowance", err, budget, p.classRanges)
		}
	}
}
