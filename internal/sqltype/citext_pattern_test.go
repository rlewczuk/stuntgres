package sqltype

import (
	"context"
	"errors"
	"testing"
)

func TestCitextPatternEvaluation(t *testing.T) {
	d, result := citextUnitType(t, 45000), citextComparisonBool(t)
	var text *Descriptor
	for _, candidate := range Builtins() {
		if candidate.ID() == Text {
			text = candidate
		}
	}
	if text == nil {
		t.Fatal("missing text")
	}
	for _, right := range []*Descriptor{d, text} {
		for kind := uint8(0); kind < 4; kind++ {
			p, err := BindCitextPattern(kind, d.Usage(), right.Usage(), result.Usage(), text.Usage(), d.Usage().Collation, false)
			if err != nil {
				t.Fatal(err)
			}
			state := new(PGRegexExecution)
			for _, tc := range []struct {
				subject, pattern string
				match            bool
			}{{"ABC", "abc", true}, {"ABC", "abd", false}, {"", "", true}, {"é", "_", true}, {"7", `\D`, false}, {"7", `\d`, true}, {"ABC", "(?c)^abc$", false}, {"ABC", "(?i)^abc$", true}} {
				if kind < 2 && (tc.pattern == `\D` || tc.pattern == `\d` || len(tc.pattern) > 3 && tc.pattern[:2] == "(?") {
					continue
				}
				if kind >= 2 && tc.pattern == "_" {
					continue
				}
				a, b := String(d, tc.subject), String(right, tc.pattern)
				got, err := p.Evaluate(context.Background(), a, b, state)
				if err != nil || got != Boolean(result, tc.match != (kind == 1 || kind == 3)) {
					t.Fatal(kind, tc, got, err)
				}
				if a.text != tc.subject || b.text != tc.pattern {
					t.Fatal("mutated pattern")
				}
			}
			for _, args := range [][2]Value{{Null(d), String(right, "[")}, {String(d, "ABC"), Null(right)}, {Null(d), Null(right)}} {
				got, err := p.Evaluate(context.Background(), args[0], args[1], state)
				if err != nil || got != Null(result) {
					t.Fatal(got, err)
				}
			}
			foreign := citextUnitType(t, d.ID())
			if _, err := p.Evaluate(context.Background(), String(foreign, "ABC"), String(right, "abc"), state); err == nil {
				t.Fatal("foreign generation accepted")
			}
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			if _, err := p.Evaluate(ctx, String(d, "ABC"), String(right, "abc"), state); !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			if kind >= 2 {
				if _, err := p.Evaluate(context.Background(), String(d, "ABC"), String(right, "["), state); err == nil {
					t.Fatal("malformed regex accepted")
				}
				got, err := p.Evaluate(context.Background(), String(d, "ABC"), String(right, "abc"), state)
				if err != nil || got != Boolean(result, kind == 2) {
					t.Fatal("recovery", got, err)
				}
			}
		}
	}
	if _, err := BindCitextPattern(0, d.Usage(), citextUnitType(t, d.ID()).Usage(), result.Usage(), text.Usage(), d.Usage().Collation, false); err == nil {
		t.Fatal("foreign descriptor bound")
	}
}
