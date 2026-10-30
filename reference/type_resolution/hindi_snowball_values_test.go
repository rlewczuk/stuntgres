package reference_test

import (
	"database/sql"
	"fmt"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceHindiSnowballMorphology(t *testing.T) {
	if len(hindiSuffixCorpus) != 132 || len(hindiConditionalRejections) != 24 {
		t.Fatal("independent release corpus cardinality", len(hindiSuffixCorpus), len(hindiConditionalRejections))
	}
	for _, table := range [][][3]string{hindiSuffixCorpus, hindiConditionalRejections, hindiNaturalCorpus} {
		for start := 0; start < len(table); start += 24 {
			end := start + 24
			if end > len(table) {
				end = len(table)
			}
			group := table[start:end]
			t.Run(fmt.Sprintf("%s/%s", group[0][0], group[0][1]), func(t *testing.T) {
				dictionaryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
					regtsExec(t, p, b, c, "CREATE TEXT SEARCH DICTIONARY hindi_rules (TEMPLATE=pg_catalog.snowball,Language='HiNdI')", 0)
					for _, name := range []string{"hindi_rules", "pg_catalog.hindi_stem"} {
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

func TestReferenceHindiSnowballBoundaries(t *testing.T) {
	dictionaryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		q := "SELECT ts_lexize('hindi_stem',$1::text) AS lexemes"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		for _, tc := range [][2]string{
			{"करती!", "करती!"}, {" करती ", "\" करती \""}, {"करती ", "\"करती \""},
			{"क\u0301", "क\u0301"}, {"कृ", "कृ"}, {"CAFÉS", "cafÉs"}, {"cafés", "cafés"},
			{strings.Repeat("A", 994) + "की", strings.Repeat("a", 994) + "क"},
			{strings.Repeat("A", 995) + "की", strings.Repeat("a", 995) + "की"},
			{strings.Repeat("A", 1000), strings.Repeat("a", 1000)},
			{strings.Repeat("A", 1001), strings.Repeat("a", 1001)},
		} {
			for _, e := range []fixture.Executor{c, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}} {
				dictionaryResult(t, p, b, e, q, "{"+tc[1]+"}", tc[0])
			}
		}
	})
}

func TestReferenceHindiSnowballMetadata(t *testing.T) {
	t.Run("ordinary-adapter-errors", hindiAdapterErrors)
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
				{[]any{"hindi_stem", "करती"}, "{कर}"}, {[]any{[]byte("hindi_stem"), []byte("ONLY")}, "{only}"},
				{[]any{"hindi_stem", ""}, "{}"}, {[]any{"hindi_stem", []byte{}}, "{}"}, {[]any{"hindi_stem", nil}, nil}, {[]any{nil, "करती"}, nil},
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
				dictionaryResult(t, p, b, e, q, want, "hindi_stem", []byte(nil))
			}
			stmt.Close()
		}
		regtsExec(t, p, b, c, `CREATE TEXT SEARCH DICTIONARY "Hindi Quoted" (TEMPLATE=snowball,Language=hindi)`, 0)
		for _, prepared := range []bool{false, true} {
			for _, q := range []string{`SELECT ts_lexize('public."Hindi Quoted"','करती') AS lexemes`, `SELECT ts_lexize('pg_catalog.hindi_stem','करती') AS lexemes`} {
				s := fixture.Select(q, []string{"lexemes"}, []string{"_TEXT"}, [][]any{{"{कर}"}})
				s.Prepared = prepared
				fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
			}
			s := fixture.Select("SELECT ts_lexize('hindi_stem','करती') AS lexemes WHERE false", []string{"lexemes"}, []string{"_TEXT"}, nil)
			s.CheckMetadata = func(t *testing.T, rows *sql.Rows) { hindiLexizeMetadata(t, b, rows) }
			s.Prepared = prepared
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
		}
		regtsSelect(t, p, b, c, "SELECT prorettype::bigint,proisstrict FROM pg_proc WHERE oid=3723", []string{"prorettype", "proisstrict"}, []string{"INT8", "BOOL"}, [][]any{{int64(1009), true}})
	})
}
