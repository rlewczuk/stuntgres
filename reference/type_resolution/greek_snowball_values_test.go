package reference_test

import (
	"database/sql"
	"fmt"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceGreekSnowballMorphology(t *testing.T) {
	for _, table := range [][][3]string{greekMorphologyCorpus0, greekMorphologyCorpus1, greekMorphologyCorpus2, greekMorphologyCorpus3, greekMorphologyCorpus4, greekMorphologyCorpus5, greekMorphologyCorpus6, greekMorphologyComposition, greekMorphologySequential} {
		for start := 0; start < len(table); start += 24 {
			end := start + 24
			if end > len(table) {
				end = len(table)
			}
			group := table[start:end]
			t.Run(fmt.Sprintf("%s/%s", group[0][0], group[0][1]), func(t *testing.T) {
				dictionaryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
					regtsExec(t, p, b, c, "CREATE TEXT SEARCH DICTIONARY greek_rules (TEMPLATE=pg_catalog.snowball,Language='GrEeK')", 0)
					for _, name := range []string{"greek_rules", "pg_catalog.greek_stem"} {
						q := "SELECT ts_lexize('" + name + "'::regdictionary,$1::text) AS lexemes"
						stmt, err := c.PrepareContext(p.Ctx, q)
						if err != nil {
							t.Fatal(err)
						}
						for _, tc := range group {
							t.Run(tc[0]+"/"+tc[1], func(t *testing.T) {
								for _, e := range []fixture.Executor{c, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}} {
									dictionaryResult(t, p, b, e, q, "{"+tc[2]+"}", tc[1])
								}
							})
						}
						stmt.Close()
					}
				})
			})
		}
	}
}

func TestReferenceGreekSnowballBoundaries(t *testing.T) {
	dictionaryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		q := "SELECT ts_lexize('greek_stem',$1::text) AS lexemes"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		for _, tc := range [][2]string{
			{"a", "a"}, {"α", "α"}, {"αα", "αα"}, {"ααα", "αα"}, {"σσσ", "σσσ"},
			{"Ά", "α"}, {"ΟΣ", "οσ"}, {"ς", "σ"}, {"ϊ", "η"}, {"ΐ", "η"}, {"Ϊ", "ι"}, {"Ϋ", "υ"}, {"ϋ", "υ"}, {"ΰ", "υ"},
			{"καλός!", "καλοσ!"}, {"καλο\u0301", "καλο\u0301"}, {"Α\u0301", "α\u0301"},
			{"café", "café"}, {"CAFÉS", "cafÉs"}, {" καλός ", "\" καλοσ \""}, {"καλός ", "\"καλοσ \""}, {" καλός", "\" καλ\""},
			{strings.Repeat("Α", 500), strings.Repeat("α", 499)},
			{"A" + strings.Repeat("Α", 500), "a" + strings.Repeat("Α", 500)},
			{strings.Repeat("A", 1000), strings.Repeat("a", 1000)},
			{strings.Repeat("A", 1001), strings.Repeat("a", 1001)},
			{"AB" + strings.Repeat("Α", 499), "ab" + strings.Repeat("α", 498)},
			{"ABC" + strings.Repeat("Α", 499), "abc" + strings.Repeat("Α", 499)},
		} {
			for _, e := range []fixture.Executor{c, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}} {
				dictionaryResult(t, p, b, e, q, "{"+tc[1]+"}", tc[0])
			}
		}
	})
}

func TestReferenceGreekSnowballMetadata(t *testing.T) {
	dictionaryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, q := range []string{"SELECT ts_lexize($1,$2) AS lexemes", "SELECT ts_lexize($1::regdictionary,$2::text) AS lexemes"} {
			stmt, err := c.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			for _, tc := range []struct {
				args []any
				want any
			}{
				{[]any{"greek_stem", "καλός"}, "{καλ}"}, {[]any{[]byte("greek_stem"), []byte("ONLY")}, "{only}"},
				{[]any{"greek_stem", ""}, "{}"}, {[]any{"greek_stem", []byte{}}, "{}"}, {[]any{"greek_stem", nil}, nil}, {[]any{nil, "καλός"}, nil},
			} {
				for _, e := range []fixture.Executor{c, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}} {
					dictionaryResult(t, p, b, e, q, tc.want, tc.args...)
				}
			}
			var want any = "{}"
			if b.PG {
				want = nil
			}
			for _, e := range []fixture.Executor{c, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}} {
				dictionaryResult(t, p, b, e, q, want, "greek_stem", []byte(nil))
			}
			stmt.Close()
		}
		regtsExec(t, p, b, c, `CREATE TEXT SEARCH DICTIONARY "Greek Quoted" (TEMPLATE=snowball,Language=greek)`, 0)
		for _, prepared := range []bool{false, true} {
			for _, q := range []string{`SELECT ts_lexize('public."Greek Quoted"','καλός') AS lexemes`, `SELECT ts_lexize('pg_catalog.greek_stem','καλός') AS lexemes`} {
				s := fixture.Select(q, []string{"lexemes"}, []string{"_TEXT"}, [][]any{{"{καλ}"}})
				s.Prepared = prepared
				fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
			}
			s := fixture.Select("SELECT ts_lexize('greek_stem','καλός') AS lexemes WHERE false", []string{"lexemes"}, []string{"_TEXT"}, nil)
			s.CheckMetadata = func(t *testing.T, rows *sql.Rows) { greekLexizeMetadata(t, b, rows) }
			s.Prepared = prepared
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
		}
		regtsSelect(t, p, b, c, "SELECT prorettype::bigint,proisstrict FROM pg_proc WHERE oid=3723", []string{"prorettype", "proisstrict"}, []string{"INT8", "BOOL"}, [][]any{{int64(1009), true}})
	})
}
