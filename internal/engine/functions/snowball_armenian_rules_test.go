package functions

import (
	"slices"
	"strings"
	"testing"
)

// Independently transcribed REL_18_6 finite rule witnesses. These literals are
// not read from the production tables; full-pipeline goldens also live in the
// public reference corpus, including interactions between these phases.
func TestSnowballArmenianRules(t *testing.T) {
	for _, family := range []struct {
		name    string
		actual  []string
		literal string
		count   int
	}{
		{"adjective", armenianAdjectives[:], "բար պես որէն ովին ակի լայն րորդ երորդ ական ալի կոտ եկեն որակ եղ վուն երեն արան են ավետ գին իվ ատ ին", 23},
		{"verb", armenianVerbs[:], "ում վում ալու ելու վել անալ ելուց ալուց ըալ ըել ալով ելով ալիս ելիս ենալ ացնալ եցնել ցնել նել ատել ոտել կոտել տել ված եցվել ացվել եցիր ացիր եցինք ացինք վեցիր վեցինք վեցիք վեցին ացրիր ացրեց ացրինք ացրիք ացրին եցիք ացիք եցին ացին ացար ացավ ացանք ացաք ացան վեցի ացրի եցար եցավ ցանք ցաք ցան ացա ացի եցա չել եցի ար ավ անք աք ան ալ ել եց աց վե ա", 71},
		{"noun", armenianNouns[:], "ածո անակ անօց արան արք պան ստան եղէն ենք իկ իչ իք մունք յակ յուն ոնք որդ ոց չեք վածք վոր ավոր ություն ուկ ուհի ույթ ույք ուստ ուս ցի ալիք անիք իլ իչք ունք գար ու ակ ան ք", 40},
		{"ending", armenianEndings[:], "ները ներն ների ներդ երից ներից երի երդ երն երը ներին ությանն ությանը ությանս ությանդ ության երին ին սա ոջ ից երով ներով երում ներում ուն ուդ վանս վանը վանդ անը անդ վան ոջը ոջս ոջդ ոց ուց ոջից ցից վից վի վով ով անով անում վանից ամբ ան ներ եր վա ը ն դ ց ի", 57},
	} {
		t.Run(family.name, func(t *testing.T) {
			witnesses := strings.Fields(family.literal)
			if len(family.actual) != family.count || len(witnesses) != family.count {
				t.Fatal("released table cardinality", len(family.actual), len(witnesses), family.count)
			}
			seen := map[string]bool{}
			for _, suffix := range witnesses {
				if seen[suffix] || !slices.Contains(family.actual, suffix) {
					t.Fatal("missing/duplicate released suffix", suffix)
				}
				seen[suffix] = true
				const prefix = "բաբաբ"
				input := prefix + suffix
				// Isolate each literal's exact guard, independently of shorter
				// among fallbacks. Byte boundaries straddle one Armenian rune.
				for _, delta := range []int{-2, 0, 2} {
					region := len(prefix) + delta
					want := prefix
					if delta > 0 {
						want = input
					}
					if got := armenianDeleteSuffix(input, []string{suffix}, region, region); got != want {
						t.Errorf("pV %q delta%d: %q want %q", suffix, delta, got, want)
					}
					if got := armenianDeleteSuffix(input, []string{suffix}, 0, region); got != want {
						t.Errorf("R2 %q delta%d: %q want %q", suffix, delta, got, want)
					}
				}
				// Use the actual complete table for longest-match deletion at
				// and immediately inside the admitted region.
				for _, region := range []int{len(prefix) - 2, len(prefix)} {
					if got := armenianDeleteSuffix(input, family.actual, region, region); got != prefix {
						t.Errorf("table %q region%d: %q", suffix, region, got)
					}
				}
			}
		})
	}
	for _, tc := range [][2]string{
		{"աղոթում", "աղոթ"}, {"աղոթք", "աղոթ"}, {"աղոթքը", "աղոթ"}, {"աղոթքի", "աղոթ"}, {"աղոթքին", "աղոթ"}, {"աղոթքից", "աղոթ"}, {"աղոթքն", "աղոթ"}, {"աղոթքներ", "աղոթ"}, {"աղոթքները", "աղոթ"}, {"աղոթքների", "աղոթ"}, {"աղոթքներին", "աղոթ"},
		{"բանաձևեր", "բանաձև"}, {"բանաձևերը", "բանաձև"}, {"բանաձևերի", "բանաձև"}, {"բանաձևերից", "բանաձև"}, {"բանաձևերն", "բանաձև"}, {"բանաձևերով", "բանաձև"}, {"բանաձևերում", "բանաձև"}, {"բանաձևը", "բանաձև"}, {"բանաձևի", "բանաձև"}, {"բանաձևին", "բանաձև"}, {"բանաձևից", "բանաձև"},
		{"բաբել", "բաբ"}, {"բաբպես", "բաբ"}, {"բաբիչ", "բաբ"}, {"բաբիչպեսել", "բաբ"}, {"բաբելել", "բաբել"}, {"բաբաբըը", "բաբաբը"}, {"բաբը", "բաբը"}, {"բաբաբը", "բաբաբ"}, {"բաբներ", "բաբներ"}, {"բաբաբներ", "բաբաբ"}, {"բաբաբներով", "բաբաբ"}, {"բաբաբների", "բաբաբ"},
		{"բել", "բել"}, {"աբել", "աբ"}, {"բք", "բք"}, {"բևք", "բևք"}, {"բաբաբացնել", "բաբաբա"}, {"բաբաբացնալ", "բաբաբ"},
		{"աղոթք!", "աղոթք!"}, {"աղոթք\u0301", "աղոթք\u0301"}, {"ԱՂՈԹՔԸ", "ԱՂՈԹՔԸ"}, {"աղոթՔը", "աղոթՔ"}, {"RATS", "RATS"}, {"և", "և"}, {"է", "է"}, {"ու", "ու"}, {"", ""},
	} {
		if got := armenianStem(tc[0]); got != tc[1] {
			t.Errorf("%q -> %q, want %q", tc[0], got, tc[1])
		}
	}
	for _, vowel := range "աէիօւեոը" {
		input := "բ" + string(vowel) + "ք"
		if got := armenianStem(input); got != "բ"+string(vowel) {
			t.Error("released vowel", input, got)
		}
	}
	for _, tc := range []struct {
		input  string
		pV, p2 int
	}{
		{"բաբաբ", 4, 10}, {"աբաբ", 2, 8}, {"բել", 4, 6}, {"բևք", 6, 6}, {"բաաաբաաբ", 4, 16}, {"", 0, 0},
	} {
		if pV, p2 := armenianRegions(tc.input); pV != tc.pV || p2 != tc.p2 {
			t.Error("original regions", tc.input, pV, p2, tc.pV, tc.p2)
		}
	}
	if got := armenianDeleteSuffix("բաբներ", armenianEndings[:], 4, 10); got != "բաբներ" {
		t.Fatal("failed longest ending R2 fell back to shorter եր", got)
	}
}
