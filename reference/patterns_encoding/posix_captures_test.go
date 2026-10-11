package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

// Full vectors are independent provider-unit inputs in the living plan; the
// public two-TEXT witness deliberately exposes only numbered capture one.
func TestReferencePosixCapturePartitions(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			for _, c := range []struct {
				s, p string
				want any
			}{
				{"foobar", `o.b`, "oob"}, {"foobar", `o(.)b`, "o"}, {"XY1234Z", `Y*([0-9]{1,3})`, "123"}, {"XY1234Z", `Y*?([0-9]{1,3})`, "1"},
				{"abc01234xyz", `(.*)(\d+)(.*)`, "abc0123"}, {"abc01234xyz", `(.*?)(\d+)(.*)`, "abc"}, {"abc01234xyz", `(?:(.*?)(\d+)(.*)){1,1}`, "abc"},
				{"weeknights", `(week|wee)(night|knights)`, "wee"}, {"abc", `a((b)c)`, "bc"}, {"abc", `a(b)(c)`, "b"}, {"abcbd", `a(b.[bc]*)+`, "bd"}, {"xyzaa", `^(.*?)(a*)$`, "xyz"},
				{"foo/bar/baz", `^([^/]+?)(?:/([^/]+?))(?:/([^/]+?))?$`, "foo"}, {"foo", `foo(bar)?`, nil}, {"bc", `(a*)*`, ""}, {"b", `(a){0}(b)`, nil}, {"é🙂x", `(é)(🙂)(x)`, "é"},
				{"a", `(a)(?:(b)|(c))*`, "a"}, {"ab", `(a)(?=b(b)?)(b)`, "a"}, {"x", `(a)|(x)`, nil}, {"abab", `(a(b)?)+`, "ab"}, {"aba", `(a(b)?)+`, "a"},
				{"abc", `(a|ab)c|b`, "ab"}, {"abxxxx", `a|b.*`, "a"}, {"aaa", `(a+?){1}`, "a"}, {"aaa", `(a+?){1,1}`, "aaa"}, {"aaa", `(a+){1}?`, "aaa"}, {"aaa", `(a+){1,1}?`, "a"},
				{"none", `z+`, nil}, {"", `()`, ""}, {"aab", `(?<=a+)(b)`, "b"},
			} {
				for _, q := range []string{`SELECT substring($1::text,$2::text) AS piece`, `SELECT pg_catalog.substring($1::text,$2::text) AS piece`, `SELECT substring($1::text FROM $2::text) AS piece`} {
					similarReferenceRoutes(t, p, b, posixSelect(q, []string{"piece"}, []string{"TEXT"}, [][]any{{c.want}}, c.s, c.p))
				}
			}
		})
	}
}

func TestReferencePosixHeldParameters(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			const q = `SELECT $1 ~ $2 AS m,substring($1,$2) AS piece`
			stmt, err := b.DB.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := fixture.HeldStatement{b.DB, stmt, q}
			for _, c := range []struct {
				s, pattern, want any
				matched          any
			}{
				{"abc", `(b)`, "b", true}, {"abc", `(a*)*`, "a", true}, {nil, `[`, nil, nil}, {"abc", nil, nil, nil}, {"abc", `z`, nil, false}, {"é🙂", `(🙂)`, "🙂", true},
			} {
				fixture.RunStep(t, p.Ctx, held, b.Recorder, posixSelect(q, []string{"m", "piece"}, []string{"BOOL", "TEXT"}, [][]any{{c.matched, c.want}}, c.s, c.pattern))
			}
			fixture.RunStep(t, p.Ctx, held, b.Recorder, posixError(q, "brackets [] not balanced", "abc", "["))
			fixture.RunStep(t, p.Ctx, held, b.Recorder, posixSelect(q, []string{"m", "piece"}, []string{"BOOL", "TEXT"}, [][]any{{true, "b"}}, "abc", "(b)"))
		})
	}
}
