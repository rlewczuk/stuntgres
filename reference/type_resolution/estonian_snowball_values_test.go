package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceEstonianSnowballMorphology(t *testing.T) {
	var groups [][][2]string
	for _, table := range estonianMorphologyGoldens {
		for start := 0; start < len(table); start += 24 {
			end := start + 24
			if end > len(table) {
				end = len(table)
			}
			groups = append(groups, table[start:end])
		}
	}
	for i, group := range groups {
		t.Run(string(rune('A'+i)), func(t *testing.T) {
			dictionaryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				regtsExec(t, p, b, c, "CREATE TEXT SEARCH DICTIONARY estonian_rules (TEMPLATE=pg_catalog.snowball,Language='EsToNiAn')", 0)
				for _, name := range []string{"estonian_rules", "pg_catalog.estonian_stem"} {
					q := "SELECT ts_lexize('" + name + "'::regdictionary,$1::text) AS lexemes"
					stmt, err := c.PrepareContext(p.Ctx, q)
					if err != nil {
						t.Fatal(err)
					}
					for _, tc := range group {
						t.Run(tc[0], func(t *testing.T) {
							dictionaryResult(t, p, b, c, q, "{"+tc[1]+"}", tc[0])
							dictionaryResult(t, p, b, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}, q, "{"+tc[1]+"}", tc[0])
						})
					}
					stmt.Close()
				}
			})
		})
	}
}

func TestReferenceEstonianSnowballBoundaries(t *testing.T) {
	dictionaryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		q := "SELECT ts_lexize('estonian_stem',$1::text) AS lexemes"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		for _, tc := range [][2]string{
			{"ma", "ma"},
			{"ema", "ema"},
			{"abma", "ab"},
			{"abat", "abat"},
			{"abcat", "abca"},
			{"abaga", "aba"},
			{"abäga", "abäga"},
			{"abääga", "abää"},
			{"abai", "aba"},
			{"abäi", "abäi"},
			{"kingi", "kingi"},
			{"bioloogi", "bioloogi"},
			{"kookki", "kook"},
			{"jooksemegi", "jookse"},
			{"raamatuki", "raamatuki"},
			{"pakka", "paka"},
			{"akka", "aka"},
			{"kke", "kke"},
			{"PPE", "ppe"},
			{"RAAMATUD", "raama"},
			{"LÖÖN", "lÖÖn"},
			{"café", "café"},
			{"cafés", "cafés"},
			{"CAFÉS", "cafÉs"},
			{"a", "a"},
			{"bbb", "bbb"},
			{"aaaa", "aaaa"},
			{"THE", "the"},
			{"ONLY", "only"},
			{"RATS", "rats"},
			{"ja", "ja"},
			{"on", "on"},
			{"ei", "ei"},
			{"ning", "ning"},
			{"café", "café"},
			{"raamatud!", "raamatud!"},
			{"raamatud́", "raamatud́"},
			{" raamatud ", "\" raamatud \""},
			{"ab" + strings.Repeat("a", 996) + "ma", "ab" + strings.Repeat("a", 996)},
			{"AB" + strings.Repeat("A", 997) + "ma", "ab" + strings.Repeat("a", 997) + "ma"},
			{"ab" + strings.Repeat("õ", 498) + "ma", "ab" + strings.Repeat("õ", 498)},
			{"AB" + strings.Repeat("õ", 498) + "ama", "ab" + strings.Repeat("õ", 498) + "ama"},
		} {
			for _, e := range []fixture.Executor{c, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}} {
				dictionaryResult(t, p, b, e, q, "{"+tc[1]+"}", tc[0])
			}
		}
	})
}

func TestReferenceEstonianSnowballMetadata(t *testing.T) {
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
				{[]any{"estonian_stem", "raamatud"}, "{raama}"}, {[]any{[]byte("estonian_stem"), []byte("ONLY")}, "{only}"}, {[]any{"estonian_stem", ""}, "{}"}, {[]any{"estonian_stem", []byte{}}, "{}"}, {[]any{"estonian_stem", nil}, nil}, {[]any{nil, "raamatud"}, nil},
			} {
				dictionaryResult(t, p, b, c, q, tc.want, tc.args...)
				dictionaryResult(t, p, b, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}, q, tc.want, tc.args...)
			}
			var want any = "{}"
			if b.PG {
				want = nil
			}
			dictionaryResult(t, p, b, c, q, want, "estonian_stem", []byte(nil))
			dictionaryResult(t, p, b, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}, q, want, "estonian_stem", []byte(nil))
			regtsSelect(t, p, b, c, "SELECT prorettype::bigint,proisstrict FROM pg_proc WHERE oid=3723", []string{"prorettype", "proisstrict"}, []string{"INT8", "BOOL"}, [][]any{{int64(1009), true}})
			stmt.Close()
		}
		for _, prepared := range []bool{false, true} {
			s := fixture.Select("SELECT ts_lexize('estonian_stem','raamatud') AS lexemes WHERE false", []string{"lexemes"}, []string{"_TEXT"}, nil)
			s.Prepared = prepared
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
		}
	})
}
