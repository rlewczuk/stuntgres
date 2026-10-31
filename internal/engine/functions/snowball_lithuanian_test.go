package functions

import "testing"

func TestSnowballLithuanianRules(t *testing.T) {
	t.Run("original-region", func(t *testing.T) {
		for _, tc := range []struct {
			word string
			p1 int
		}{{"", 0}, {"ababa", 2}, {"abababa", 4}, {"ababas", 2}, {"ąbaba", 3}, {"bcdf", 4}, {"aeiou", 5}, {"ébab", 5}} {
			if got := lithuanianRegion(tc.word); got != tc.p1 {
				t.Errorf("region(%q)=%d, want %d", tc.word, got, tc.p1)
			}
		}
		for _, vowel := range []string{"a", "e", "i", "y", "o", "u", "ą", "ę", "į", "ų", "ė", "ū"} {
			if got := lithuanianRegion(vowel + "ba"); got != len(vowel)+1 {
				t.Errorf("vowel %q: %d", vowel, got)
			}
		}
		for _, nonvowel := range []string{"é", "Ė", "Ą", "b", "č", "ž"} {
			if got := lithuanianRegion(nonvowel + "ba"); got != len(nonvowel)+2 {
				t.Errorf("nonvowel %q: %d", nonvowel, got)
			}
		}
	})
	t.Run("region-inside-longest", func(t *testing.T) {
		for _, tc := range []struct {
			word string
			p1 int
			want string
		}{{"bias", 1, "ias"}, {"bias", 2, "as"}, {"bias", 3, "s"}, {"bias", 4, ""}, {"bąias", 3, "ias"}, {"bąias", 4, "as"}} {
			if got := lithuanianSuffix(tc.word, tc.p1, lithuanianStep1[:]); got != tc.want {
				t.Errorf("step1(%q,%d)=%q, want %q", tc.word, tc.p1, got, tc.want)
			}
		}
		for _, tc := range []struct {
			word string
			p1 int
			want string
		}{{"biuot", 1, "iuot"}, {"biuot", 2, "uot"}, {"biuot", 3, "ot"}, {"biuot", 4, ""}, {"bąiuot", 3, "iuot"}, {"bąiuot", 4, "uot"}} {
			if got := lithuanianSuffix(tc.word, tc.p1, lithuanianStep2[:]); got != tc.want {
				t.Errorf("step2(%q,%d)=%q, want %q", tc.word, tc.p1, got, tc.want)
			}
		}
		word := "biuoting"
		for {
			suffix := lithuanianSuffix(word, 2, lithuanianStep2[:])
			if suffix == "" {
				break
			}
			word = word[:len(word)-len(suffix)]
		}
		if word != "bi" {
			t.Fatal("repeat with fixed region", word)
		}
	})
	t.Run("unrestricted-conflicts", func(t *testing.T) {
		for _, pair := range [][2]string{
			{"aite", "aitė"}, {"aitės", "aitė"}, {"uote", "uotė"}, {"uotės", "uotė"},
			{"ėjime", "ėjimas"}, {"esiu", "esys"}, {"asius", "asys"},
			{"avime", "avimas"}, {"ojime", "ojimas"}, {"okate", "okatė"}, {"okatės", "okatė"},
			{"aite!", "aite!"},
		} {
			if got := lithuanianConflicts(pair[0]); got != pair[1] {
				t.Error(pair, got)
			}
		}
		// aite has original p1=3. Its replacement has p1=3 as a byte
		// boundary but must not be re-marked after changing its length.
		if got := lithuanianStem("aite"); got != "ait" {
			t.Fatal("conflict kept original region", got)
		}
	})
	t.Run("released-stages-and-families", func(t *testing.T) {
		for _, pair := range [][2]string{
			{"advokate", "advokat"}, {"advokatės", "advokat"}, {"akiniais", "akin"}, {"auginti", "aug"},
			{"brolis", "brol"}, {"bėgdamas", "bėg"}, {"dirbti", "dirb"}, {"draugauti", "draug"},
			{"gaidys", "gaid"}, {"galva", "galv"}, {"galvos", "galv"}, {"geresnis", "ger"}, {"geriausias", "ger"},
			{"gervė", "gerv"}, {"gervės", "gerv"}, {"keliai", "kel"}, {"kelias", "kel"},
			{"lenkiškas", "lenk"}, {"lietuvaitės", "lietuvait"}, {"seserimis", "seser"}, {"seseris", "seser"}, {"sesers", "ses"},
			{"sūnus", "sūn"}, {"tvarkingas", "tvark"}, {"vandens", "vand"}, {"vanduo", "vand"},
			{"vyrai", "vyr"}, {"vyrams", "vyr"}, {"vyras", "vyr"}, {"vyrą", "vyr"}, {"vyrų", "vyr"}, {"žaidžia", "žaid"},
			{"bababingokas", "babab"}, {"bababčiai", "bababt"}, {"bababdžia", "bababd"},
			{"bababčingas", "bababt"}, {"bababdžingas", "bababd"}, {"bababgdas", "bababg"},
			{"beria", "ber"}, {"benimis", "ben"}, {"bababaite", "bababait"}, {"bababaitės", "bababait"},
			{"bababuote", "babab"}, {"bababuotės", "babab"}, {"bababėjime", "bababėjim"},
			{"bababesiu", "bababes"}, {"bababasius", "bababas"}, {"bababavime", "bababavim"},
			{"bababojime", "bababojim"}, {"bababokate", "bababokat"}, {"bababokatės", "bababokat"},
			{"parking'as", "parking'"}, {"babab'", "babab'"}, {"as", "as"}, {"aba", "ab"}, {"abė", "ab"}, {"bcdfgh", "bcdfgh"},
		} {
			if got := lithuanianStem(pair[0]); got != pair[1] {
				t.Errorf("stem(%q)=%q, want %q", pair[0], got, pair[1])
			}
		}
	})
	t.Run("fixed-region-after-shortening", func(t *testing.T) {
		// Original initial-a marking yields p1=6. After -as and -ing,
		// abaiot is only six runes: re-marking would yield 2 and wrongly
		// admit -ot at 4. The release keeps the original 6.
		if got := lithuanianRegion("abaiotingas"); got != 6 {
			t.Fatal("original initial-a region", got)
		}
		if got := lithuanianStem("abaiotingas"); got != "abaiot" {
			t.Fatal("re-marked shortened word", got)
		}
	})
}
