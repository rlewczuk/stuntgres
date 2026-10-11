package sqltype

import (
	"context"
	"reflect"
	"testing"
)

func TestPGRegexCaptureVectors(t *testing.T) {
	// Independent byte offsets distinguish final repeated captures from earlier
	// equal text. {-1,-1} is absence; equal nonnegative endpoints are empty.
	for _, tc := range []struct {
		source, pattern string
		whole           PGRegexSpan
		captures        []PGRegexSpan
	}{
		{"foobar", "o.b", PGRegexSpan{1, 4}, nil},
		{"foobar", "o(.)b", PGRegexSpan{1, 4}, []PGRegexSpan{{2, 3}}},
		{"XY1234Z", `Y*([0-9]{1,3})`, PGRegexSpan{1, 5}, []PGRegexSpan{{2, 5}}},
		{"XY1234Z", `Y*?([0-9]{1,3})`, PGRegexSpan{1, 3}, []PGRegexSpan{{2, 3}}},
		{"abc", "a((b)c)", PGRegexSpan{0, 3}, []PGRegexSpan{{1, 3}, {1, 2}}},
		{"abc", "a(b)(c)", PGRegexSpan{0, 3}, []PGRegexSpan{{1, 2}, {2, 3}}},
		{"abc01234xyz", `(.*)(\d+)(.*)`, PGRegexSpan{0, 11}, []PGRegexSpan{{0, 7}, {7, 8}, {8, 11}}},
		{"abc01234xyz", `(.*?)(\d+)(.*)`, PGRegexSpan{0, 4}, []PGRegexSpan{{0, 3}, {3, 4}, {4, 4}}},
		{"abc01234xyz", `(?:(.*?)(\d+)(.*)){1,1}`, PGRegexSpan{0, 11}, []PGRegexSpan{{0, 3}, {3, 8}, {8, 11}}},
		{"weeknights", `(week|wee)(night|knights)`, PGRegexSpan{0, 10}, []PGRegexSpan{{0, 3}, {3, 10}}},
		{"abcbd", `a(b.[bc]*)+`, PGRegexSpan{0, 5}, []PGRegexSpan{{3, 5}}},
		{"xyzaa", `^(.*?)(a*)$`, PGRegexSpan{0, 5}, []PGRegexSpan{{0, 3}, {3, 5}}},
		{"foo/bar/baz", `^([^/]+?)(?:/([^/]+?))(?:/([^/]+?))?$`, PGRegexSpan{0, 11}, []PGRegexSpan{{0, 3}, {4, 7}, {8, 11}}},
		{"foo", `foo(bar)?`, PGRegexSpan{0, 3}, []PGRegexSpan{{-1, -1}}},
		{"bc", `(a*)*`, PGRegexSpan{0, 0}, []PGRegexSpan{{0, 0}}},
		{"b", `(a){0}(b)`, PGRegexSpan{0, 1}, []PGRegexSpan{{-1, -1}, {0, 1}}},
		{"abb", `((a)|b)*`, PGRegexSpan{0, 3}, []PGRegexSpan{{2, 3}, {-1, -1}}},
		{"abab", `(a(b)?)+`, PGRegexSpan{0, 4}, []PGRegexSpan{{2, 4}, {3, 4}}},
		{"aba", `(a(b)?)+`, PGRegexSpan{0, 3}, []PGRegexSpan{{2, 3}, {-1, -1}}},
		{"é🙂x", `(é)(🙂)(x)`, PGRegexSpan{0, 7}, []PGRegexSpan{{0, 2}, {2, 6}, {6, 7}}},
		{"a", `(a)(?:(b)|(c))*`, PGRegexSpan{0, 1}, []PGRegexSpan{{0, 1}, {-1, -1}, {-1, -1}}},
		{"ab", `(a)(?=b(b)?)(b)`, PGRegexSpan{0, 2}, []PGRegexSpan{{0, 1}, {1, 2}}},
		{"x", `(a)|(x)`, PGRegexSpan{0, 1}, []PGRegexSpan{{-1, -1}, {0, 1}}},
		{"abc", `(a|ab)c|b`, PGRegexSpan{0, 3}, []PGRegexSpan{{0, 2}}},
		{"abxxxx", `a|b.*`, PGRegexSpan{0, 1}, nil},
		{"aaa", `(a+?){1}`, PGRegexSpan{0, 1}, []PGRegexSpan{{0, 1}}},
		{"aaa", `(a+?){1,1}`, PGRegexSpan{0, 3}, []PGRegexSpan{{0, 3}}},
		{"aaa", `(a+){1}?`, PGRegexSpan{0, 3}, []PGRegexSpan{{0, 3}}},
		{"aaa", `(a+){1,1}?`, PGRegexSpan{0, 1}, []PGRegexSpan{{0, 1}}},
		{"", `()`, PGRegexSpan{0, 0}, []PGRegexSpan{{0, 0}}},
		{"aab", `(?<=a+)(b)`, PGRegexSpan{2, 3}, []PGRegexSpan{{2, 3}}},
		{"zzabcabc!", `(a(b)c)\1`, PGRegexSpan{2, 8}, []PGRegexSpan{{2, 5}, {3, 4}}},
		{"abab", `(a)(b)\1\2`, PGRegexSpan{0, 4}, []PGRegexSpan{{0, 1}, {1, 2}}},
		{"é🙂é🙂", `(é)(🙂)\1\2`, PGRegexSpan{0, 12}, []PGRegexSpan{{0, 2}, {2, 6}}},
		{"abcdefghijj", `(a)(b)(c)(d)(e)(f)(g)(h)(i)(j)\10`, PGRegexSpan{0, 11}, []PGRegexSpan{{0, 1}, {1, 2}, {2, 3}, {3, 4}, {4, 5}, {5, 6}, {6, 7}, {7, 8}, {8, 9}, {9, 10}}},
	} {
		t.Run(tc.pattern+"/"+tc.source, func(t *testing.T) {
			p, err := CompilePGRegex(context.Background(), tc.pattern, PGRegexOptions{}, CollationRef{ID: 950})
			if err != nil {
				t.Fatal(err)
			}
			for frame := 0; frame < 2; frame++ {
				got, err := p.Match(context.Background(), tc.source, true)
				if err != nil || !got.Matched {
					t.Fatal(got, err)
				}
				if got.Whole != tc.whole || !reflect.DeepEqual(got.Captures, tc.captures) {
					t.Fatalf("spans whole=%+v captures=%+v want whole=%+v captures=%+v", got.Whole, got.Captures, tc.whole, tc.captures)
				}
				if _, err = p.Match(context.Background(), "impossible", true); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}

func TestPGRegexBoolResultDemand(t *testing.T) {
	for _, tc := range []struct {
		source, pattern string
		want            bool
	}{
		{"abab", `(a(b)?)+`, true},
		{"xyz", `(a(b)?)+`, false},
		{"abab", `(a(b)?)\1`, true},
		{"abac", `(a(b)?)\1`, false},
		{"ab", `(?=a(?=b))(a)(b)`, true},
		{"ac", `(?=a(?=b))(a)(b)`, false},
		{"abc", `b`, true},
		{"abc", `z`, false},
	} {
		t.Run(tc.pattern+"/"+tc.source, func(t *testing.T) {
			pgRegexAssertBool(t, tc.source, tc.pattern, 950, tc.want)
		})
	}
}
func TestPGRegexCharacterBoundarySpans(t *testing.T) {
	p, err := CompilePGRegex(context.Background(), `(🙂)(é?)`, PGRegexOptions{}, CollationRef{ID: 950})
	if err != nil {
		t.Fatal(err)
	}
	got, err := p.Match(context.Background(), "x🙂éz", true)
	if err != nil || !got.Matched || got.Whole != (PGRegexSpan{1, 7}) || !reflect.DeepEqual(got.Captures, []PGRegexSpan{{1, 5}, {5, 7}}) {
		t.Fatal(got, err)
	}
}
