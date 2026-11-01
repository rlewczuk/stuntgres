package functions

import (
	"context"
	"errors"
	"math"
	"reflect"
	"strings"
	"testing"
)

// This deliberately literal extractor and exhaustive extent oracle share no
// production extraction, scoring or boundary helpers.
func pgTrgmOracleStream(text string) (values []string, left, right []bool) {
	words := strings.FieldsFunc(text, func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9')
	})
	for _, word := range words {
		padded := "  " + strings.ToLower(word) + " "
		for i := 0; i+3 <= len(padded); i++ {
			values = append(values, padded[i:i+3])
			left = append(left, i == 0)
			right = append(right, i+3 == len(padded))
		}
	}
	return
}

func pgTrgmOracleSet(values []string) map[string]bool {
	set := make(map[string]bool)
	for _, value := range values {
		set[value] = true
	}
	return set
}

func pgTrgmOracleScore(a, b map[string]bool) float32 {
	matches := 0
	for key := range a {
		if b[key] {
			matches++
		}
	}
	if matches == 0 {
		return 0
	}
	return float32(matches) / float32(len(a)+len(b)-matches)
}

func pgTrgmOracleWord(first, second string, strict bool) float32 {
	a, _, _ := pgTrgmOracleStream(first)
	b, left, right := pgTrgmOracleStream(second)
	set := pgTrgmOracleSet(a)
	var best float32
	for start := range b {
		if strict && !left[start] {
			continue
		}
		for end := start; end < len(b); end++ {
			if strict && !right[end] {
				continue
			}
			if score := pgTrgmOracleScore(set, pgTrgmOracleSet(b[start:end+1])); score > best {
				best = score
			}
		}
	}
	return best
}

func TestPgTrgmKernels(t *testing.T) {
	ctx := context.Background()
	for _, test := range []struct {
		text string
		want []string
	}{
		{"", nil}, {" --- é界🙂 ", nil},
		{"cat", []string{"  c", " ca", "at ", "cat"}},
		{"Cat CAT cat", []string{"  c", " ca", "at ", "cat"}},
		{"ab-CD", []string{"  a", "  c", " ab", " cd", "ab ", "cd "}},
		{"café", []string{"  c", " ca", "af ", "caf"}},
		{"A1", []string{"  a", " a1", "a1 "}},
	} {
		keys, err := PgTrgmTrigrams(ctx, test.text)
		if err != nil {
			t.Fatal(err)
		}
		var got []string
		for _, key := range keys {
			got = append(got, string([]byte{byte(key >> 16), byte(key >> 8), byte(key)}))
		}
		if !reflect.DeepEqual(got, test.want) {
			t.Fatalf("trigrams(%q)=%q want %q", test.text, got, test.want)
		}
		if len(keys) != 0 {
			keys[0] = 0
			fresh, err := PgTrgmTrigrams(ctx, test.text)
			if err != nil || fresh[0] == 0 {
				t.Fatal("trigram output reused caller-owned storage", err)
			}
		}
	}
	for _, test := range []struct {
		left, right            string
		ordinary, word, strict float32
	}{
		{"", "", 0, 0, 0}, {"a", "aa", 1.0 / 4, 1.0 / 2, 1.0 / 4},
		{"aa", "a", 1.0 / 4, 1.0 / 3, 1.0 / 4},
		{"word", "two words", 4.0 / 11, 4.0 / 5, 4.0 / 7},
		{"two words", "word", 4.0 / 11, 2.0 / 5, 4.0 / 11},
		{"a b", "x a b y", 4.0 / 8, 1, 1},
	} {
		ordinary, err := PgTrgmSimilarity(ctx, test.left, test.right)
		if err != nil || ordinary != test.ordinary {
			t.Fatalf("ordinary(%q,%q)=%v want %v: %v", test.left, test.right, ordinary, test.ordinary, err)
		}
		for i, strict := range []bool{false, true} {
			want := []float32{test.word, test.strict}[i]
			got, err := PgTrgmWordSimilarity(ctx, test.left, test.right, strict)
			if err != nil || got != want {
				t.Fatalf("word(%q,%q,strict=%v)=%v want %v: %v", test.left, test.right, strict, got, want, err)
			}
		}
	}
	// All short binary-alphabet words, plus repeated/multiple-word extents and
	// non-C characters, exercise every extent against independent set scoring.
	corpus := []string{"", "a", "b", "aa", "ab", "ba", "bb", "aaa", "aab", "aba", "abb", "baa", "bab", "bba", "bbb", "a a", "a b", "b a", "aa a", "a aa", "a b a", "x aa y", "A-Éb", "é"}
	for _, first := range corpus {
		a, _, _ := pgTrgmOracleStream(first)
		for _, second := range corpus {
			b, _, _ := pgTrgmOracleStream(second)
			ordinary, err := PgTrgmSimilarity(ctx, first, second)
			if err != nil || math.Float32bits(ordinary) != math.Float32bits(pgTrgmOracleScore(pgTrgmOracleSet(a), pgTrgmOracleSet(b))) {
				t.Fatalf("independent ordinary oracle disagrees for %q/%q: %v", first, second, err)
			}
			for _, strict := range []bool{false, true} {
				got, err := PgTrgmWordSimilarity(ctx, first, second, strict)
				want := pgTrgmOracleWord(first, second, strict)
				if err != nil || math.Float32bits(got) != math.Float32bits(want) {
					t.Fatalf("independent extent oracle %q/%q strict=%v: got %v want %v: %v", first, second, strict, got, want, err)
				}
				thresholds := []float64{0, 0.25, 0.5, 1, float64(want)}
				if want > 0 {
					thresholds = append(thresholds, math.Nextafter(float64(want), 0))
				}
				if want < 1 {
					thresholds = append(thresholds, math.Nextafter(float64(want), 1))
				}
				for _, threshold := range thresholds {
					witness, err := pgTrgmWordSimilarity(ctx, first, second, strict, true, threshold)
					if err != nil || (float64(witness) >= threshold) != (float64(want) >= threshold) {
						t.Fatalf("check-only oracle %q/%q strict=%v threshold=%.17g: witness %v maximum %v: %v", first, second, strict, threshold, witness, want, err)
					}
				}
			}
		}
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := PgTrgmTrigrams(cancelled, strings.Repeat("a", 8192)); !errors.Is(err, context.Canceled) {
		t.Fatal("extractor ignored cancellation", err)
	}
	if _, err := PgTrgmSimilarity(cancelled, "a", "aa"); !errors.Is(err, context.Canceled) {
		t.Fatal("similarity ignored cancellation", err)
	}
	for _, strict := range []bool{false, true} {
		if _, err := PgTrgmWordSimilarity(cancelled, "a", "aa", strict); !errors.Is(err, context.Canceled) {
			t.Fatal("extent kernel ignored cancellation", err)
		}
		for _, threshold := range []float64{0, 0.5, 1} {
			for _, right := range []string{"", "aa"} {
				if _, err := pgTrgmWordSimilarity(cancelled, "a", right, strict, true, threshold); !errors.Is(err, context.Canceled) {
					t.Fatal("check-only kernel bypassed cancellation at an empty/zero-threshold boundary", strict, threshold, err)
				}
			}
		}
	}
}

// Observe existing cancellation checkpoints and optionally cancel a real
// context after a generous work budget. No clock, goroutine or production hook
// is involved; exact polling counts are not part of the contract.
type pgTrgmWorkContext struct {
	context.Context
	cancel        context.CancelFunc
	checks, limit int
}

func (c *pgTrgmWorkContext) Err() error {
	c.checks++
	if c.limit > 0 && c.checks >= c.limit {
		c.cancel()
	}
	return c.Context.Err()
}

func TestPgTrgmWordPredicateWork(t *testing.T) {
	for _, strict := range []bool{false, true} {
		var fullChecks []int
		for _, words := range []int{512, 4096} {
			right := strings.Repeat("a ", words)
			// Covers complete extraction plus the first qualifying extent
			// with ample slack, but not whole-stream positional traversal.
			budget := 64 + len(right)/128
			check := &pgTrgmWorkContext{Context: t.Context()}
			witness, err := pgTrgmWordSimilarity(check, "a", right, strict, true, 1)
			if err != nil || witness != 1 || check.checks > budget {
				t.Fatalf("predicate did not stop at a complete matching extent: strict=%v words=%d score=%v checks=%d budget=%d: %v", strict, words, witness, check.checks, budget, err)
			}
			full := &pgTrgmWorkContext{Context: t.Context()}
			score, err := PgTrgmWordSimilarity(full, "a", right, strict)
			if err != nil || score != 1 || full.checks <= budget*4 {
				t.Fatalf("full maximum did not retain its separate traversal: strict=%v words=%d score=%v checks=%d budget=%d: %v", strict, words, score, full.checks, budget, err)
			}
			fullChecks = append(fullChecks, full.checks)
			for _, checkOnly := range []bool{false, true} {
				parent, cancel := context.WithCancel(t.Context())
				bounded := &pgTrgmWorkContext{Context: parent, cancel: cancel, limit: budget}
				score, err := pgTrgmWordSimilarity(bounded, "a", right, strict, checkOnly, 1)
				cancel()
				if checkOnly {
					if err != nil || score != 1 || bounded.checks >= budget {
						t.Fatal("check-only path exceeded the prefix work budget", strict, words, score, err)
					}
				} else if !errors.Is(err, context.Canceled) {
					t.Fatal("full traversal fit the prefix-only budget", strict, words, score, err)
				}
			}
			// Negative predicates must keep searching/cancelling, not turn
			// budget exhaustion or the first partial match into success.
			for _, negative := range []struct {
				right     string
				threshold float64
			}{
				{strings.Repeat("b ", words), 0.5},
				{strings.Repeat("aa ", words), 1},
			} {
				parent, cancel := context.WithCancel(t.Context())
				bounded := &pgTrgmWorkContext{Context: parent, cancel: cancel, limit: budget}
				_, err := pgTrgmWordSimilarity(bounded, "a", negative.right, strict, true, negative.threshold)
				cancel()
				if !errors.Is(err, context.Canceled) {
					t.Fatal("negative predicate ignored bounded cancellation", strict, words, err)
				}
			}
		}
		// Eight times the repeated input makes the old full-search work
		// grow by substantially more than eight, for both boundary modes.
		// Together with the prefix budget above this distinguishes the
		// quadratic repeated-matching path without runtime measurements.
		if fullChecks[1] <= fullChecks[0]*12 {
			t.Fatal("repeated-extent scaling witness no longer distinguishes full traversal", strict, fullChecks)
		}
		right := strings.Repeat("aa ", 32) + "a"
		witness, err := pgTrgmWordSimilarity(t.Context(), "a", right, strict, true, 0.25)
		want := float32(0.5)
		if strict {
			want = 0.25
		}
		if err != nil || witness != want {
			t.Fatal("predicate did not return its earlier qualifying extent", strict, witness, err)
		}
		score, err := PgTrgmWordSimilarity(t.Context(), "a", right, strict)
		if err != nil || score != 1 {
			t.Fatal("full maximum was truncated at the predicate witness", strict, score, err)
		}
		// Cancellation during setup still wins over empty score0 >= 0.
		parent, cancel := context.WithCancel(t.Context())
		bounded := &pgTrgmWorkContext{Context: parent, cancel: cancel, limit: 4}
		_, err = pgTrgmWordSimilarity(bounded, "a", "", strict, true, 0)
		cancel()
		if !errors.Is(err, context.Canceled) {
			t.Fatal("empty check-only invocation bypassed setup cancellation", strict, err)
		}
	}
}
