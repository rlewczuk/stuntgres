package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

// Fixed project-owned examples, independently derived from REL_18_6's Arabic
// kernel and dsnowball_lexize. They are not a current Snowball wordlist.
func TestReferenceArabicSnowballMorphology(t *testing.T) {
	groups := [][][2]string{
		{{"كتاب", "كتاب"}, {"الكتاب", "كتاب"}, {"بالكتاب", "كتاب"}, {"للكتاب", "كتاب"}, {"وكتاب", "كتاب"}, {"فكتاب", "كتاب"}, {"مدرسة", "مدرس"}, {"المدرسة", "مدرس"}, {"المدارس", "مدارس"}},
		{{"كتبت", "كتب"}, {"كتبتما", "كتب"}, {"كتبوا", "كتب"}, {"كتبتم", "كتب"}, {"كتابه", "كتاب"}, {"كتابها", "كتاب"}, {"كتابهما", "كتاب"}, {"كتابنا", "كتاب"}, {"كتابي", "كتاب"}, {"كاتبون", "كاتب"}, {"كاتبان", "كاتب"}, {"كاتبين", "كاتب"}, {"يكتبون", "يكتب"}, {"سيكتب", "يكتب"}, {"يستعمل", "استعمل"}},
		{{"كتب", "كتب"}, {"كتي", "كت"}, {"كت", "كت"}, {"كتبا", "كتب"}, {"كبا", "كبا"}, {"كتبون", "كتبون"}, {"كتاب!", "كتاب!"}, {"الكتاب!", "كتاب!"}, {"والكتاب", "والكتاب"}, {"ال", "ال"}, {"الاب", "الاب"}, {"الكتب", "كتب"}, {"بالاب", "بالاب"}, {"بالكتب", "كتب"}},
	}
	for i, group := range groups {
		t.Run(string(rune('A'+i)), func(t *testing.T) {
			dictionaryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				regtsExec(t, p, b, c, "CREATE TEXT SEARCH DICTIONARY arabic_rules (TEMPLATE=pg_catalog.snowball,Language='ArAbIc')", 0)
				for _, name := range []string{"arabic_rules", "pg_catalog.arabic_stem"} {
					q := "SELECT ts_lexize('" + name + "'::regdictionary,$1::text) AS lexemes"
					stmt, err := c.PrepareContext(p.Ctx, q)
					if err != nil {
						t.Fatal(err)
					}
					for _, tc := range group {
						dictionaryResult(t, p, b, c, q, "{"+tc[1]+"}", tc[0])
						dictionaryResult(t, p, b, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}, q, "{"+tc[1]+"}", tc[0])
					}
					stmt.Close()
				}
			})
		})
	}
}

func TestReferenceArabicSnowballBoundaries(t *testing.T) {
	dictionaryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		q := "SELECT ts_lexize('arabic_stem',$1::text) AS lexemes"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		for _, tc := range [][2]string{
			{"كِتَابٌ", "كتاب"}, {"كـتاب", "كتاب"}, {"أمل", "امل"}, {"مسؤول", "مسوول"}, {"قارئ", "قارء"}, {"أ", "ء"}, {"إ", "ء"}, {"آ", "ء"}, {"ؤ", "ء"}, {"ئ", "ء"}, {"فتى", "فتي"}, {"على", "علي"},
			{"في", "في"}, {"من", "من"}, {"هذا", "هذا"}, {"ONLY", "only"}, {"RATS", "rats"}, {"CAFÉS", "cafÉs"}, {"١٢٣", "123"}, {"۱۲۳", "۱۲۳"}, {"کتاب", "کتاب"}, {"\ufe8d", "ا"}, {"\ufefb", "لا"}, {"\u0653", "\u0653"}, {"\u064e", "\u064e"}, {"\u0640", "\u0640"},
			// Released Prefix_Step3b_Noun removes one initial beh after ون.
			{strings.Repeat("ب", 498) + "ون", strings.Repeat("ب", 497)},
			{strings.Repeat("ب", 499) + "ون", strings.Repeat("ب", 499) + "ون"},
			{"A" + strings.Repeat("ب", 498) + "ون", "a" + strings.Repeat("ب", 498) + "ون"},
			{"A" + strings.Repeat("\u064e", 499) + "١", "a" + strings.Repeat("\u064e", 499) + "١"},
		} {
			for _, e := range []fixture.Executor{c, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}} {
				dictionaryResult(t, p, b, e, q, "{"+tc[1]+"}", tc[0])
			}
		}
	})
}

func TestReferenceArabicSnowballMetadata(t *testing.T) {
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
				{[]any{"arabic_stem", "الكتاب"}, "{كتاب}"}, {[]any{[]byte("arabic_stem"), []byte("ONLY")}, "{only}"}, {[]any{"arabic_stem", ""}, "{}"}, {[]any{"arabic_stem", []byte{}}, "{}"}, {[]any{"arabic_stem", nil}, nil}, {[]any{nil, "الكتاب"}, nil},
			} {
				dictionaryResult(t, p, b, c, q, tc.want, tc.args...)
				dictionaryResult(t, p, b, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}, q, tc.want, tc.args...)
			}
			var want any = "{}"
			if b.PG {
				want = nil
			}
			dictionaryResult(t, p, b, c, q, want, "arabic_stem", []byte(nil))
			dictionaryResult(t, p, b, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}, q, want, "arabic_stem", []byte(nil))
			regtsSelect(t, p, b, c, "SELECT prorettype::bigint,proisstrict FROM pg_proc WHERE oid=3723", []string{"prorettype", "proisstrict"}, []string{"INT8", "BOOL"}, [][]any{{int64(1009), true}})
			stmt.Close()
		}
		for _, prepared := range []bool{false, true} {
			s := fixture.Select("SELECT ts_lexize('arabic_stem','الكتاب') AS lexemes WHERE false", []string{"lexemes"}, []string{"_TEXT"}, nil)
			s.Prepared = prepared
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
		}
	})
}
