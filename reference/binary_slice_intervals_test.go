//go:build reference

package reference_test

import (
	"fmt"
	"testing"
)

func TestReferenceBinarySliceIntervals(t *testing.T) {
	p := newReferencePair(t)
	for _, tc := range []struct {
		s, n int
		want string
	}{{1, 3, "abc"}, {0, 3, "ab"}, {-2, 4, "a"}, {-3, 2, ""}, {2, 0, ""}, {7, 2, ""}, {2, 2147483646, "bcdef"}, {-10, 2147483646, "abcdef"}, {-2147483648, 2147483647, ""}} {
		for _, form := range []string{"substring('abcdef'::bytea,%d,%d)", "substr('abcdef'::bytea,%d,%d)", "substring('abcdef'::bytea FROM %d FOR %d)", "substring('abcdef'::bytea FOR %[2]d FROM %[1]d)"} {
			step := referenceSelect("SELECT "+fmt.Sprintf(form, tc.s, tc.n)+" AS v", []string{"v"}, []string{"BYTEA"}, [][]any{{tc.want}})
			p.step(t, step)
			p.step(t, referencePrepared(step))
		}
	}
	for _, tc := range []struct {
		s    int
		want string
	}{{-2, "abcdef"}, {0, "abcdef"}, {1, "abcdef"}, {2, "bcdef"}, {7, ""}, {2147483647, ""}} {
		step := referenceSelect(fmt.Sprintf("SELECT substring('abcdef'::bytea FROM %d) AS a,substr('abcdef'::bytea,%d) AS b", tc.s, tc.s), []string{"a", "b"}, []string{"BYTEA", "BYTEA"}, [][]any{{tc.want, tc.want}})
		p.step(t, step)
		p.step(t, referencePrepared(step))
	}
	for _, tc := range []struct{ count, want string }{{"2::smallint", "ab"}, {"2::bigint", "ab"}, {"1.5::numeric", "ab"}, {"'2'::text", "ab"}, {"TRUE", "a"}} {
		step := referenceSelect("SELECT substring('abcdef'::bytea FOR "+tc.count+") AS v", []string{"v"}, []string{"BYTEA"}, [][]any{{tc.want}})
		p.step(t, step)
		p.step(t, referencePrepared(step))
	}
	for _, tc := range []struct {
		s, n int
		want string
	}{{3, 3, "abXYf"}, {3, 0, "abXYcdef"}, {3, -2, "abXYabcdef"}, {9, 2, "abcdefXY"}, {3, -2147483648, "abXYabcdef"}} {
		step := referenceSelect(fmt.Sprintf("SELECT overlay('abcdef'::bytea,'XY'::bytea,%d,%d) AS v", tc.s, tc.n), []string{"v"}, []string{"BYTEA"}, [][]any{{tc.want}})
		p.step(t, step)
		p.step(t, referencePrepared(step))
	}
	for _, direction := range []string{"", "BOTH ", "LEADING ", "TRAILING "} {
		want := "A\x00\x80B"
		if direction == "LEADING " {
			want += "\xff\x00"
		}
		if direction == "TRAILING " {
			want = "\x00\xff\x80" + want
		}
		for _, from := range []string{"", "FROM "} {
			step := referenceSelect(`SELECT trim(`+direction+from+`'\x00ff8041008042ff00'::bytea,'\xff8000'::bytea) AS v`, []string{"v"}, []string{"BYTEA"}, [][]any{{want}})
			p.step(t, step)
			p.step(t, referencePrepared(step))
		}
	}
	for _, tc := range []struct {
		hay, needle string
		want        int64
	}{{"", "", 1}, {"abc", "", 1}, {"", "x", 0}, {"aaaa", "aa", 1}, {"ababa", "aba", 1}, {"abc", "z", 0}, {"a", "ab", 0}} {
		step := referenceSelect("SELECT pg_catalog.position($1::bytea,$2::bytea) AS p", []string{"p"}, []string{"INT4"}, [][]any{{tc.want}}, []byte(tc.hay), []byte(tc.needle))
		p.step(t, step)
		p.step(t, referencePrepared(step))
	}
}
