package functions

import "unicode/utf8"

// Direct port of PostgreSQL REL_18_6 stem_UTF_8_basque.c and Snowball
// d19326ac6c1b9a417fc872f7c2f845265a5e9ece algorithms/basque.sbl.
// BSD license: see snowball_LICENSE.txt. Case and stopwords belong to the wrapper.
type basqueRule struct {
	suffix      string
	region      uint8 // 0: unconditional replacement; 1: RV; 2: R1; 3: R2
	replacement string
}

var basqueVerbs = [...]basqueRule{
	{"le", 1, ""},
	{"la", 1, ""},
	{"tzaile", 1, ""},
	{"aldatu", 1, ""},
	{"atu", 1, ""},
	{"tzailea", 1, ""},
	{"taile", 1, ""},
	{"tailea", 1, ""},
	{"pera", 1, ""},
	{"gale", 1, ""},
	{"galea", 1, ""},
	{"gura", 1, ""},
	{"kura", 1, ""},
	{"kor", 1, ""},
	{"korra", 1, ""},
	{"or", 1, ""},
	{"orra", 1, ""},
	{"tun", 1, ""},
	{"tuna", 1, ""},
	{"gaitz", 1, ""},
	{"gaitza", 1, ""},
	{"kaitz", 1, ""},
	{"kaitza", 1, ""},
	{"ezin", 1, ""},
	{"ezina", 1, ""},
	{"tezin", 1, ""},
	{"tezina", 1, ""},
	{"errez", 1, ""},
	{"erreza", 1, ""},
	{"karri", 1, ""},
	{"karria", 1, ""},
	{"tzaga", 1, ""},
	{"tzaka", 1, ""},
	{"tzake", 1, ""},
	{"tzeke", 1, ""},
	{"ez", 1, ""},
	{"eza", 1, ""},
	{"tzez", 1, ""},
	{"keta", 1, ""},
	{"eta", 1, ""},
	{"etan", 1, ""},
	{"pen", 1, ""},
	{"pena", 1, ""},
	{"tze", 1, ""},
	{"atze", 1, ""},
	{"kuntza", 1, ""},
	{"kunde", 1, ""},
	{"kundea", 1, ""},
	{"kune", 1, ""},
	{"kunea", 1, ""},
	{"kuna", 1, ""},
	{"kera", 1, ""},
	{"era", 1, ""},
	{"kizun", 1, ""},
	{"kizuna", 1, ""},
	{"dura", 1, ""},
	{"tura", 1, ""},
	{"men", 1, ""},
	{"mena", 1, ""},
	{"go", 1, ""},
	{"ago", 1, ""},
	{"tio", 1, ""},
	{"taldi", 1, ""},
	{"taldia", 1, ""},
	{"aldi", 1, ""},
	{"aldia", 1, ""},
	{"gune", 1, ""},
	{"gunea", 1, ""},
	{"bide", 1, ""},
	{"bidea", 1, ""},
	{"pide", 1, ""},
	{"pidea", 1, ""},
	{"gai", 1, ""},
	{"gaia", 1, ""},
	{"ki", 1, ""},
	{"kin", 1, ""},
	{"rekin", 1, ""},
	{"kina", 1, ""},
	{"kari", 1, ""},
	{"karia", 1, ""},
	{"ari", 1, ""},
	{"tari", 1, ""},
	{"etari", 1, ""},
	{"gailu", 1, ""},
	{"gailua", 1, ""},
	{"kide", 1, ""},
	{"kidea", 1, ""},
	{"ide", 1, ""},
	{"idea", 1, ""},
	{"du", 1, ""},
	{"ka", 1, ""},
	{"kan", 1, ""},
	{"an", 1, ""},
	{"ean", 1, ""},
	{"tu", 1, ""},
	{"lari", 1, ""},
	{"tatu", 1, ""},
	{"rean", 1, ""},
	{"tarazi", 1, ""},
	{"arazi", 1, ""},
	{"tzat", 1, ""},
	{"bera", 1, ""},
	{"dako", 1, ""},
	{"garri", 3, ""},
	{"garria", 3, ""},
	{"tza", 3, ""},
	{"atseden", 0, "atseden"},
	{"arabera", 0, "arabera"},
	{"baditu", 0, "baditu"},
}

var basqueNouns = [...]basqueRule{
	{"ari", 1, ""},
	{"aria", 1, ""},
	{"bizia", 1, ""},
	{"kari", 1, ""},
	{"karia", 1, ""},
	{"lari", 1, ""},
	{"laria", 1, ""},
	{"tari", 1, ""},
	{"taria", 1, ""},
	{"zain", 1, ""},
	{"zaina", 1, ""},
	{"tzain", 1, ""},
	{"tzaina", 1, ""},
	{"zale", 1, ""},
	{"zalea", 1, ""},
	{"tzale", 1, ""},
	{"tzalea", 1, ""},
	{"aizun", 1, ""},
	{"orde", 1, ""},
	{"ordea", 1, ""},
	{"burua", 1, ""},
	{"ohi", 1, ""},
	{"ohia", 1, ""},
	{"kintza", 1, ""},
	{"gintzo", 1, ""},
	{"gintzu", 1, ""},
	{"tzu", 1, ""},
	{"tzua", 1, ""},
	{"tzo", 1, ""},
	{"tzoa", 1, ""},
	{"kuntza", 1, ""},
	{"talde", 1, ""},
	{"taldea", 1, ""},
	{"eria", 1, ""},
	{"keria", 1, ""},
	{"teria", 1, ""},
	{"di", 1, ""},
	{"za", 1, ""},
	{"ada", 1, ""},
	{"tara", 1, ""},
	{"etara", 1, ""},
	{"tra", 1, ""},
	{"ta", 1, ""},
	{"tegi", 1, ""},
	{"tegia", 1, ""},
	{"keta", 1, ""},
	{"z", 1, ""},
	{"zko", 1, ""},
	{"zkoa", 1, ""},
	{"ti", 1, ""},
	{"tia", 1, ""},
	{"tsu", 1, ""},
	{"tsua", 1, ""},
	{"zu", 1, ""},
	{"zua", 1, ""},
	{"bera", 1, ""},
	{"pera", 1, ""},
	{"zto", 1, ""},
	{"ztoa", 1, ""},
	{"asi", 1, ""},
	{"asia", 1, ""},
	{"gile", 1, ""},
	{"gilea", 1, ""},
	{"estu", 1, ""},
	{"estua", 1, ""},
	{"larri", 1, ""},
	{"larria", 1, ""},
	{"nahi", 1, ""},
	{"nahia", 1, ""},
	{"koi", 1, ""},
	{"koia", 1, ""},
	{"oi", 1, ""},
	{"oia", 1, ""},
	{"goi", 1, ""},
	{"min", 1, ""},
	{"mina", 1, ""},
	{"dun", 1, ""},
	{"duna", 1, ""},
	{"duru", 1, ""},
	{"durua", 1, ""},
	{"duri", 1, ""},
	{"duria", 1, ""},
	{"os", 1, ""},
	{"osa", 1, ""},
	{"oso", 1, ""},
	{"osoa", 1, ""},
	{"ar", 1, ""},
	{"ara", 1, ""},
	{"tar", 1, ""},
	{"dar", 1, ""},
	{"dara", 1, ""},
	{"tiar", 1, ""},
	{"tiara", 1, ""},
	{"liar", 1, ""},
	{"liara", 1, ""},
	{"gabe", 1, ""},
	{"gabea", 1, ""},
	{"kabe", 1, ""},
	{"kabea", 1, ""},
	{"ga", 1, ""},
	{"ge", 1, ""},
	{"kada", 1, ""},
	{"tasun", 1, ""},
	{"tasuna", 1, ""},
	{"asun", 1, ""},
	{"asuna", 1, ""},
	{"go", 1, ""},
	{"mendu", 1, ""},
	{"mendua", 1, ""},
	{"mentu", 1, ""},
	{"mentua", 1, ""},
	{"mendi", 1, ""},
	{"mendia", 1, ""},
	{"zio", 1, ""},
	{"zioa", 1, ""},
	{"zino", 1, ""},
	{"zinoa", 1, ""},
	{"zione", 1, ""},
	{"zionea", 1, ""},
	{"ezia", 1, ""},
	{"degi", 1, ""},
	{"degia", 1, ""},
	{"egi", 1, ""},
	{"egia", 1, ""},
	{"toki", 1, ""},
	{"tokia", 1, ""},
	{"leku", 1, ""},
	{"lekua", 1, ""},
	{"gintza", 1, ""},
	{"alde", 1, ""},
	{"aldea", 1, ""},
	{"kalde", 1, ""},
	{"kaldea", 1, ""},
	{"gune", 1, ""},
	{"gunea", 1, ""},
	{"une", 1, ""},
	{"unea", 1, ""},
	{"una", 1, ""},
	{"pe", 1, ""},
	{"pea", 1, ""},
	{"gibel", 1, ""},
	{"gibela", 1, ""},
	{"ondo", 1, ""},
	{"ondoa", 1, ""},
	{"arte", 1, ""},
	{"artea", 1, ""},
	{"aurre", 1, ""},
	{"aurrea", 1, ""},
	{"etxe", 1, ""},
	{"etxea", 1, ""},
	{"ola", 1, ""},
	{"ontzi", 1, ""},
	{"ontzia", 1, ""},
	{"gela", 1, ""},
	{"denda", 1, ""},
	{"taldi", 1, ""},
	{"taldia", 1, ""},
	{"aldi", 1, ""},
	{"aldia", 1, ""},
	{"te", 1, ""},
	{"tea", 1, ""},
	{"zaro", 1, ""},
	{"zaroa", 1, ""},
	{"taro", 1, ""},
	{"taroa", 1, ""},
	{"oro", 1, ""},
	{"oroa", 1, ""},
	{"aro", 1, ""},
	{"aroa", 1, ""},
	{"ero", 1, ""},
	{"eroa", 1, ""},
	{"eroz", 1, ""},
	{"eroza", 1, ""},
	{"ka", 1, ""},
	{"kan", 1, ""},
	{"kana", 1, ""},
	{"tako", 1, ""},
	{"etako", 1, ""},
	{"takoa", 1, ""},
	{"kote", 1, ""},
	{"kotea", 1, ""},
	{"tzar", 1, ""},
	{"tzarra", 1, ""},
	{"handi", 1, ""},
	{"handia", 1, ""},
	{"kondo", 1, ""},
	{"kondoa", 1, ""},
	{"skila", 1, ""},
	{"no", 1, ""},
	{"noa", 1, ""},
	{"ño", 1, ""},
	{"ñoa", 1, ""},
	{"ska", 1, ""},
	{"xka", 1, ""},
	{"zka", 1, ""},
	{"tila", 1, ""},
	{"to", 1, ""},
	{"toa", 1, ""},
	{"tto", 1, ""},
	{"ttoa", 1, ""},
	{"txo", 1, ""},
	{"txoa", 1, ""},
	{"txu", 1, ""},
	{"txua", 1, ""},
	{"anda", 1, ""},
	{"anga", 1, ""},
	{"urren", 1, ""},
	{"urrena", 1, ""},
	{"gai", 1, ""},
	{"gaia", 1, ""},
	{"gei", 1, ""},
	{"geia", 1, ""},
	{"eme", 1, ""},
	{"emea", 1, ""},
	{"kume", 1, ""},
	{"kumea", 1, ""},
	{"sa", 1, ""},
	{"ko", 1, ""},
	{"eko", 1, ""},
	{"koa", 1, ""},
	{"ena", 1, ""},
	{"enea", 1, ""},
	{"ne", 1, ""},
	{"nea", 1, ""},
	{"kor", 1, ""},
	{"korra", 1, ""},
	{"ez", 1, ""},
	{"eza", 1, ""},
	{"eta", 1, ""},
	{"etan", 1, ""},
	{"ki", 1, ""},
	{"kia", 1, ""},
	{"kin", 1, ""},
	{"kina", 1, ""},
	{"tu", 1, ""},
	{"tua", 1, ""},
	{"du", 1, ""},
	{"dua", 1, ""},
	{"ek", 1, ""},
	{"tarik", 1, ""},
	{"tariko", 1, ""},
	{"tan", 1, ""},
	{"ordu", 1, ""},
	{"ordua", 1, ""},
	{"oste", 1, ""},
	{"ostea", 1, ""},
	{"tzara", 1, ""},
	{"ra", 1, ""},
	{"antza", 1, ""},
	{"behar", 1, ""},
	{"ro", 1, ""},
	{"giro", 1, ""},
	{"ak", 1, ""},
	{"zp", 1, ""},
	{"ket", 1, ""},
	{"kail", 1, ""},
	{"kaila", 1, ""},
	{"ail", 1, ""},
	{"kirri", 1, ""},
	{"kirria", 1, ""},
	{"ngo", 1, ""},
	{"ngoa", 1, ""},
	{"ñi", 1, ""},
	{"sko", 1, ""},
	{"sta", 1, ""},
	{"koitz", 1, ""},
	{"koitza", 1, ""},
	{"na", 1, ""},
	{"garren", 1, ""},
	{"garrena", 1, ""},
	{"kera", 1, ""},
	{"gerren", 1, ""},
	{"gerrena", 1, ""},
	{"garna", 1, ""},
	{"kide", 1, ""},
	{"tz", 1, ""},
	{"tuko", 1, ""},
	{"ora", 3, ""},
	{"garri", 3, ""},
	{"garria", 3, ""},
	{"or", 3, ""},
	{"buru", 3, ""},
	{"ren", 3, ""},
	{"tza", 3, ""},
	{"joka", 0, "jok"},
	{"tzen", 2, ""},
	{"ten", 2, ""},
	{"en", 2, ""},
	{"tatu", 2, ""},
	{"trako", 0, "tra"},
	{"minutuko", 0, "minutu"},
	{"zehar", 0, "zehar"},
	{"geldi", 0, "geldi"},
	{"igaro", 0, "igaro"},
	{"aurka", 0, "aurka"},
}

var basqueAdjectives = [...]basqueRule{
	{"era", 1, ""},
	{"ero", 1, ""},
	{"go", 1, ""},
	{"tate", 1, ""},
	{"tade", 1, ""},
	{"date", 1, ""},
	{"dade", 1, ""},
	{"keria", 1, ""},
	{"ki", 1, ""},
	{"to", 1, ""},
	{"ro", 1, ""},
	{"la", 1, ""},
	{"gi", 1, ""},
	{"larik", 1, ""},
	{"lanik", 1, ""},
	{"ik", 1, ""},
	{"ztik", 1, ""},
	{"rik", 1, ""},
	{"zlea", 0, "z"},
}

func basqueVowel(r rune) bool {
	return r == 'a' || r == 'e' || r == 'i' || r == 'o' || r == 'u'
}

// All marks are byte offsets in the original input, never recomputed after slices.
func basqueRegions(input string) (pV, p1, p2 int) {
	pV, p1, p2 = len(input), len(input), len(input)
	first, n := utf8.DecodeRuneInString(input)
	if n < len(input) {
		second, m := utf8.DecodeRuneInString(input[n:])
		pos := n + m
		if !basqueVowel(first) && basqueVowel(second) {
			if pos < len(input) {
				_, size := utf8.DecodeRuneInString(input[pos:])
				pV = pos + size
			}
		} else {
			seekVowel := !basqueVowel(second)
			for i, r := range input[pos:] {
				if basqueVowel(r) == seekVowel {
					pV = pos + i + utf8.RuneLen(r)
					break
				}
			}
		}
	}
	previousVowel := false
	for i, r := range input {
		vowel := basqueVowel(r)
		if previousVowel && !vowel {
			if p1 == len(input) {
				p1 = i + utf8.RuneLen(r)
			} else {
				p2 = i + utf8.RuneLen(r)
				break
			}
		}
		previousVowel = vowel
	}
	return
}

// Scratch is invocation-local and allocated only when a real replacement or a
// deletion before a retained tail requires it. Pure terminal deletion slices input.
type basqueWord struct {
	input  string
	buffer []byte
	cursor int
}

func (w *basqueWord) byteAt(i int) byte {
	if w.buffer != nil {
		return w.buffer[i]
	}
	return w.input[i]
}

func (w *basqueWord) apply(rules []basqueRule, regions [4]int) bool {
	selected := -1
	longest := 0
	for i := range rules {
		suffix := rules[i].suffix
		if len(suffix) <= longest || len(suffix) > w.cursor {
			continue
		}
		start := w.cursor - len(suffix)
		same := true
		for j := range len(suffix) {
			if w.byteAt(start+j) != suffix[j] {
				same = false
				break
			}
		}
		if same {
			selected, longest = i, len(suffix)
		}
	}
	if selected < 0 {
		return false
	}
	rule := rules[selected]
	start := w.cursor - longest
	// A failed longest match never retries a shorter entry.
	if start < regions[rule.region] {
		return false
	}
	replacement := rule.replacement
	if replacement != rule.suffix {
		if w.buffer == nil && replacement == "" && w.cursor == len(w.input) {
			w.input = w.input[:start]
		} else {
			if w.buffer == nil {
				w.buffer = []byte(w.input)
			}
			end := start + len(replacement)
			copy(w.buffer[end:], w.buffer[w.cursor:])
			copy(w.buffer[start:end], replacement)
			w.buffer = w.buffer[:len(w.buffer)-(w.cursor-start)+len(replacement)]
		}
	}
	// Even same-text protections consume cursor distance, preserving their tail.
	w.cursor = start
	return true
}

func basqueStem(input string) string {
	pV, p1, p2 := basqueRegions(input)
	regions := [4]int{0, pV, p1, p2}
	word := basqueWord{input: input, cursor: len(input)}
	for word.apply(basqueVerbs[:], regions) {
	}
	for word.apply(basqueNouns[:], regions) {
	}
	word.apply(basqueAdjectives[:], regions)
	if word.buffer != nil {
		return string(word.buffer)
	}
	return word.input
}
