package functions

import (
	"strings"
	"testing"
)

// Intermediate results come from the released SBL actions, before later stages
// obscure which selected condition succeeded. Artificial limits isolate edges.
func TestSnowballFinnishStages(t *testing.T) {
	check := func(stage, input, got, want string) {
		t.Helper()
		if got != want {
			t.Errorf("%s %q -> %q, want %q", stage, input, got, want)
		}
	}
	for _, suffix := range []string{"kin", "kaan", "kään", "ko", "kö", "han", "hän", "pa", "pä", "sti"} {
		for _, prefix := range []string{"bababu", "bababn", "bababt"} {
			word := prefix + suffix
			check("particle at limit", word, string(finnishParticle([]rune(word), 6, 6)), prefix)
			check("particle before limit", word, string(finnishParticle([]rune(word), 7, 7)), word)
		}
		if suffix != "sti" {
			word := "bababx" + suffix
			check("particle predecessor", word, string(finnishParticle([]rune(word), 3, 5)), word)
		}
	}
	check("one particle", "talohankin", string(finnishParticle([]rune("talohankin"), 3, 5)), "talohan")
	for _, suffix := range []string{"si", "ni", "nsa", "nsä", "mme", "nne"} {
		word := "bababu" + suffix
		check("possessive at limit", word, string(finnishPossessive([]rune(word), 6)), "bababu")
		check("possessive before limit", word, string(finnishPossessive([]rune(word), 7)), word)
	}
	for _, tc := range [][2]string{{"babaksi", "babaksi"}, {"babakseni", "babaksi"}, {"bababuqaan", "bababuqaan"}, {"bababuqään", "bababuqään"}, {"bababuqeen", "bababuqeen"}} {
		check("possessive action", tc[0], string(finnishPossessive([]rune(tc[0]), 3)), tc[1])
	}
	for _, group := range []struct{ ending, contexts string }{
		{"an", "ta ssa sta lla lta na"}, {"än", "tä ssä stä llä ltä nä"}, {"en", "lle ine"},
	} {
		for _, context := range strings.Fields(group.contexts) {
			prefix := "bababu" + context
			word := prefix + group.ending
			limit := len([]rune(prefix))
			check("Vn possessive", word, string(finnishPossessive([]rune(word), limit)), prefix)
			check("Vn region", word, string(finnishPossessive([]rune(word), limit+1)), word)
			word = "bababuq" + group.ending
			check("Vn wrong predecessor", word, string(finnishPossessive([]rune(word), 3)), word)
		}
	}
	// All thirty case entries, with positive actions and selection boundaries.
	for _, tc := range []struct{ suffix, prefix, want string }{
		{"han", "bababa", "bababa"}, {"hen", "bababe", "bababe"}, {"hin", "bababi", "bababi"}, {"hon", "bababo", "bababo"}, {"hän", "bababä", "bababä"}, {"hön", "bababö", "bababö"},
		{"den", "bababai", "bababai"}, {"tten", "bababai", "bababai"}, {"siin", "bababai", "bababai"}, {"seen", "bababaa", "bababaa"},
		{"a", "bababu", "bababu"}, {"ä", "bababu", "bababu"}, {"tta", "bababe", "bababe"}, {"ttä", "bababe", "bababe"},
		{"ta", "bababu", "bababu"}, {"tä", "bababu", "bababu"}, {"ssa", "bababu", "bababu"}, {"ssä", "bababu", "bababu"}, {"sta", "bababu", "bababu"}, {"stä", "bababu", "bababu"},
		{"lla", "bababu", "bababu"}, {"llä", "bababu", "bababu"}, {"lta", "bababu", "bababu"}, {"ltä", "bababu", "bababu"}, {"lle", "bababu", "bababu"}, {"na", "bababu", "bababu"}, {"nä", "bababu", "bababu"}, {"ksi", "bababu", "bababu"}, {"ine", "bababu", "bababu"}, {"n", "bababu", "bababu"},
	} {
		word := tc.prefix + tc.suffix
		got, removed := finnishCase([]rune(word), 3)
		check("case action", word, string(got), tc.want)
		if !removed {
			t.Error("case removed flag", word)
		}
		got, removed = finnishCase([]rune(word), len([]rune(word)))
		check("case outside region", word, string(got), word)
		if removed {
			t.Error("case rejected flag", word)
		}
		// Predicate-bearing among entries require their lookbehind in R1;
		// other action lookbehind is deliberately outside the region limit.
		if tc.suffix != "den" && tc.suffix != "tten" && tc.suffix != "siin" && tc.suffix != "seen" {
			got, removed = finnishCase([]rune(word), len([]rune(tc.prefix)))
			check("case exact limit", word, string(got), tc.want)
			if !removed {
				t.Error("case exact limit flag", word)
			}
		}
	}
	for _, tc := range []struct {
		word, want string
		removed    bool
	}{
		{"babxhan", "babxhan", false}, {"babxhen", "babxhen", false}, {"babxhin", "babxhin", false}, {"babxhon", "babxhon", false}, {"babxhän", "babxhän", false}, {"babøhön", "babøhön", false},
		{"babautta", "babautta", false}, {"babauttä", "babauttä", false}, {"bababba", "bababba", false}, {"babauua", "babauua", false},
		{"babaaden", "babaade", true}, {"babayiden", "babayide", true}, {"babayyseen", "babayyse", true}, {"babaaseen", "babaa", true}, {"babaiden", "babai", true}, {"babaain", "babaai", true}, {"babaaan", "babaa", true}, {"babaien", "babai", true},
	} {
		got, removed := finnishCase([]rune(tc.word), 3)
		check("case condition", tc.word, string(got), tc.want)
		if removed != tc.removed {
			t.Error(tc, removed)
		}
	}
	for _, suffix := range []string{"mpi", "mpa", "mpä", "mmi", "mma", "mmä", "impi", "impa", "impä", "immi", "imma", "immä", "eja", "ejä"} {
		word := "bababu" + suffix
		check("other exact limit", word, string(finnishOther([]rune(word), 6)), "bababu")
		// Longer suffixes can select their shorter among entry if it lies
		// within R2, just as generated backward selection does.
		check("other excluded", word, string(finnishOther([]rune(word), len([]rune(word)))), word)
		word = "bababupo" + suffix
		want := "bababupo"
		if !strings.HasPrefix(suffix, "i") && suffix != "eja" && suffix != "ejä" {
			want = word
		}
		check("other po", word, string(finnishOther([]rune(word), 5)), want)
	}
	for _, tc := range []struct {
		word, want string
		removed    bool
	}{
		{"bababui", "bababu", true}, {"bababuj", "bababu", true}, {"bababut", "bababut", true},
		{"bababui", "bababui", false}, {"bababut", "bababu", false}, {"bababxt", "bababxt", false},
		{"bababummat", "bababu", false}, {"bababuimmat", "bababu", false}, {"bababupommat", "bababupomma", false},
	} {
		check("plural route", tc.word, string(finnishPlural([]rune(tc.word), 3, 5, tc.removed)), tc.want)
	}
	check("plural predecessor boundary", "babut", string(finnishPlural([]rune("babut"), 4, 5, false)), "babut")
	for _, tc := range [][2]string{
		{"bababaa", "babab"}, {"bababee", "babab"}, {"bababii", "babab"}, {"bababoo", "bababo"}, {"bababuu", "bababu"}, {"bababää", "babab"}, {"bababöö", "bababö"}, {"bababyy", "bababyy"},
		{"bababa", "babab"}, {"bababä", "babab"}, {"bababe", "babab"}, {"bababi", "babab"}, {"bababé", "bababé"},
		{"bababoj", "bababo"}, {"bababuj", "bababu"}, {"bababjo", "bababj"}, {"babbb", "babb"}, {"babbuu", "babu"}, {"babéé", "babéé"},
	} {
		check("tidy", tc[0], string(finnishTidy([]rune(tc[0]), 3)), tc[1])
	}
	check("undouble outside R1", "bb", string(finnishTidy([]rune("bb"), 2)), "b")
	check("tidy predecessor boundary", "baba", string(finnishTidy([]rune("baba"), 3)), "baba")
	check("tidy exact predecessor", "baba", string(finnishTidy([]rune("baba"), 2)), "bab")
}
