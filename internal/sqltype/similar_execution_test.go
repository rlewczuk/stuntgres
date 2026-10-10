package sqltype

import (
	"context"
	"errors"
	"strings"
	"stuntgres/internal/sqlstate"
	"testing"
)

// Err is the translation/compiler's admission poll before replacement storage
// can be allocated. Snapshot cache ownership there, not after eventual failure.
type similarReplacementAdmissionContext struct {
	context.Context
	observe func()
	entered bool
}

func (c *similarReplacementAdmissionContext) Err() error {
	if !c.entered {
		c.entered = true
		c.observe()
	}
	return context.Canceled
}

func similarExecutionBudgetFixture(t *testing.T) SimilarExecution {
	t.Helper()
	ctx := context.Background()
	collation := CollationRef{ID: 100, Derivation: Default}
	canonical, err := TranslateSimilar(ctx, "a", "#")
	if err != nil {
		t.Fatal(err)
	}
	program, err := compileSimilarBudget(ctx, canonical, collation, 64<<10)
	if err != nil {
		t.Fatal(err)
	}
	matched, _, _, err := program.Match(ctx, "a")
	if err != nil || !matched {
		t.Fatal(matched, err)
	}
	if program.budget.used <= len(canonical) || program.budget.used > program.budget.limit {
		t.Fatal("fixture must own accounted IR and scratch", program.budget)
	}
	return SimilarExecution{pattern: "a", escape: "#", translated: canonical, translationValid: true, canonical: canonical, collation: collation, program: program, limit: 64 << 10}
}

func TestSimilarExecutionReplacementOwnership(t *testing.T) {
	for _, kind := range []string{"pattern", "escape", "program", "collation"} {
		t.Run(kind, func(t *testing.T) {
			state := similarExecutionBudgetFixture(t)
			oldCanonical := state.canonical
			staleProgram, staleCanonical, staleTranslation := false, false, false
			admission := &similarReplacementAdmissionContext{Context: context.Background(), observe: func() {
				staleProgram = state.program != nil
				// Collation-only recompilation retains the same already-accounted canonical
				// input; every different-pattern replacement must drop stale cache aliases.
				staleCanonical = state.canonical != ""
				staleTranslation = kind != "collation" && state.translationValid && state.translated == oldCanonical
			}}
			var err error
			switch kind {
			case "pattern":
				_, err = state.normalize(admission, "bb", "#")
			case "escape":
				_, err = state.normalize(admission, "a", "!")
			case "program":
				_, _, _, err = state.match(admission, "^(?:bb)$", "bb", state.collation)
			case "collation":
				_, _, _, err = state.match(admission, oldCanonical, "a", CollationRef{ID: 950, Derivation: Default})
			}
			if !admission.entered || !errors.Is(err, context.Canceled) {
				t.Fatal("replacement admission not observed", err)
			}
			if staleProgram || staleCanonical || staleTranslation {
				t.Errorf("obsolete execution storage retained at allocation admission: program=%t canonical=%t translation=%t", staleProgram, staleCanonical, staleTranslation)
			}
			if state != (SimilarExecution{limit: 64 << 10}) {
				t.Fatal("failed replacement retained cache state or lost configuration", state)
			}
			canonical, err := state.normalize(context.Background(), "b", "#")
			if err != nil {
				t.Fatal(err)
			}
			matched, _, _, err := state.match(context.Background(), canonical, "b", CollationRef{ID: 100, Derivation: Default})
			if err != nil || !matched {
				t.Fatal("replacement recovery", matched, err)
			}
		})
	}
}

func TestSimilarExecutionReplacementBudget(t *testing.T) {
	ctx := context.Background()
	collation := CollationRef{ID: 100, Derivation: Default}
	clean := func(t *testing.T, s *SimilarExecution, limit int) {
		t.Helper()
		if *s != (SimilarExecution{limit: limit}) {
			t.Fatal("active ownership or configuration after failure/reset", *s)
		}
	}
	recovery := func(t *testing.T, s *SimilarExecution) {
		t.Helper()
		canonical, err := s.normalize(ctx, "b", "#")
		if err != nil {
			t.Fatal(err)
		}
		matched, _, _, err := s.match(ctx, canonical, "b", collation)
		if err != nil || !matched {
			t.Fatal("recovery", matched, err)
		}
		if s.program.budget.limit != s.memoryLimit() || s.program.budget.used > s.memoryLimit() || s.program.budget.used <= len(canonical) {
			t.Fatal("canonical/IR/scratch ownership not covered", s.program.budget)
		}
	}
	complexError := func(t *testing.T, err error, runtime bool) {
		t.Helper()
		var d *sqlstate.Error
		if !errors.As(err, &d) || d.Code != "2201B" {
			t.Fatal("expected complexity diagnostic", err)
		}
		prefix := "invalid regular expression: "
		if runtime {
			prefix = "regular expression failed: "
		}
		if !strings.HasPrefix(d.Message, prefix) {
			t.Fatal(d.Message)
		}
	}
	t.Run("normalization", func(t *testing.T) {
		s := similarExecutionBudgetFixture(t)
		canonical, err := s.normalize(ctx, "b", "#")
		if err != nil || s.program != nil || s.canonical != "" {
			t.Fatal("normalization retained old program", err)
		}
		if _, _, _, err = s.match(ctx, canonical, "b", collation); err != nil {
			t.Fatal(err)
		}
		recovery(t, &s)
	})
	t.Run("collation", func(t *testing.T) {
		s := similarExecutionBudgetFixture(t)
		canonical := s.translated
		if _, _, _, err := s.match(ctx, canonical, "a", CollationRef{ID: 950, Derivation: Default}); err != nil {
			t.Fatal(err)
		}
		if s.translated != s.canonical || s.program.budget.limit != s.limit || s.program.budget.used > s.limit {
			t.Fatal("recompiled ownership", s)
		}
		recovery(t, &s)
	})
	t.Run("translation-limit", func(t *testing.T) {
		s := similarExecutionBudgetFixture(t)
		_, err := s.normalize(ctx, strings.Repeat("b", s.limit), "#")
		complexError(t, err, false)
		clean(t, &s, 64<<10)
		recovery(t, &s)
	})
	t.Run("compile-limit", func(t *testing.T) {
		s := similarExecutionBudgetFixture(t)
		canonical, err := s.normalize(ctx, strings.Repeat("b", s.limit/2), "#")
		if err != nil {
			t.Fatal(err)
		}
		_, _, _, err = s.match(ctx, canonical, "b", collation)
		complexError(t, err, false)
		clean(t, &s, 64<<10)
		recovery(t, &s)
	})
	t.Run("compile-invalid", func(t *testing.T) {
		s := similarExecutionBudgetFixture(t)
		canonical, err := s.normalize(ctx, "[", "#")
		if err != nil {
			t.Fatal(err)
		}
		_, _, _, err = s.match(ctx, canonical, "b", collation)
		var d *sqlstate.Error
		if !errors.As(err, &d) || d.Code != "2201B" {
			t.Fatal(err)
		}
		clean(t, &s, 64<<10)
		recovery(t, &s)
	})
	t.Run("runtime-limit", func(t *testing.T) {
		s := SimilarExecution{limit: 64 << 10}
		canonical, err := s.normalize(ctx, `%#"a*#"%`, "#")
		if err != nil {
			t.Fatal(err)
		}
		program, err := compileSimilarBudget(ctx, canonical, collation, s.limit)
		if err != nil {
			t.Fatal(err)
		}
		s.limit = program.budget.used
		program.budget.limit = s.limit
		s.program, s.canonical, s.collation = program, canonical, collation
		limit := s.limit
		_, _, _, err = s.match(ctx, canonical, "aaaa", collation)
		complexError(t, err, true)
		clean(t, &s, limit)
		s.limit = 64 << 10
		recovery(t, &s)
	})
	t.Run("reset", func(t *testing.T) {
		s := similarExecutionBudgetFixture(t)
		s.Reset()
		clean(t, &s, 64<<10)
		recovery(t, &s)
	})
	var text, boolean *Descriptor
	for _, d := range Builtins() {
		if d.ID() == Text {
			text = d
		}
		if d.ID() == Bool {
			boolean = d
		}
	}
	predicate, err := BindSimilar("~", text.Usage(), text.Usage(), boolean.Usage(), collation, false)
	if err != nil {
		t.Fatal(err)
	}
	escape, err := BindSimilarEscape(text.Usage(), text.Usage(), text.Usage())
	if err != nil {
		t.Fatal(err)
	}
	substring, err := BindSimilarSubstring([3]Usage{text.Usage(), text.Usage(), text.Usage()}, text.Usage(), false)
	if err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"predicate", "escape", "substring"} {
		for _, cancel := range []bool{false, true} {
			t.Run(kind+map[bool]string{false: "-null", true: "-cancel"}[cancel], func(t *testing.T) {
				s := similarExecutionBudgetFixture(t)
				evaluationContext := ctx
				source := Null(text)
				if cancel {
					c, cancelContext := context.WithCancel(ctx)
					cancelContext()
					evaluationContext = c
					source = String(text, "a")
				}
				var value Value
				var err error
				switch kind {
				case "predicate":
					value, err = predicate.Evaluate(evaluationContext, source, String(text, s.canonical), &s)
				case "escape":
					value, err = escape.Evaluate(evaluationContext, source, String(text, "#"), &s)
				case "substring":
					value, err = substring.Evaluate(evaluationContext, [3]Value{source, String(text, "a"), String(text, "#")}, &s)
				}
				if cancel {
					if !errors.Is(err, context.Canceled) {
						t.Fatal(err)
					}
				} else if err != nil || !value.null {
					t.Fatal("strict NULL", value, err)
				}
				clean(t, &s, 64<<10)
				recovery(t, &s)
			})
		}
	}
}
