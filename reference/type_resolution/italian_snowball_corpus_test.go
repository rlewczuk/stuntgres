package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

// Goldens are independent of native code: historical snowball-data commit
// 4f982ec072a091771b321ab7861841471942e14a, italian/{voc,output}.txt.
// Synthetic matrices below are hand-traced from the February 2025 release SBL,
// not copied from native rule tables. They require pinned-oracle adjudication.
func TestReferenceItalianSnowballMorphology(t *testing.T) {
	t.Run("historical-vocabulary", func(t *testing.T) {
		italianCorpus(t, [][2]string{
			{"aiuto", "aiut"}, {"amiche", "amic"}, {"amici", "amic"},
			{"azione", "azion"}, {"azioni", "azion"}, {"bellezza", "bellezz"},
			{"biologia", "biolog"}, {"caffè", "caff"}, {"cani", "can"},
			{"crocchi", "crocc"}, {"crocchio", "crocc"},
			{"diva", "div"}, {"divani", "divan"}, {"divano", "divan"},
			{"esperienza", "esperient"}, {"gatti", "gatt"}, {"guardandogli", "guard"},
			{"mangiando", "mang"}, {"mangiare", "mang"}, {"perché", "perc"},
			{"piccoli", "piccol"}, {"possibilità", "possibil"}, {"quadro", "quadr"},
			{"rapidamente", "rapid"}, {"rivoluzione", "rivolu"}, {"velocemente", "veloc"},
		})
	})
	t.Run("all-51-standard-suffixes", func(t *testing.T) {
		// abac: RV=3, R1=2, R2=4. Separate actions have literal goldens.
		var cases [][2]string
		for _, suffix := range strings.Fields("anza anze ico ici ica ice iche ichi ismo ismi abile abili ibile ibili ista iste isti istà istè istì oso osi osa ose mente atrice atrici ante anti azione azioni atore atori amento amenti imento imenti amente ità ivo ivi iva ive") {
			cases = append(cases, [2]string{"abac" + suffix, "abac"})
		}
		for _, pair := range [][2]string{{"logia", "abaclog"}, {"logie", "abaclog"}, {"uzione", "abacu"}, {"uzioni", "abacu"}, {"usione", "abacu"}, {"usioni", "abacu"}, {"enza", "abacent"}, {"enze", "abacent"}} {
			cases = append(cases, [2]string{"abac" + pair[0], pair[1]})
		}
		italianCorpus(t, cases)
	})
	t.Run("all-lowercase-verb-suffixes", func(t *testing.T) {
		var cases [][2]string
		// The 87-entry released inventory includes uppercase Yamo, exercised
		// separately below and at the private kernel seam.
		for _, suffix := range strings.Fields("ammo ando ano are arono asse assero assi assimo ata ate ati ato ava avamo avano avate avi avo emmo enda ende endi endo erà erai eranno ere erebbe erebbero erei eremmo eremo ereste eresti erete erò erono essero ete eva evamo evano evate evi evo iamo immo irà irai iranno ire irebbe irebbero irei iremmo iremo ireste iresti irete irò irono isca iscano isce isci isco iscono issero ita ite iti ito iva ivamo ivano ivate ivi ivo ono uta ute uti uto ar ir") {
			cases = append(cases, [2]string{"abac" + suffix, "abac"})
		}
		cases = append(cases, [2]string{"abacYamo", "abacyam"})
		italianCorpus(t, cases)
	})
	for _, ending := range []string{"ando", "endo", "ar", "er", "ir"} {
		t.Run("all-37-pronouns-after-"+ending, func(t *testing.T) {
			var cases [][2]string
			for _, pronoun := range strings.Fields("ci gli la le li lo mi ne si ti vi sene gliela gliele glieli glielo gliene mela mele meli melo mene tela tele teli telo tene cela cele celi celo cene vela vele veli velo vene") {
				cases = append(cases, [2]string{"abac" + ending + pronoun, "abac"})
			}
			italianCorpus(t, cases)
		})
	}
	t.Run("secondary-actions-and-fixed-regions", func(t *testing.T) {
		italianCorpus(t, [][2]string{
			{"abacicazione", "abac"}, {"abacicazioni", "abac"}, {"abicazione", "abic"},
			{"abacicatore", "abac"}, {"abacicamente", "abac"}, {"abacosamente", "abac"},
			{"abacivamente", "abac"}, {"abacativamente", "abac"}, {"abacabilamente", "abac"},
			{"abacabilità", "abac"}, {"abacicità", "abac"}, {"abacività", "abac"},
			{"abacicativo", "abac"}, {"abacatità", "abacat"}, {"abacatività", "abacat"},
			{"abicativo", "abic"}, {"abacivo", "abac"},
			{"amente", "ament"}, {"abamente", "ab"}, {"abbamente", "abb"},
			{"ico", "ico"}, {"abico", "abic"}, {"abacico", "abac"}, {"abaccico", "abacc"},
			{"baar", "baar"}, {"bacar", "bac"}, {"baccar", "bacc"},
			{"abacer", "abacer"}, {"abacerlo", "abac"}, {"arci", "arci"}, {"darlo", "darl"},
			{"bacarlo", "bac"}, {"abacarlo", "abac"}, {"abacglielo", "abacgliel"},
			{"bach", "bach"}, {"bacch", "bacc"}, {"bagh", "bagh"}, {"bacgh", "bacg"},
			{"crocchio", "crocc"}, {"abacii", "abac"}, {"abacù", "abacù"},
			{"abacista", "abac"}, {"abacisti", "abac"},
			{"abemente", "abement"}, {"abacmente", "abac"}, {"abaccmente", "abacc"},
			{"baare", "baar"}, {"bacare", "bac"}, {"baccare", "bacc"},
			{"abare", "abar"}, {"abcare", "abcar"}, {"abacare", "abac"},
			{"aabare", "aab"}, {"bbare", "bbar"}, {"bbacare", "bbac"}, {"aaae", "aaae"},
			{"bacivo", "bac"}, {"baciva", "bac"}, {"abivo", "abiv"},
			{"abacazioneando", "abacazion"}, {"abacicoico", "abacic"},
		})
	})
}

func italianCorpus(t *testing.T, cases [][2]string) {
	t.Helper()
	// Every group owns its fixture deadline rather than extending it for a corpus.
	dictionaryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		regtsExec(t, p, b, c, "CREATE TEXT SEARCH DICTIONARY italian_corpus (TEMPLATE=snowball,Language=italian)", 0)
		const query = "SELECT ts_lexize('italian_corpus',$1::text) AS lexemes"
		stmt, err := c.PrepareContext(p.Ctx, query)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		for _, tc := range cases {
			t.Run(tc[0], func(t *testing.T) {
				for _, e := range []fixture.Executor{c, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: query}} {
					dictionaryResult(t, p, b, e, query, "{"+tc[1]+"}", tc[0])
				}
			})
		}
	})
}
