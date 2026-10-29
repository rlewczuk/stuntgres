package fts

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
)

func matchUnit(t *testing.T, vector, query string, want bool) {
	t.Helper()
	v, err := ParseVector(t.Context(), vector)
	if err != nil {
		t.Fatal(err)
	}
	q, err := ParseQuery(t.Context(), query)
	if err != nil {
		t.Fatal(err)
	}
	got, err := v.Match(t.Context(), q)
	if err != nil || got != want {
		t.Fatalf("%q @@ %q = %v, %v; want %v", vector, query, got, err, want)
	}
}

func TestFTSMatchBoolean(t *testing.T) {
	for _, tc := range []struct {
		vector, query string
		want          bool
	}{
		{"fat cat rat", "cat & rat", true}, {"fat cat rat", "cat & cow", false},
		{"fat cat rat", "cat & !cow", true}, {"fat cat rat", "cat & !rat", false},
		{"cat", "cow | cat", true}, {"cat", "cow | rat", false},
		{"cat", "!(cat | cow)", false}, {"cat", "!!cat", true},
		{"", "!foo", true}, {"", "", false}, {"cat", "", false},
		{"Rats", "rat", false}, {"cats", "cat", false},
	} {
		matchUnit(t, tc.vector, tc.query, tc.want)
	}
	q, err := ParseQuery(t.Context(), "cat")
	if err != nil {
		t.Fatal(err)
	}
	for range 2048 {
		q, err = q.Not(t.Context())
		if err != nil {
			t.Fatal(err)
		}
	}
	v, err := ParseVector(t.Context(), "cat")
	if err != nil {
		t.Fatal(err)
	}
	if got, err := v.Match(t.Context(), q); err != nil || !got {
		t.Fatal(got, err)
	}
}

func TestFTSMatchPositions(t *testing.T) {
	for _, tc := range []struct {
		vector, query string
		want          bool
	}{
		{"a:1 b:2", "a<0>b", false}, {"a:1 b:2", "a<1>b", true},
		{"a:1 b:2", "a<2>b", false}, {"a:1 b:3", "a<2>b", true},
		{"a:1 b:3", "a<0>a:*", true}, {"a:1 b:2 c:3", "a<->(b<->c)", true},
		{"q:1,3 x:2 y:4", "q<->(x&y)", false},
		{"q:1 x:2", "q<->(x|y<->z)", true}, {"q:1 y:2", "q<->(x|y<->z)", false},
		{"q:1 y:2 z:3", "q<->(x|y<->z)", true},
		{"x:1 y:2,4 q:3", "!x<->y", true}, {"x:1 y:2,4 q:3", "!(x<->y)", false},
		{"a b c", "a<->b", false}, {"a b c", "!(a<->b)", true},
		{"a b:2", "(a|b)<->b", false}, {"a b:2", "!((a|b)<->b)", true},
		{"a:1 b:16383", "a<16384>b", false}, {"a:1", "!missing<16384>a", true},
		{"a:1 b:1", "(a<16382>!missing)<0>b", false},
		{"a:1 b:1", "(a<16383>!missing)<0>b", false},
		{"a:1 b:1", "(a<16384>!missing)<0>b", true},
		{"a:1 b:2", "(a<16384>!missing)<0>b", false},
		{"a:2 b:2", "(a<16384>!missing)<0>b", true},
		{"a:1 b:16383", "(a<16382>!missing)<0>b", true},
		{"a:1 b:2 c:1 d:2", "(!(a<2>b)&c)<->d", false},
		{"a:1 b:2 c:1 d:4", "(!(a<2>b)&c)<->d", true},
		{"c:1 d:2", "(!(a<2>b)&c)<->d", true},
		{"a:1 b:2 c:1 d:2", "(!(missing<2>b)&c)<->d", true},
		{"a:1 b:2 c:1 d:2", "(!(a<2>missing)&c)<->d", true},
		{"a:1 b:2 c:1 d:2", "((a<2>b)|c)<->d", true},
		{"a:1 b:2 c:1 d:2", "(c|(a<2>b))<->d", true},
		{"a:1 b:2 c:1 d:2", "(!((a<2>b)&missing)&c)<->d", true},
		{"a:1 b:2 c:1 d:2", "(!(!(a<2>b))|c)<->d", true},
		{"a:1 b:2 c:1 d:2", "(!(!(!(a<2>b)))&c)<->d", false},
		{"a:1 b:2 c:1 d:4", "(!(!(!(a<2>b)))&c)<->d", true},
		{"a:1 b:2 c:1 d:2", "((!(a<2>b)|c)&c)<->d", false},
		{"a:1 b:2 c:1 d:4", "((!(a<2>b)|c)&c)<->d", true},
	} {
		matchUnit(t, tc.vector, tc.query, tc.want)
	}
	finite := matchResult{state: matchYes, ends: []int{1, 3}}
	complement := finite
	complement.complement = true
	for _, tc := range []struct {
		op         byte
		a, b       matchResult
		ends       []int
		complement bool
		state      byte
	}{
		{'&', finite, finite, []int{1, 3}, false, matchYes},
		{'&', finite, complement, nil, false, matchNo},
		{'&', complement, finite, nil, false, matchNo},
		{'&', complement, complement, []int{1, 3}, true, matchYes},
		{'|', finite, complement, []int{}, true, matchYes},
		{'|', complement, finite, []int{}, true, matchYes},
		{'|', complement, complement, []int{1, 3}, true, matchYes},
		{'|', matchResult{state: matchMaybe}, finite, nil, false, matchMaybe},
		{'&', matchResult{state: matchMaybe}, matchResult{}, nil, false, matchNo},
	} {
		r, err := matchCombine(t.Context(), tc.op, 0, tc.a, tc.b)
		if err != nil || r.state != tc.state || r.complement != tc.complement || !slices.Equal(r.ends, tc.ends) {
			t.Fatal(tc, r, err)
		}
	}
	r, err := matchCombine(t.Context(), '<', 16384, finite, complement)
	if err != nil || r.width != 16384 || !reflect.DeepEqual(r.ends, []int{16385, 16387}) {
		t.Fatal(r, err)
	}
	for _, tc := range []struct{ stored, position int }{
		{16383, 16383}, {16384, 0}, {16385, 1}, {65535, 16383}, {0, 0},
	} {
		result := matchResult{state: matchYes, width: 65536, ends: []int{tc.stored}}
		if result.at(0) != tc.position || result.width != 65536 {
			t.Fatal("position bits changed wide width", tc, result)
		}
	}
	// Output existence is decided before uint16 storage. A positive end
	// that narrows to zero remains a successful child, not an empty set.
	wrapped, err := matchCombine(t.Context(), '<', 16384,
		matchResult{state: matchYes, ends: []int{1}},
		matchResult{state: matchYes, width: 49151, complement: true})
	if err != nil || wrapped.state != matchYes || wrapped.width != 65535 || !slices.Equal(wrapped.ends, []int{0}) {
		t.Fatal("generated WordEntryPos storage", wrapped, err)
	}
	a := matchResult{state: matchYes, ends: []int{1}}
	b := matchResult{state: matchYes, ends: []int{2}}
	failed, err := matchCombine(t.Context(), '<', 2, a, b)
	if err != nil || failed.state != matchNo || failed.width != 2 || failed.len() != 0 {
		t.Fatal("failed phrase lost calculated width", failed, err)
	}
	for _, operands := range [][2]matchResult{{failed, a}, {a, failed}} {
		aligned, err := matchCombine(t.Context(), '|', 0, operands[0], operands[1])
		if err != nil || aligned.state != matchYes || aligned.width != 0 || !slices.Equal(aligned.ends, []int{1}) {
			t.Fatal("failed OR child retained alignment width", aligned, err)
		}
		early, err := matchCombine(t.Context(), '&', 0, operands[0], operands[1])
		if err != nil || early.state != matchNo || early.width != 0 {
			t.Fatal("early AND failure inherited child width", early, err)
		}
	}
}

func TestFTSMatchPrefixWeights(t *testing.T) {
	for _, tc := range []struct {
		vector, query string
		want          bool
	}{
		{"supernova", "super", false}, {"supernova", "super:*", true},
		{"supeanova supernova", "super:*", true}, {"asupernova", "super:*", false},
		{"wa:1D wb:2A", "w:*D & w:*A", true}, {"wa:1D wb:2A", "w:*D<->w:*A", true},
		{"wa:1A wb:2D", "w:*D<->w:*A", false}, {"wa:1A", "w:*D", false},
		{"wa", "w:*D", true}, {"w:1A wa:2D", "w:*D", true},
		{"wa:1 wb:1 c:2", "w:*<->c", true}, {"wa:1 wb c:2", "w:*<->c", false},
		{"wa:1 wb c:2", "!(w:*<->c)", true},
		{"a:1A b:2B c:3C d:4", "a:A & b:B & c:C & d:D", true},
		{"a:1A b:2B c:3C d:4", "a:AB & c:ACD & d:ACD", true},
	} {
		matchUnit(t, tc.vector, tc.query, tc.want)
	}
}

func TestFTSMatchCancellation(t *testing.T) {
	v, err := ParseVector(t.Context(), "a:1 b:2")
	if err != nil {
		t.Fatal(err)
	}
	q, err := ParseQuery(t.Context(), "a<->b")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := v.Match(ctx, q); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	q, err = ParseQuery(t.Context(), strings.Repeat("(a & ", 128)+"a"+strings.Repeat(")", 128))
	if err != nil {
		t.Fatal(err)
	}
	interior := &countingContext{Context: t.Context(), after: 20}
	if _, err := v.Match(interior, q); !errors.Is(err, context.Canceled) || interior.calls != 20 {
		t.Fatal(interior.calls, err)
	}
	invalid := &Query{root: 0, nodes: []queryNode{{op: '&', left: 0, right: 0}}}
	if _, err := v.Match(t.Context(), invalid); err == nil {
		t.Fatal("invalid private arena admitted")
	}
	if _, err := (*Vector)(nil).Match(t.Context(), q); err == nil {
		t.Fatal("invalid vector admitted")
	}
	t.Run("positional_merge_checkpoints", func(t *testing.T) {
		// Prefix unions can contain far more positions than one stored lexeme.
		// A single unmatched leading end followed by equal pairs advances the
		// cursor sum through odd numbers only; cadence must count iterations.
		const count = 16383
		all := make([]int, count)
		odd, even := make([]int, 0, count/2+1), make([]int, 0, count/2)
		for i := range all {
			all[i] = i + 1
			if all[i]&1 != 0 {
				odd = append(odd, all[i])
			} else {
				even = append(even, all[i])
			}
		}
		for _, tc := range []struct {
			name string
			left, right []int
		}{
			{"left_leading_then_pairs", all, all[1:]},
			{"right_leading_then_pairs", all[1:], all},
			{"alternating_single_cursor", odd, even},
		} {
			t.Run(tc.name, func(t *testing.T) {
				leftBefore := append([]int(nil), tc.left...)
				rightBefore := append([]int(nil), tc.right...)
				left := matchResult{state: matchYes, ends: tc.left}
				right := matchResult{state: matchYes, ends: tc.right}
				// Entry, initial merge checkpoint, then bounded interior
				// checkpoints: cancellation occurs before this long merge ends.
				interior := &countingContext{Context: t.Context(), after: 4}
				result, err := matchCombine(interior, '&', 0, left, right)
				if !errors.Is(err, context.Canceled) || result.state != matchNo || result.len() != 0 {
					t.Fatal("interior positional cancellation", result, err)
				}
				if !slices.Equal(tc.left, leftBefore) || !slices.Equal(tc.right, rightBefore) {
					t.Fatal("canceled positional merge mutated inputs")
				}
			})
		}
		t.Run("completed_merge_checkpoint", func(t *testing.T) {
			for _, end := range []int{1, 2} {
				left := matchResult{state: matchYes, ends: []int{1}}
				right := matchResult{state: matchYes, ends: []int{end}}
				// With one merge iteration, cancellation becomes observable
				// only at completion, for both a match and an empty result.
				completion := &countingContext{Context: t.Context(), after: 3}
				result, err := matchCombine(completion, '&', 0, left, right)
				if !errors.Is(err, context.Canceled) || result.state != matchNo || result.len() != 0 {
					t.Fatal("completed positional cancellation", result, err)
				}
				if left.at(0) != 1 || right.at(0) != end {
					t.Fatal("completed canceled merge mutated inputs")
				}
			}
		})
	})
}

func TestFTSMatchImmutability(t *testing.T) {
	v, err := ParseVector(t.Context(), "wa:1D wb:2A c:3")
	if err != nil {
		t.Fatal(err)
	}
	q, err := ParseQuery(t.Context(), "(w:*D<->w:*A)<->c")
	if err != nil {
		t.Fatal(err)
	}
	beforeV, err := ParseVector(t.Context(), v.String())
	if err != nil {
		t.Fatal(err)
	}
	beforeQ, err := ParseQuery(t.Context(), q.String())
	if err != nil {
		t.Fatal(err)
	}
	backingV, backingQ := &v.lexemes[0], &q.nodes[0]
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			for range 32 {
				if got, err := v.Match(t.Context(), q); err != nil || !got {
					t.Error(got, err)
				}
			}
		})
	}
	wg.Wait()
	absent, err := ParseVector(t.Context(), "wa:1D wb:3A c:4")
	if err != nil {
		t.Fatal(err)
	}
	beforeAbsent, err := ParseVector(t.Context(), absent.String())
	if err != nil {
		t.Fatal(err)
	}
	if got, err := absent.Match(t.Context(), q); err != nil || got || !reflect.DeepEqual(absent, beforeAbsent) {
		t.Fatal("false match mutated operands", got, err)
	}
	cancel := &countingContext{Context: t.Context(), after: 5}
	if _, err := v.Match(cancel, q); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(v, beforeV) || !reflect.DeepEqual(q, beforeQ) || &v.lexemes[0] != backingV || &q.nodes[0] != backingQ {
		t.Fatal("mutated match operands")
	}
}
