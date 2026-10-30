package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

// Literal pairs derived from REL_18_6's pinned Irish algorithm and the
// independently authored Snowball examples, never from native provider tables.
func TestReferenceIrishSnowballMorphology(t *testing.T) {
	groups := map[string][][2]string{
		"initial": {
			{"h-babab", "babab"}, {"n-babab", "babab"}, {"t-babab", "babab"},
			{"d'babab", "babab"}, {"m'babab", "babab"}, {"b'babab", "babab"},
			{"d'fhbabab", "fbabab"}, {"shbabab", "sbabab"}, {"tsbabab", "sbabab"},
			{"mbbabab", "bbabab"}, {"bhbabab", "bbabab"}, {"gcbabab", "cbabab"},
			{"chbabab", "cbabab"}, {"ndbabab", "dbabab"}, {"dhbabab", "dbabab"},
			{"bhfbabab", "fbabab"}, {"fhbabab", "fbabab"}, {"ngbabab", "gbabab"},
			{"ghbabab", "gbabab"}, {"bpbabab", "pbabab"}, {"phbabab", "pbabab"},
			{"dtbabab", "tbabab"}, {"thbabab", "tbabab"}, {"mhbabab", "mbabab"},
			{"bhbhbabab", "bbhbabab"},
		},
		"noun": {
			{"bababamh", "babab"}, {"bababeamh", "babab"}, {"babababh", "babab"}, {"bababeabh", "babab"},
			{"bababaibh", "babab"}, {"bababibh", "babab"}, {"bababaimh", "babab"}, {"bababimh", "babab"},
			{"bababaíocht", "babab"}, {"bababíocht", "babab"}, {"bababaíochta", "babab"}, {"bababíochta", "babab"},
			{"bababire", "babab"}, {"bababirí", "babab"}, {"bababaire", "babab"}, {"bababairí", "babab"},
		},
		"derivation": {
			{"bababacht", "babab"}, {"bababeacht", "babab"}, {"bababach", "babab"}, {"bababeach", "babab"},
			{"bababeachtúil", "babab"}, {"bababeachta", "babab"}, {"bababachtúil", "babab"}, {"bababachta", "babab"},
			{"bababarcacht", "bababarc"},
			{"bababarcachtaí", "bababarc"},
			{"bababarcachta", "bababarc"},
			{"bababarc", "bababarc"},
			{"bababgineach", "bababgin"}, {"bababgineas", "bababgin"}, {"bababginis", "bababgin"},
			{"bababgrafaíoch", "bababgraf"}, {"bababgrafaíocht", "bababgraf"}, {"bababgrafaíochta", "bababgraf"}, {"bababgrafaíochtaí", "bababgraf"},
			{"bababpaite", "bababpaite"}, {"bababpatach", "bababpaite"}, {"bababpataigh", "bababpaite"}, {"bababpatacha", "bababpaite"},
			{"bababóideach", "bababóid"}, {"bababóideacha", "bababóid"}, {"bababóidigh", "bababóid"},
		},
		"verb": {
			{"bababimid", "babab"}, {"bababaimid", "babab"}, {"bababímid", "babab"}, {"bababaímid", "babab"},
			{"bababfaidh", "babab"}, {"bababfidh", "babab"}, {"bababain", "babab"}, {"bababeadh", "babab"},
			{"bababadh", "babab"}, {"bababáil", "babab"}, {"bababtear", "babab"}, {"bababtar", "babab"},
		},
		"natural": {
			{"siopadóireacht", "siopadóir"}, {"poblacht", "poblacht"}, {"monarcacht", "monarc"},
			{"abhcóideacht", "abhcóid"}, {"abhcóidíocht", "abhcóid"}, {"abhcóidíochta", "abhcóid"},
			{"abheadh", "abh"}, {"abhlainneach", "abhlainn"}, {"abhlaireacht", "abhlair"},
			{"pábháil", "pábh"}, {"pacáil", "pac"}, {"pacáiltear", "pacáil"}, {"pacaireachta", "pacair"},
			{"pacáistíocht", "pacáist"}, {"pacáistíochta", "pacáist"}, {"pacáistítear", "pacáistí"},
			{"abairteach", "abairt"}, {"abairteachaibh", "abairt"}, {"abairtear", "abair"},
			{"ábaltacht", "ábalt"}, {"abaracht", "abar"}, {"abartar", "abar"}, {"abdómain", "abdóm"}, {"abhaile", "abhaile"},
		},
	}
	for _, name := range []string{"initial", "noun", "derivation", "verb", "natural"} {
		t.Run(name, func(t *testing.T) { irishCorpus(t, groups[name]) })
	}
}

func irishCorpus(t *testing.T, pairs [][2]string) {
	t.Helper()
	dictionaryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		const q = "SELECT ts_lexize($1::regdictionary,$2::text) AS lexemes"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		for _, pair := range pairs {
			// PostgreSQL array output quotes tokens containing whitespace.
			want := "{" + pair[1] + "}"
			if strings.ContainsAny(pair[1], " \t") {
				want = "{\"" + pair[1] + "\"}"
			}
			dictionaryResult(t, p, b, c, q, want, "irish_stem", pair[0])
			for range 2 {
				dictionaryResult(t, p, b, held, q, want, "irish_stem", pair[0])
			}
		}
	})
}

func TestReferenceIrishSnowballBoundaries(t *testing.T) {
	irishCorpus(t, [][2]string{
		{"aimid", "aimid"}, {"aímid", "aímid"}, {"afaidh", "a"}, {"faidh", "faidh"}, {"aire", "aire"},
		{"bcdfgh", "bcdfgh"}, {"y", "y"}, {"xyzacht", "xyzacht"}, {"them", "tem"},
		{"bababachtamh", "babab"}, {"bababtaríocht", "babab"}, {"mbababaire", "babab"},
		{"RATS", "rats"}, {"RUNNING", "running"}, {"ONLY", "only"}, {"THE", "te"},
		{"n-athair", "athair"}, {"nAthair", "nathair"}, {"d'fhág", "fág"}, {"d’fhág", "d’fhág"},
		{"ÁIL", "Áil"}, {"áil", "áil"}, {"café", "café"}, {"cafés", "cafés"}, {"ca\u0301fe", "ca\u0301fe"},
		{"h-", "h-"}, {"d'", "d'"}, {"m'", "m'"}, {"b'", "b'"},
		{"RATS!", "rats!"}, {"A B", "a b"},
		{"h-" + strings.Repeat("a", 998), strings.Repeat("a", 998)},
		{"H-" + strings.Repeat("A", 999), "h-" + strings.Repeat("a", 999)},
		{"h-" + strings.Repeat("á", 499), strings.Repeat("á", 499)},
		{"h-" + strings.Repeat("á", 499) + "a", "h-" + strings.Repeat("á", 499) + "a"},
	})
}

func TestReferenceIrishSnowballStopwords(t *testing.T) {
	t.Run("english127", irishEnglishStoplist)
	irishCorpus(t, [][2]string{{"THE", "te"}, {"ONLY", "only"}, {"agus", "agus"}, {"RATS", "rats"}})
}
