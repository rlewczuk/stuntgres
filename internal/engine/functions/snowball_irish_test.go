package functions

import "testing"

// Independent literal release corpus, also exercised through the public driver.
func TestSnowballIrishRules(t *testing.T) {
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
	for name, pairs := range groups {
		t.Run(name, func(t *testing.T) {
			for _, pair := range pairs {
				if got := irishStem(pair[0]); got != pair[1] {
					t.Errorf("%q: got %q, want %q", pair[0], got, pair[1])
				}
			}
		})
	}
	t.Run("regions", func(t *testing.T) {
		for _, tc := range []struct {
			word       string
			rv, r1, r2 int
		}{
			{"babab", 2, 3, 5}, {"bcdf", 4, 4, 4}, {"aaaa", 1, 4, 4},
			{"ábáb", 2, 3, 6}, {"a😀a😀", 1, 5, 10}, {"Ábáb", 5, 6, 6},
		} {
			rv, r1, r2 := irishRegions(tc.word)
			if rv != tc.rv || r1 != tc.r1 || r2 != tc.r2 {
				t.Errorf("%q regions: %d %d %d; want %d %d %d", tc.word, rv, r1, r2, tc.rv, tc.r1, tc.r2)
			}
		}
	})
	t.Run("boundaries-and-order", func(t *testing.T) {
		for _, pair := range [][2]string{
			{"aimid", "aimid"}, {"aímid", "aímid"}, {"afaidh", "a"}, {"faidh", "faidh"}, {"aire", "aire"},
			{"bababachtamh", "babab"}, {"bababtaríocht", "babab"}, {"mbababaire", "babab"},
			{"n-athair", "athair"}, {"d'fhág", "fág"}, {"d’fhág", "d’fhág"},
			{"áil", "áil"}, {"café", "café"}, {"cafés", "cafés"}, {"ca\u0301fe", "ca\u0301fe"},
			{"h-", ""}, {"d'", ""}, {"m'", ""}, {"b'", ""}, {"", ""},
		} {
			if got := irishStem(pair[0]); got != pair[1] {
				t.Errorf("%q: got %q, want %q", pair[0], got, pair[1])
			}
		}
	})
	irishStageBoundaries(t)
}

func irishStageBoundaries(t *testing.T) {
	t.Helper()
	// Literal suffix expectations are independent of the production tables.
	for _, stage := range []struct {
		name  string
		rules []irishRule
		pairs [][2]string
	}{
		{"noun-r1", irishNounRules[:], [][2]string{
			{"amh", ""}, {"eamh", ""}, {"abh", ""}, {"eabh", ""}, {"aibh", ""}, {"ibh", ""}, {"aimh", ""}, {"imh", ""},
			{"aíocht", ""}, {"íocht", ""}, {"aíochta", ""}, {"íochta", ""},
		}},
		{"noun-r2", irishNounRules[:], [][2]string{{"ire", ""}, {"irí", ""}, {"aire", ""}, {"airí", ""}}},
		{"deriv-r2", irishDerivationRules[:], [][2]string{
			{"acht", ""}, {"eacht", ""}, {"ach", ""}, {"each", ""}, {"eachtúil", ""}, {"eachta", ""}, {"achtúil", ""}, {"achta", ""},
		}},
		{"verb-rv", irishVerbRules[:], [][2]string{{"imid", ""}, {"aimid", ""}, {"ímid", ""}, {"aímid", ""}, {"faidh", ""}, {"fidh", ""}}},
		{"verb-r1", irishVerbRules[:], [][2]string{{"ain", ""}, {"eadh", ""}, {"adh", ""}, {"áil", ""}, {"tear", ""}, {"tar", ""}}},
	} {
		t.Run(stage.name, func(t *testing.T) {
			for _, pair := range stage.pairs {
				for _, prefix := range []string{"babab", "ábáb"} {
					word := prefix + pair[0]
					for _, delta := range []int{0, 1} {
						boundary := len(prefix) + delta
						rv, r1, r2 := 0, 0, 0
						switch stage.name {
						case "noun-r1", "verb-r1":
							r1 = boundary
						case "noun-r2", "deriv-r2":
							r2 = boundary
						case "verb-rv":
							rv = boundary
						}
						want := prefix + pair[1]
						if delta == 1 {
							want = word
						}
						if got := irishSuffix(word, stage.rules, rv, r1, r2); got != want {
							t.Errorf("%q boundary %d: got %q, want %q", word, boundary, got, want)
						}
					}
				}
			}
		})
	}
	t.Run("deriv-unconditional", func(t *testing.T) {
		for _, pair := range [][2]string{
			{"arcacht", "arc"}, {"arcachtaí", "arc"}, {"arcachta", "arc"}, {"gineach", "gin"}, {"gineas", "gin"}, {"ginis", "gin"},
			{"grafaíoch", "graf"}, {"grafaíocht", "graf"}, {"grafaíochta", "graf"}, {"grafaíochtaí", "graf"},
			{"paite", "paite"}, {"patach", "paite"}, {"pataigh", "paite"}, {"patacha", "paite"},
			{"óideach", "óid"}, {"óideacha", "óid"}, {"óidigh", "óid"},
		} {
			word := "babab" + pair[0]
			if got := irishSuffix(word, irishDerivationRules[:], len(word), len(word), len(word)); got != "babab"+pair[1] {
				t.Errorf("%q: %q", word, got)
			}
		}
	})
	if got := irishSuffix("eamh", irishNounRules[:], 0, 1, 0); got != "eamh" {
		t.Fatal("shorter noun retry", got)
	}
	if got := irishSuffix("aimid", irishVerbRules[:], 1, 0, 0); got != "aimid" {
		t.Fatal("shorter verb retry", got)
	}
}
