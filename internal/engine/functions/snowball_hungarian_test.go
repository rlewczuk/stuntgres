package functions

import (
	"testing"
	"unicode/utf8"
)

// Literal release corpus, independently specified and corrected by the pinned
// PostgreSQL oracle; no expectations come from the production rule tables.
var hungarianUnitMorphology = [][2]string{
	{"házban", "ház"},
	{"házben", "ház"},
	{"házba", "ház"},
	{"házbe", "ház"},
	{"házra", "ház"},
	{"házre", "ház"},
	{"háznak", "ház"},
	{"háznek", "ház"},
	{"házval", "ház"},
	{"házvel", "ház"},
	{"háztól", "ház"},
	{"háztől", "ház"},
	{"házról", "ház"},
	{"házről", "ház"},
	{"házból", "ház"},
	{"házből", "ház"},
	{"házhoz", "ház"},
	{"házhez", "ház"},
	{"házhöz", "ház"},
	{"háznál", "ház"},
	{"háznél", "ház"},
	{"házig", "ház"},
	{"házat", "ház"},
	{"házet", "ház"},
	{"házot", "ház"},
	{"házöt", "ház"},
	{"házért", "ház"},
	{"házképp", "ház"},
	{"házképpen", "ház"},
	{"házkor", "ház"},
	{"házul", "ház"},
	{"házül", "ház"},
	{"házvá", "ház"},
	{"házvé", "ház"},
	{"házonként", "ház"},
	{"házenként", "ház"},
	{"házanként", "ház"},
	{"házként", "ház"},
	{"házen", "ház"},
	{"házon", "ház"},
	{"házan", "ház"},
	{"házön", "ház"},
	{"házn", "ház"},
	{"házt", "ház"},
	{"házoké", "ház"},
	{"házöké", "ház"},
	{"házaké", "ház"},
	{"házeké", "ház"},
	{"házéké", "ház"},
	{"házáké", "ház"},
	{"házké", "ház"},
	{"házééi", "ház"},
	{"házáéi", "ház"},
	{"házéi", "ház"},
	{"házéé", "ház"},
	{"házé", "ház"},
	{"házünk", "ház"},
	{"házunk", "ház"},
	{"házánk", "háza"},
	{"házénk", "háze"},
	{"háznk", "ház"},
	{"házájuk", "háza"},
	{"házéjük", "háze"},
	{"házjuk", "ház"},
	{"házjük", "ház"},
	{"házuk", "ház"},
	{"házük", "ház"},
	{"házem", "ház"},
	{"házom", "ház"},
	{"házam", "ház"},
	{"házám", "háza"},
	{"házém", "háze"},
	{"házm", "ház"},
	{"házod", "ház"},
	{"házed", "ház"},
	{"házad", "ház"},
	{"házöd", "ház"},
	{"házád", "háza"},
	{"házéd", "háze"},
	{"házd", "ház"},
	{"házja", "ház"},
	{"házje", "ház"},
	{"háza", "ház"},
	{"háze", "ház"},
	{"házo", "ház"},
	{"házá", "háza"},
	{"házé", "ház"},
	{"házjaim", "ház"},
	{"házjeim", "ház"},
	{"házáim", "háza"},
	{"házéim", "háze"},
	{"házaim", "ház"},
	{"házeim", "ház"},
	{"házim", "ház"},
	{"házjaid", "ház"},
	{"házjeid", "ház"},
	{"házáid", "háza"},
	{"házéid", "háze"},
	{"házaid", "ház"},
	{"házeid", "ház"},
	{"házid", "ház"},
	{"házjai", "ház"},
	{"házjei", "ház"},
	{"házái", "háza"},
	{"házéi", "ház"},
	{"házai", "ház"},
	{"házei", "ház"},
	{"házi", "ház"},
	{"házjaink", "ház"},
	{"házjeink", "ház"},
	{"házeink", "ház"},
	{"házaink", "ház"},
	{"házáink", "háza"},
	{"házéink", "háze"},
	{"házink", "ház"},
	{"házjaitok", "ház"},
	{"házjeitek", "ház"},
	{"házaitok", "ház"},
	{"házeitek", "ház"},
	{"házáitok", "háza"},
	{"házéitek", "háze"},
	{"házitek", "ház"},
	{"házjeik", "ház"},
	{"házjaik", "ház"},
	{"házaik", "ház"},
	{"házeik", "ház"},
	{"házáik", "háza"},
	{"házéik", "háze"},
	{"házik", "ház"},
	{"házák", "háza"},
	{"házék", "háze"},
	{"házök", "ház"},
	{"házak", "ház"},
	{"házok", "ház"},
	{"házek", "ház"},
	{"házk", "ház"},
	// Full pipeline: instrumental exposes n/t to case, d/m to singular-owner,
	// and k to plural. Factive runs after case, so its exposed n/t survive.
	// All other double units retain their undoubled final consonant unit.
	{"házbbal", "házb"},
	{"házbbel", "házb"},
	{"házbbá", "házb"},
	{"házbbé", "házb"},
	{"házccal", "házc"},
	{"házccel", "házc"},
	{"házccá", "házc"},
	{"házccé", "házc"},
	{"házccsal", "házcs"},
	{"házccsel", "házcs"},
	{"házccsá", "házcs"},
	{"házccsé", "házcs"},
	{"házddal", "ház"},
	{"házddel", "ház"},
	{"házddá", "ház"},
	{"házddé", "ház"},
	{"házffal", "házf"},
	{"házffel", "házf"},
	{"házffá", "házf"},
	{"házffé", "házf"},
	{"házggal", "házg"},
	{"házggel", "házg"},
	{"házggá", "házg"},
	{"házggé", "házg"},
	{"házggyal", "házgy"},
	{"házggyel", "házgy"},
	{"házggyá", "házgy"},
	{"házggyé", "házgy"},
	{"házjjal", "házj"},
	{"házjjel", "házj"},
	{"házjjá", "házj"},
	{"házjjé", "házj"},
	{"házkkal", "ház"},
	{"házkkel", "ház"},
	{"házkká", "ház"},
	{"házkké", "ház"},
	{"házllal", "házl"},
	{"házllel", "házl"},
	{"házllá", "házl"},
	{"házllé", "házl"},
	{"házllyal", "házly"},
	{"házllyel", "házly"},
	{"házllyá", "házly"},
	{"házllyé", "házly"},
	{"házmmal", "ház"},
	{"házmmel", "ház"},
	{"házmmá", "ház"},
	{"házmmé", "ház"},
	{"háznnal", "ház"},
	{"háznnel", "ház"},
	{"háznná", "házn"},
	{"háznné", "házn"},
	{"háznnyal", "házny"},
	{"háznnyel", "házny"},
	{"háznnyá", "házny"},
	{"háznnyé", "házny"},
	{"házppal", "házp"},
	{"házppel", "házp"},
	{"házppá", "házp"},
	{"házppé", "házp"},
	{"házrral", "házr"},
	{"házrrel", "házr"},
	{"házrrá", "házr"},
	{"házrré", "házr"},
	{"házssal", "házs"},
	{"házssel", "házs"},
	{"házssá", "házs"},
	{"házssé", "házs"},
	{"házsszal", "házsz"},
	{"házsszel", "házsz"},
	{"házsszá", "házsz"},
	{"házsszé", "házsz"},
	{"házttal", "ház"},
	{"házttel", "ház"},
	{"házttá", "házt"},
	{"háztté", "házt"},
	{"házttyal", "házty"},
	{"házttyel", "házty"},
	{"házttyá", "házty"},
	{"házttyé", "házty"},
	{"házvval", "házv"},
	{"házvvel", "házv"},
	{"házvvá", "házv"},
	{"házvvé", "házv"},
	{"házzzal", "házz"},
	{"házzzel", "házz"},
	{"házzzá", "házz"},
	{"házzzé", "házz"},
	{"házzzsal", "házzs"},
	{"házzzsel", "házzs"},
	{"házzzsá", "házzs"},
	{"házzzsé", "házzs"},
	{"almában", "alm"},
	{"almák", "alma"},
	{"babák", "baba"},
	{"házán", "ház"},
	{"házánként", "ház"},
	{"házzal", "ház"},
	{"házzá", "ház"},
	{"kézzel", "kéz"},
	{"kulccsal", "kulcs"},
	{"házal", "házal"},
	{"házén", "ház"},
	{"abal", "abal"},
	{"bbal", "bbal"},
	{"aá", "aá"},
	{"áá", "áá"},
	{"házastul", "házast"},
	{"házestül", "házest"},
	{"házstul", "házst"},
	{"házstül", "házst"},
	{"házástul", "házást"},
	{"házéstül", "házést"},
	{"a", "a"},
	{"á", "á"},
	{"ba", "ba"},
	{"ak", "ak"},
	{"ab", "ab"},
	{"bb", "bb"},
	{"aeiou", "aeiou"},
	{"bak", "bak"},
	{"Házak", "ház"},
	{"HÁZAK", "hÁzak"},
	{"RATS", "rats"},
	{"ONLY", "only"},
	{"babaháznak", "babaház"},
	{"babakocsi", "babakocs"},
	{"babakocsijáért", "babakocs"},
	{"babból", "bab"},
	{"babona", "babon"},
	{"babonákkal", "babona"},
	{"babérokat", "babér"},
	{"babért", "bab"},
	{"mulasztásban", "mulasztás"},
	{"mulasztással", "mulasztás"},
	{"mulasztási", "mulasztás"},
	{"mulatságos", "mulatságos"},
}

func TestSnowballHungarianRules(t *testing.T) {
	for _, tc := range hungarianUnitMorphology {
		if got := hungarianStem(germanUnitLower(tc[0])); got != tc[1] {
			t.Errorf("%q -> %q, want %q", tc[0], got, tc[1])
		}
	}
	t.Run("regions", func(t *testing.T) {
		for _, tc := range []struct {
			word  string
			runes int
		}{{"", 0}, {"a", 1}, {"ab", 2}, {"ba", 2}, {"acs", 3}, {"adzs", 4}, {"aegy", 4}, {"aeiou", 5}, {"ács", 3}, {"ágy", 3}, {"ány", 3}, {"ály", 3}, {"ász", 3}, {"áty", 3}, {"ázs", 3}, {"Ábak", 3}} {
			if got := utf8.RuneCountInString(tc.word[:hungarianRegion(tc.word)]); got != tc.runes {
				t.Error(tc, got)
			}
		}
	})
	t.Run("private-other-case", func(t *testing.T) {
		for _, tc := range [][2]string{{"házastul", "ház"}, {"házestül", "ház"}, {"házstul", "ház"}, {"házstül", "ház"}, {"házástul", "háza"}, {"házéstül", "háze"}} {
			word := hungarianWord{prefix: tc[0]}
			word.apply(3, hungarianOther[:])
			if got := word.text(); got != tc[1] {
				t.Error(tc, got)
			}
			word = hungarianWord{prefix: tc[0]}
			word.apply(len(tc[0]), hungarianOther[:])
			if got := word.text(); got != tc[0] {
				t.Error("region rejection", tc, got)
			}
		}
	})
	t.Run("double-units", func(t *testing.T) {
		for _, tc := range [][2]string{{"bb", "b"}, {"cc", "c"}, {"ccs", "cs"}, {"dd", "d"}, {"ff", "f"}, {"gg", "g"}, {"ggy", "gy"}, {"jj", "j"}, {"kk", "k"}, {"ll", "l"}, {"lly", "ly"}, {"mm", "m"}, {"nn", "n"}, {"nny", "ny"}, {"pp", "p"}, {"rr", "r"}, {"ss", "s"}, {"ssz", "sz"}, {"tt", "t"}, {"tty", "ty"}, {"vv", "v"}, {"zz", "z"}, {"zzs", "zs"}} {
			word := hungarianWord{prefix: tc[0]}
			if !word.double() {
				t.Error("missing double", tc)
			}
			word.undouble()
			if word.text() != tc[1] {
				t.Error(tc, word.text())
			}
		}
		for _, s := range []string{"", "b", "cs", "gy", "ny", "á", "ház"} {
			if (hungarianWord{prefix: s}).double() {
				t.Error("false double", s)
			}
		}
	})
	t.Run("longest-before-region", func(t *testing.T) {
		for _, tc := range []struct {
			word  string
			rules []hungarianRule
		}{
			{"bak", hungarianPlural[:]}, {"bam", hungarianSingular[:]},
			{"baké", hungarianOwned[:]}, {"baim", hungarianPluralOwner[:]},
			{"bastul", hungarianOther[:]},
		} {
			word := hungarianWord{prefix: tc.word}
			word.apply(2, tc.rules)
			if got := word.text(); got != tc.word {
				t.Error("shorter suffix retried", tc.word, got)
			}
		}
		if got := hungarianStem("bat"); got != "bat" {
			t.Error("case retried shorter t", got)
		}
		for _, tc := range []struct {
			word, endings string
			p1            int
		}{{"bbal", "al el", 3}, {"bbá", "á é", 3}, {"házal", "al el", 3}, {"házá", "á é", 3}} {
			word := hungarianWord{prefix: tc.word}
			word.assimilation(tc.p1, tc.endings)
			if got := word.text(); got != tc.word {
				t.Error("failed double/region edited word", tc, got)
			}
		}
	})
	t.Run("replacement-region-and-tail", func(t *testing.T) {
		word := hungarianWord{prefix: "házá"}
		word.replace(4, "á", "a")
		if word.text() != "háza" {
			t.Fatal(word.text())
		}
		word.apply(4, hungarianSingular[:])
		if word.text() != "ház" || word.tail != "" {
			t.Fatal("later stage did not erase replacement", word)
		}
		word = hungarianWord{prefix: "házá"}
		word.replace(5, "á", "a")
		if word.text() != "házá" {
			t.Fatal("outside-region accent shortened", word)
		}
		word = hungarianWord{prefix: "kulccs"}
		word.undouble()
		if word.text() != "kulcs" || !word.hasSuffix("cs") || !word.hasSuffix("kulcs") || word.hasSuffix("ccs") {
			t.Fatal("split-tail suffix", word)
		}
	})
}
