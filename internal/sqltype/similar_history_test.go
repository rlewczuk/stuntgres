package sqltype

import (
	"context"
	"errors"
	"testing"
)

func TestSimilarPreferenceHistoryOrderAndInterning(t *testing.T) {
	p := similarTestProgram(t, `#"a#"`, "#", 950)
	w := similarWork{ctx: context.Background()}
	p.budget.work = &w
	defer func() { p.budget.work = nil }()
	appendLength := func(previous, length int) int {
		t.Helper()
		id, err := p.appendSimilarHistory(&w, previous, length)
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	a := appendLength(appendLength(0, 2), 1)
	b := appendLength(appendLength(0, 1), 2)
	if again := appendLength(appendLength(0, 2), 1); again != a {
		t.Fatal("equivalent histories have different state identity", a, again)
	}
	for _, tc := range []struct {
		pref int8
		want int
	}{{1, 1}, {-1, -1}} {
		got, err := p.compareSimilarHistory(&w, a, b, tc.pref)
		if err != nil || got != tc.want {
			t.Fatal(tc, got, err)
		}
	}
	empty := appendLength(0, 0)
	greedy, err := p.compareSimilarHistory(&w, empty, 0, 1)
	if err != nil || greedy != 1 {
		t.Fatal(greedy, err)
	}
	lazy, err := p.compareSimilarHistory(&w, empty, 0, -1)
	if err != nil || lazy != -1 {
		t.Fatal(lazy, err)
	}
}
func TestSimilarPreferenceHistoryCancellation(t *testing.T) {
	p := similarTestProgram(t, `#"a#"`, "#", 950)
	w := similarWork{ctx: context.Background()}
	p.budget.work = &w
	defer func() { p.budget.work = nil }()
	a, b := 0, 0
	for range 10000 {
		var err error
		a, err = p.appendSimilarHistory(&w, a, 1)
		if err != nil {
			t.Fatal(err)
		}
		b, err = p.appendSimilarHistory(&w, b, 2)
		if err != nil {
			t.Fatal(err)
		}
	}
	w = similarWork{ctx: &likeCountingContext{Context: context.Background()}}
	if _, err := p.compareSimilarHistory(&w, a, b, 1); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
