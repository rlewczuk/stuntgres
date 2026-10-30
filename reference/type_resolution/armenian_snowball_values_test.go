package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

// These fixed examples trace REL_18_6's Armenian kernel and C-locale adapter,
// independently of the implementation. The table corpus is in the companion file.
func TestReferenceArmenianSnowballMorphology(t *testing.T) {
	for i, count := range []int{23, 71, 40, 57} {
		if len(armenianSuffixGoldens[i].pairs) != count {
			t.Fatalf("%s: require %d independently specified witnesses", armenianSuffixGoldens[i].name, count)
		}
	}
	groups := [][][2]string{
		{{"աղոթում", "աղոթ"}, {"աղոթք", "աղոթ"}, {"աղոթքը", "աղոթ"}, {"աղոթքի", "աղոթ"}, {"աղոթքին", "աղոթ"}, {"աղոթքից", "աղոթ"}, {"աղոթքն", "աղոթ"}, {"աղոթքներ", "աղոթ"}, {"աղոթքները", "աղոթ"}, {"աղոթքների", "աղոթ"}, {"աղոթքներին", "աղոթ"}},
		{{"բանաձևեր", "բանաձև"}, {"բանաձևերը", "բանաձև"}, {"բանաձևերի", "բանաձև"}, {"բանաձևերից", "բանաձև"}, {"բանաձևերն", "բանաձև"}, {"բանաձևերով", "բանաձև"}, {"բանաձևերում", "բանաձև"}, {"բանաձևը", "բանաձև"}, {"բանաձևի", "բանաձև"}, {"բանաձևին", "բանաձև"}, {"բանաձևից", "բանաձև"}},
		{{"բաբել", "բաբ"}, {"բաբպես", "բաբ"}, {"բաբիչ", "բաբ"}, {"բաբիչպեսել", "բաբ"}, {"բաբելել", "բաբել"}, {"բաբաբըը", "բաբաբը"}, {"բաբը", "բաբը"}, {"բաբաբը", "բաբաբ"}, {"բաբներ", "բաբներ"}, {"բաբաբներ", "բաբաբ"}, {"բաբաբներով", "բաբաբ"}, {"բաբաբների", "բաբաբ"}, {"բել", "բել"}, {"աբել", "աբ"}},
	}
	for _, table := range armenianSuffixGoldens {
		group := make([][2]string, 0, len(table.pairs))
		for _, pair := range table.pairs {
			group = append(group, [2]string{"բաբաբ" + pair[0], pair[1]})
		}
		groups = append(groups, group)
	}
	for i, group := range groups {
		t.Run(string(rune('A'+i)), func(t *testing.T) {
			dictionaryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				regtsExec(t, p, b, c, "CREATE TEXT SEARCH DICTIONARY armenian_rules (TEMPLATE=pg_catalog.snowball,Language='ArMeNiAn')", 0)
				for _, name := range []string{"armenian_rules", "pg_catalog.armenian_stem"} {
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

func TestReferenceArmenianSnowballBoundaries(t *testing.T) {
	dictionaryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		q := "SELECT ts_lexize('armenian_stem',$1::text) AS lexemes"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		for _, tc := range [][2]string{
			{"բք", "բք"}, {"բևք", "բևք"},
			{"բաք", "բա"}, {"բէք", "բէ"}, {"բիք", "բի"}, {"բօք", "բօ"}, {"բւք", "բւ"}, {"բեք", "բե"}, {"բոք", "բո"}, {"բըք", "բը"},
			{"աղոթք!", "աղոթք!"}, {"աղոթք\u0301", "աղոթք\u0301"}, {"ԱՂՈԹՔԸ", "ԱՂՈԹՔԸ"}, {"աղոթՔը", "աղոթՔ"},
			{"RATS", "rats"}, {"ONLY", "only"}, {"CAFÉS", "cafÉs"}, {"café", "café"}, {"cafe\u0301", "cafe\u0301"}, {"և", "և"}, {"է", "է"}, {"ու", "ու"},
			{strings.Repeat("բ", 497) + "աիչ", strings.Repeat("բ", 497) + "ա"},
			{strings.Repeat("բ", 498) + "աիչ", strings.Repeat("բ", 498) + "աիչ"},
			{"A" + strings.Repeat("բ", 497) + "աիչ", "a" + strings.Repeat("բ", 497) + "աիչ"},
			{"A" + strings.Repeat("բ", 496) + "աիչ", "a" + strings.Repeat("բ", 496) + "ա"},
		} {
			for _, e := range []fixture.Executor{c, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}} {
				dictionaryResult(t, p, b, e, q, "{"+tc[1]+"}", tc[0])
			}
		}
	})
}

func TestReferenceArmenianSnowballMetadata(t *testing.T) {
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
				{[]any{"armenian_stem", "աղոթքը"}, "{աղոթ}"}, {[]any{[]byte("armenian_stem"), []byte("ONLY")}, "{only}"}, {[]any{"armenian_stem", ""}, "{}"}, {[]any{"armenian_stem", []byte{}}, "{}"}, {[]any{"armenian_stem", nil}, nil}, {[]any{nil, "աղոթքը"}, nil},
			} {
				dictionaryResult(t, p, b, c, q, tc.want, tc.args...)
				dictionaryResult(t, p, b, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}, q, tc.want, tc.args...)
			}
			var want any = "{}"
			if b.PG {
				want = nil
			}
			dictionaryResult(t, p, b, c, q, want, "armenian_stem", []byte(nil))
			dictionaryResult(t, p, b, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}, q, want, "armenian_stem", []byte(nil))
			regtsSelect(t, p, b, c, "SELECT prorettype::bigint,proisstrict FROM pg_proc WHERE oid=3723", []string{"prorettype", "proisstrict"}, []string{"INT8", "BOOL"}, [][]any{{int64(1009), true}})
			stmt.Close()
		}
		for _, prepared := range []bool{false, true} {
			s := fixture.Select("SELECT ts_lexize('armenian_stem','աղոթքը') AS lexemes WHERE false", []string{"lexemes"}, []string{"_TEXT"}, nil)
			s.Prepared = prepared
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
		}
	})
}
