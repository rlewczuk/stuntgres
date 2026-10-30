package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceBasqueSnowballMorphology(t *testing.T) {
	for i, count := range []int{109, 295, 19} {
		if len(basqueSuffixGoldens[i].pairs) != count {
			t.Fatalf("%s: require %d independent witnesses", basqueSuffixGoldens[i].name, count)
		}
	}
	groups := [][][2]string{
		{
			{"barrutiaren", "barru"},
			{"barrutiek", "barru"},
			{"barrutien", "barru"},
			{"barrutietako", "barru"},
			{"barrutietan", "barru"},
			{"barrutiko", "barru"},
			{"barrutik", "barrut"},
			{"barrutitan", "barrutit"},
			{"basa", "basa"},
			{"basailu", "basailu"},
			{"basalto", "basal"},
			{"basamortu", "basam"},
			{"museoak", "museo"},
			{"museoan", "museo"},
			{"museoen", "museo"},
			{"museoetan", "museo"},
			{"museoko", "museo"},
			{"museoaren", "museoaren"},
			{"music", "music"},
			{"musika", "musi"},
			{"musikagile", "musika"},
			{"musikagilea", "musi"},
			{"musikagileak", "musi"},
			{"musikagilearen", "musi"},
		},
		{
			{"etxe", "etxe"},
			{"etxeak", "etxe"},
			// Longest noun eko starts before RV; failure cannot fall back to ko.
			{"etxeko", "etxeko"},
			{"etxeetan", "etxe"},
			{"batzu", "batzu"},
			{"babtzu", "bab"},
			{"baben", "bab"},
			{"ben", "ben"},
			{"babgarri", "babgarri"},
			{"bababgarri", "babab"},
			{"babtza", "babtza"},
			{"bababtza", "babab"},
			{"bababatuatu", "babab"},
			{"joka", "jok"},
			{"trako", "tra"},
			{"minutuko", "minutu"},
			{"zlea", "z"},
			{"atseden", "atseden"},
			{"arabera", "arabera"},
			{"baditu", "baditu"},
			{"zehar", "zehar"},
			{"geldi", "geldi"},
			{"igaro", "igaro"},
			// Verb ka is in RV and runs before the noun aurka protection.
			{"aurka", "aur"},
			{"bababatuatseden", "bababatseden"},
			{"bababminutuko", "bababminutu"},
			// Independently source-traced cross-routine cursor witnesses; root resolves
			// these new literals on pinned PostgreSQL, not from native provider output.
			{"bababroatseden", "bababatseden"},
			{"bababikzehar", "bababzehar"},
			// Source-derived adjective-once witness: rik deletes, leaving ik unvisited.
			{"bababikrik", "bababik"},
		},
	}
	for _, table := range basqueSuffixGoldens {
		// Bound each serial fixture without extending the shared 60-second deadline.
		for start := 0; start < len(table.pairs); start += 32 {
			end := start + 32
			if end > len(table.pairs) {
				end = len(table.pairs)
			}
			groups = append(groups, table.pairs[start:end])
		}
	}
	for i, group := range groups {
		t.Run(string(rune('A'+i)), func(t *testing.T) {
			dictionaryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				regtsExec(t, p, b, c, "CREATE TEXT SEARCH DICTIONARY basque_rules (TEMPLATE=pg_catalog.snowball,Language='BaSqUe')", 0)
				for _, name := range []string{"basque_rules", "pg_catalog.basque_stem"} {
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

func TestReferenceBasqueSnowballBoundaries(t *testing.T) {
	dictionaryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		q := "SELECT ts_lexize('basque_stem',$1::text) AS lexemes"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		for _, tc := range [][2]string{
			{"eta", "eta"}, {"da", "da"}, {"THE", "the"}, {"ONLY", "only"}, {"RATS", "rats"},
			{"ETXEAK", "etxe"}, {"CAFÉS", "cafÉs"}, {"café", "café"}, {"cafés", "cafés"}, {"cafe\u0301", "cafe\u0301"},
			{"etxeak!", "etxeak!"}, {"etxeak\u0301", "etxeak\u0301"},
			{"a", "a"}, {"bb", "bb"}, {"bbb", "bbb"}, {"aaaa", "aaaa"},
			{"bababño", "babab"}, {"bababñi", "babab"},
			{"abatu", "aba"}, {"aabatu", "aab"}, {"bbatu", "bba"}, {"babatu", "bab"},
			{"bab" + strings.Repeat("a", 995) + "ak", "bab" + strings.Repeat("a", 995)},
			{"BAB" + strings.Repeat("A", 996) + "ak", "bab" + strings.Repeat("a", 996) + "ak"},
			{"bab" + strings.Repeat("ñ", 497) + "aak", "bab" + strings.Repeat("ñ", 497) + "a"},
			{"BAB" + strings.Repeat("ñ", 498) + "ak", "bab" + strings.Repeat("ñ", 498) + "ak"},
		} {
			for _, e := range []fixture.Executor{c, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}} {
				dictionaryResult(t, p, b, e, q, "{"+tc[1]+"}", tc[0])
			}
		}
	})
}

func TestReferenceBasqueSnowballMetadata(t *testing.T) {
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
				{[]any{"basque_stem", "etxeak"}, "{etxe}"}, {[]any{[]byte("basque_stem"), []byte("ONLY")}, "{only}"}, {[]any{"basque_stem", ""}, "{}"}, {[]any{"basque_stem", []byte{}}, "{}"}, {[]any{"basque_stem", nil}, nil}, {[]any{nil, "etxeak"}, nil},
			} {
				dictionaryResult(t, p, b, c, q, tc.want, tc.args...)
				dictionaryResult(t, p, b, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}, q, tc.want, tc.args...)
			}
			var want any = "{}"
			if b.PG {
				want = nil
			}
			dictionaryResult(t, p, b, c, q, want, "basque_stem", []byte(nil))
			dictionaryResult(t, p, b, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}, q, want, "basque_stem", []byte(nil))
			regtsSelect(t, p, b, c, "SELECT prorettype::bigint,proisstrict FROM pg_proc WHERE oid=3723", []string{"prorettype", "proisstrict"}, []string{"INT8", "BOOL"}, [][]any{{int64(1009), true}})
			stmt.Close()
		}
		for _, prepared := range []bool{false, true} {
			s := fixture.Select("SELECT ts_lexize('basque_stem','etxeak') AS lexemes WHERE false", []string{"lexemes"}, []string{"_TEXT"}, nil)
			s.Prepared = prepared
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
		}
	})
}
