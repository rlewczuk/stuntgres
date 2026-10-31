package functions

import (
	"strings"
	"testing"
)

func TestSnowballItalianRules(t *testing.T) {
	// Exercise registered authority as well as the direct released kernel below.
	provider := lookupSnowballLanguage("italian")
	if provider == nil || provider.stem == nil {
		t.Fatal("missing released Italian provider")
	}
	if got := italianStem("abacYamo"); got != "abac" {
		t.Fatalf("released uppercase Yamo kernel rule: %q", got)
	}
	check := func(input, want string) {
		t.Helper()
		if got := provider.stem(input); got != want {
			t.Errorf("stem(%q)=%q, want %q", input, got, want)
		}
	}
	// Independently transcribed historical snowball-data goldens and hand-traced
	// released-SBL witnesses. No production rule tables generate expectations.
	for _, tc := range [][2]string{
		{"aiuto", "aiut"}, {"amiche", "amic"}, {"amici", "amic"}, {"azione", "azion"}, {"azioni", "azion"},
		{"bellezza", "bellezz"}, {"biologia", "biolog"}, {"caffè", "caff"}, {"cani", "can"},
		{"crocchi", "crocc"}, {"crocchio", "crocc"}, {"diva", "div"}, {"divani", "divan"}, {"divano", "divan"},
		{"esperienza", "esperient"}, {"gatti", "gatt"}, {"guardandogli", "guard"},
		{"mangiando", "mang"}, {"mangiare", "mang"}, {"perché", "perc"}, {"piccoli", "piccol"},
		{"possibilità", "possibil"}, {"quadro", "quadr"}, {"rapidamente", "rapid"}, {"rivoluzione", "rivolu"}, {"velocemente", "veloc"},
		{"abacYamo", "abac"}, {"abacyamo", "abacyam"},
		{"abacicazione", "abac"}, {"abacicazioni", "abac"}, {"abicazione", "abic"}, {"abacicatore", "abac"},
		{"abacicamente", "abac"}, {"abacosamente", "abac"}, {"abacivamente", "abac"},
		{"abacativamente", "abac"}, {"abacabilamente", "abac"}, {"abacabilità", "abac"},
		{"abacicità", "abac"}, {"abacività", "abac"}, {"abacicativo", "abac"}, {"abicativo", "abic"},
		{"abacatività", "abacat"}, {"abacatità", "abacat"},
		{"amente", "ament"}, {"abamente", "ab"}, {"abbamente", "abb"},
		{"ico", "ico"}, {"ica", "ica"}, {"amo", "amo"}, {"abico", "abic"}, {"abacico", "abac"}, {"abaccico", "abacc"},
		{"baar", "baar"}, {"bacar", "bac"}, {"baccar", "bacc"}, {"abacer", "abacer"}, {"abacerlo", "abac"},
		{"arci", "arci"}, {"darlo", "darl"}, {"bacarlo", "bac"}, {"abacarlo", "abac"}, {"abacglielo", "abacgliel"},
		{"abemente", "abement"}, {"abacmente", "abac"}, {"abaccmente", "abacc"},
		{"baare", "baar"}, {"bacare", "bac"}, {"baccare", "bacc"},
		{"abare", "abar"}, {"abcare", "abcar"}, {"abacare", "abac"},
		{"aabare", "aab"}, {"bbare", "bbar"}, {"bbacare", "bbac"}, {"aaae", "aaae"},
		{"bacivo", "bac"}, {"baciva", "bac"}, {"abivo", "abiv"},
		{"abacazioneando", "abacazion"}, {"abacicoico", "abacic"},
		{"bach", "bach"}, {"bacch", "bacc"}, {"bagh", "bagh"}, {"bacgh", "bacg"},
		{"abacá", "abac"}, {"abacé", "abac"}, {"abací", "abac"}, {"abacó", "abac"}, {"abacú", "abacù"},
		{"abacaia", "abacai"}, {"abacaua", "abacau"}, {"abacquiamo", "abacqu"},
		// R2 removes ic from dell'amico; no apostrophe/elision rule is involved.
		{"abacii", "abac"}, {"abacù", "abacù"}, {"l'amico", "l'amic"}, {"dell'amico", "dell'am"},
		{"caffe\u0300", "caffe\u0300"}, {"perche\u0301", "perche\u0301"}, {"cani!", "cani!"}, {"cani ", "cani "}, {"", ""},
	} {
		check(tc[0], tc[1])
	}
	t.Run("all-51-standard-suffixes", func(t *testing.T) {
		for _, suffix := range strings.Fields("anza anze ico ici ica ice iche ichi ismo ismi abile abili ibile ibili ista iste isti istà istè istì oso osi osa ose mente atrice atrici ante anti azione azioni atore atori amento amenti imento imenti amente ità ivo ivi iva ive") {
			check("abac"+suffix, "abac")
		}
		for _, tc := range [][2]string{{"logia", "abaclog"}, {"logie", "abaclog"}, {"uzione", "abacu"}, {"uzioni", "abacu"}, {"usione", "abacu"}, {"usioni", "abacu"}, {"enza", "abacent"}, {"enze", "abacent"}} {
			check("abac"+tc[0], tc[1])
		}
	})
	t.Run("all-87-verb-suffixes", func(t *testing.T) {
		for _, suffix := range strings.Fields("ammo ando ano are arono asse assero assi assimo ata ate ati ato ava avamo avano avate avi avo emmo enda ende endi endo erà erai eranno ere erebbe erebbero erei eremmo eremo ereste eresti erete erò erono essero ete eva evamo evano evate evi evo Yamo iamo immo irà irai iranno ire irebbe irebbero irei iremmo iremo ireste iresti irete irò irono isca iscano isce isci isco iscono issero ita ite iti ito iva ivamo ivano ivate ivi ivo ono uta ute uti uto ar ir") {
			check("abac"+suffix, "abac")
		}
	})
	t.Run("all-37-pronouns-all-five-predecessors", func(t *testing.T) {
		for _, predecessor := range []string{"ando", "endo", "ar", "er", "ir"} {
			for _, suffix := range strings.Fields("ci gli la le li lo mi ne si ti vi sene gliela gliele glieli glielo gliene mela mele meli melo mene tela tele teli telo tene cela cele celi celo cene vela vele veli velo vene") {
				check("abac"+predecessor+suffix, "abac")
			}
		}
	})
}
