package functions

import (
	"strings"
	"unicode/utf8"
)

// Armenian rules are ported from PostgreSQL REL_18_6's Snowball 2.2.0
// src/backend/snowball/libstemmer/stem_UTF_8_armenian.c (BSD Snowball license;
// see snowball_LICENSE.txt). The four finite among tables only delete suffixes.
// Regions are original-input byte offsets; no normalization or case folding is
// performed by this kernel. The dictionary wrapper owns those adapter rules.
var armenianAdjectives = [...]string{
	"բար", "րորդ", "երորդ", "ալի", "ակի", "որակ", "եղ", "վուն",
	"արան", "ական", "են", "երեն", "եկեն", "որէն", "ին", "գին",
	"ովին", "լայն", "պես", "իվ", "ատ", "ավետ", "կոտ",
}

var armenianVerbs = [...]string{
	"ար", "ացար", "եցար", "ացրիր", "ացիր", "եցիր", "վեցիր", "ալուց",
	"ելուց", "աց", "եց", "ացրեց", "ալու", "ելու", "աք", "ցաք",
	"ացաք", "ացրիք", "ացիք", "եցիք", "վեցիք", "անք", "ցանք", "ացանք",
	"ացրինք", "ացինք", "եցինք", "վեցինք", "ա", "ացա", "եցա", "վե",
	"ացրի", "ացի", "եցի", "վեցի", "ալ", "ըալ", "ացնալ", "անալ",
	"ենալ", "ել", "ըել", "նել", "ցնել", "եցնել", "չել", "վել",
	"ացվել", "եցվել", "տել", "ատել", "ոտել", "կոտել", "ված", "ում",
	"վում", "ան", "ցան", "ացան", "ացրին", "ացին", "եցին", "վեցին",
	"ալիս", "ելիս", "ավ", "ացավ", "եցավ", "ալով", "ելով",
}

var armenianNouns = [...]string{
	"գար", "վոր", "ավոր", "անօց", "ոց", "ու", "ք", "արք",
	"չեք", "իք", "ալիք", "անիք", "վածք", "ույք", "ունք", "մունք",
	"ենք", "ոնք", "իչք", "որդ", "ույթ", "ցի", "ուհի", "իլ",
	"ուկ", "ակ", "յակ", "անակ", "իկ", "յուն", "ություն", "ան",
	"արան", "պան", "ստան", "եղէն", "ածո", "իչ", "ուս", "ուստ",
}

var armenianEndings = [...]string{
	"եր", "ներ", "ց", "ուց", "ից", "երից", "ներից", "ցից",
	"վանից", "ոջից", "վից", "ոց", "սա", "վա", "ամբ", "դ",
	"երդ", "ներդ", "ուդ", "անդ", "ությանդ", "վանդ", "ոջդ", "ը",
	"երը", "ները", "անը", "ությանը", "վանը", "ոջը", "ի", "երի",
	"ների", "վի", "երում", "ներում", "անում", "ն", "երն", "ներն",
	"ուն", "ան", "ության", "վան", "ին", "երին", "ներին", "ությանն",
	"ոջ", "ությանս", "վանս", "ոջս", "ով", "երով", "ներով", "անով", "վով",
}

func armenianVowel(r rune) bool {
	switch r {
	case 'ա', 'է', 'ի', 'օ', 'ւ', 'ե', 'ո', 'ը':
		return true
	}
	return false
}

// pV follows the first vowel, p2 the second vowel-to-nonvowel boundary.
// Missing boundaries stay at the original limit, as in mark_regions.
func armenianRegions(input string) (pV, p2 int) {
	pV, p2 = len(input), len(input)
	state := 0
	for i, r := range input {
		vowel := armenianVowel(r)
		switch state {
		case 0:
			if vowel {
				pV = i + utf8.RuneLen(r)
				state = 1
			}
		case 1:
			if !vowel {
				state = 2
			}
		case 2:
			if vowel {
				state = 3
			}
		case 3:
			if !vowel {
				return pV, i + utf8.RuneLen(r)
			}
		}
	}
	return pV, p2
}

// Match within pV before testing the selected suffix against p2. In particular,
// an ending outside p2 must not fall back to a shorter matching ending inside it.
func armenianDeleteSuffix(input string, suffixes []string, pV, region int) string {
	longest := 0
	for _, suffix := range suffixes {
		if len(suffix) > longest && len(input)-len(suffix) >= pV && strings.HasSuffix(input, suffix) {
			longest = len(suffix)
		}
	}
	if longest != 0 && len(input)-longest >= region {
		return input[:len(input)-longest]
	}
	return input
}

func armenianStem(input string) string {
	pV, p2 := armenianRegions(input)
	input = armenianDeleteSuffix(input, armenianEndings[:], pV, p2)
	input = armenianDeleteSuffix(input, armenianVerbs[:], pV, pV)
	input = armenianDeleteSuffix(input, armenianAdjectives[:], pV, pV)
	return armenianDeleteSuffix(input, armenianNouns[:], pV, pV)
}
