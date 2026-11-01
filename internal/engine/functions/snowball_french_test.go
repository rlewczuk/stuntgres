package functions

import (
	"strings"
	"testing"
)

func TestSnowballFrenchRules(t *testing.T) {
	t.Run("residual-final", frenchFinalStageRules)
	for _, tc := range frenchUnitMorphology {
		if got := frenchStem(tc[0]); got != tc[1] {
			t.Errorf("released %q: got %q, want %q", tc[0], got, tc[1])
		}
	}
	// Independent full-pipeline traces of region guards, longest-match failure,
	// mutate-and-continue, postlude, and rules absent from this release.
	for _, tc := range [][2]string{
		{"bababance", "babab"}, {"babance", "babanc"},
		{"bababissement", "babab"}, {"babaissement", "babaissement"},
		{"babablogie", "babablog"}, {"bababusion", "bababu"}, {"bababution", "bababu"}, {"bababence", "bababent"},
		{"bababicatrice", "babab"}, {"babicatrice", "babiqu"},
		{"bababité", "babab"}, {"bababicité", "babab"}, {"babicité", "babiqu"},
		{"bababif", "babab"}, {"bababive", "babab"}, {"babeuse", "babeux"}, {"babeusement", "babeux"},
		{"bababamment", "babab"}, {"bababemment", "bababent"}, {"bababiment", "babab"}, {"bababment", "bababment"},
		{"bababir", "babab"}, {"babaair", "babaair"}, {"bababions", "babab"}, {"babions", "babion"},
		{"bababeant", "babab"}, {"bababtion", "bababt"}, {"bababxion", "bababxion"}, {"baption", "baption"},
		{"cé", "cé"}, {"cèd", "ced"}, {"cafe\u0301", "cafe\u0301"},
		{"chats!", "chats!"}, {"l'", "l'"}, {"qu'amour", "amour"}, {"z'amour", "z'amour"}, {"l’amour", "l’amour"},
		{"bijoux", "bijoux"}, {"genoux", "genoux"}, {"français", "franc"}, {"niais", "ni"}, {"niaises", "niais"},
	} {
		if got := frenchStem(tc[0]); got != tc[1] {
			t.Errorf("boundary %q: got %q, want %q", tc[0], got, tc[1])
		}
	}
	t.Run("prelude-regions-postlude", func(t *testing.T) {
		for _, tc := range [][2]string{{"aia", "aIa"}, {"aië", "aIHe"}, {"ëia", "HeIa"}, {"aïa", "aHia"}, {"quai", "qUai"}, {"payer", "paYer"}, {"yoyo", "YoYo"}} {
			if got := string(frenchPrelude(make([]rune, 0, len(tc[0])), tc[0])); got != tc[1] {
				t.Errorf("prelude %q: %q, want %q", tc[0], got, tc[1])
			}
		}
		for _, tc := range []struct {
			word       string
			rv, r1, r2 int
		}{
			{"paris", 3, 3, 5}, {"colis", 3, 3, 5}, {"tapis", 3, 3, 5}, {"aie", 3, 3, 3}, {"abacus", 3, 2, 4}, {"babab", 2, 3, 5}, {"", 0, 0, 0},
		} {
			rv, r1, r2 := frenchRegions([]rune(tc.word))
			if rv != tc.rv || r1 != tc.r1 || r2 != tc.r2 {
				t.Errorf("regions %q: %d/%d/%d", tc.word, rv, r1, r2)
			}
		}
		for _, tc := range [][2]string{{"IUYHeHiH", "iuyëï"}, {"H", ""}, {"Hé", "é"}} {
			if got := string(frenchPostlude([]rune(tc[0]))); got != tc[1] {
				t.Errorf("postlude %q: %q", tc[0], got)
			}
		}
	})
	t.Run("standard-actions", func(t *testing.T) {
		// Each suffix is independently enumerated from the released algorithm;
		// expected stage outputs are not assembled from production tables.
		for _, tc := range []struct {
			suffixes, want string
			success        bool
		}{
			{"ance iqUe isme able iste eux ances iqUes ismes ables istes atrice ateur ation atrices ateurs ations ement ements ité ités if ive ifs ives euse euses issement issements", "babab", true},
			{"logie logies", "babablog", true}, {"usion ution usions utions", "bababu", true}, {"ence ences", "bababent", true},
			{"eaux", "bababeau", true}, {"aux", "bababal", true},
			{"amment", "bababant", false}, {"emment", "bababent", false},
		} {
			for _, suffix := range strings.Fields(tc.suffixes) {
				got, success := frenchStandard([]rune("babab"+suffix), 2, 3, 5)
				if string(got) != tc.want || success != tc.success {
					t.Errorf("standard %q: %q/%t, want %q/%t", suffix, string(got), success, tc.want, tc.success)
				}
			}
		}
		for _, tc := range []struct {
			word, want string
			rv, r1, r2 int
			success    bool
		}{
			{"bababivement", "babab", 2, 3, 5, true}, {"bababativement", "babab", 2, 3, 5, true},
			{"bababeusement", "babab", 2, 3, 5, true}, {"babeusement", "babeux", 2, 3, 5, true},
			{"bababablement", "babab", 2, 3, 5, true}, {"bababiqUement", "babab", 2, 3, 5, true},
			{"bababièrement", "bababi", 2, 3, 5, true}, {"bababIèrement", "bababi", 2, 3, 5, true},
			{"babababilité", "babab", 2, 3, 5, true}, {"bababilité", "bababl", 2, 3, 5, true},
			{"bababicité", "babab", 2, 3, 5, true}, {"babicité", "babiqU", 2, 3, 5, true},
			{"bababivité", "babab", 2, 3, 5, true}, {"bababicatif", "babab", 2, 3, 5, true},
			{"babicatif", "babiqU", 2, 3, 5, true}, {"bababiment", "bababi", 2, 3, 5, false},
			{"bababiments", "bababi", 2, 3, 5, false}, {"bababment", "bababment", 2, 3, 5, false},
			{"bababments", "bababments", 2, 3, 5, false}, {"babance", "babance", 2, 3, 5, false},
			{"babaissement", "babaissement", 2, 3, 5, false},
		} {
			got, success := frenchStandard([]rune(tc.word), tc.rv, tc.r1, tc.r2)
			if string(got) != tc.want || success != tc.success {
				t.Errorf("standard branch %q: %q/%t, want %q/%t", tc.word, string(got), success, tc.want, tc.success)
			}
		}
	})
	t.Run("verb-actions", func(t *testing.T) {
		for _, suffix := range strings.Fields("îmes ît îtes i ie ies ir ira irai iraIent irais irait iras irent irez iriez irions irons iront is issaIent issais issait issant issante issantes issants isse issent isses issez issiez issions issons it") {
			got, success := frenchIVerb([]rune("babab"+suffix), 2)
			if string(got) != "babab" || !success {
				t.Errorf("i-verb %q: %q/%t", suffix, string(got), success)
			}
			for _, prefix := range []string{"babaa", "babaH"} {
				word := prefix + suffix
				got, success := frenchIVerb([]rune(word), 2)
				if string(got) != word || success {
					t.Errorf("i-verb predecessor %q: %q/%t", word, string(got), success)
				}
			}
		}
		for _, suffix := range strings.Fields("ions é ée ées és èrent er era erai eraIent erais erait eras erez eriez erions erons eront ez iez âmes ât âtes a ai aIent ais ait ant ante antes ants as asse assent asses assiez assions") {
			got, success := frenchVerb([]rune("babab"+suffix), 2, 5)
			if string(got) != "babab" || !success {
				t.Errorf("verb %q: %q/%t", suffix, string(got), success)
			}
		}
		if got, success := frenchIVerb([]rune("babir"), 3); string(got) != "babir" || success {
			t.Fatal("predecessor outside RV", string(got), success)
		}
		if got, success := frenchVerb([]rune("babions"), 2, 5); string(got) != "babions" || success {
			t.Fatal("ions outside R2", string(got), success)
		}
		if got, success := frenchVerb([]rune("bababeant"), 2, 5); string(got) != "babab" || !success {
			t.Fatal("optional e", string(got), success)
		}
	})
}
