package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

// Historical pairs independently come from snowball-data commit
// 4f982ec072a091771b321ab7861841471942e14a spanish/{voc,output}.txt.
// Synthetic expectations trace REL_18_6 Spanish, not native tables or results.
func TestReferenceSpanishSnowballCorpus(t *testing.T) {
	t.Run("historical-vocabulary", func(t *testing.T) {
		spanishCorpus(t, [][2]string{
			{"activamente", "activ"},
			{"árboles", "arbol"},
			{"biología", "biolog"},
			{"caballos", "caball"},
			{"camión", "camion"},
			{"canción", "cancion"},
			{"canciones", "cancion"},
			{"casas", "cas"},
			{"construyendo", "constru"},
			{"corriendo", "corr"},
			{"diferencias", "diferent"},
			{"educación", "educ"},
			{"educativos", "educ"},
			{"hablando", "habl"},
			{"haciéndola", "hac"},
			{"niños", "niñ"},
			{"pague", "pag"},
			{"perros", "perr"},
			{"rápidamente", "rapid"},
			{"revolución", "revolu"},
			{"vergüenza", "vergüenz"},
		})
	})
	t.Run("released-standard-actions", func(t *testing.T) {
		// Independent 46 released endings; no unaccented acion/ucion from 3.0.0.
		var cases [][2]string
		for _, suffix := range strings.Fields("anza anzas ico ica icos icas ismo ismos able ables ible ibles ista istas oso osa osos osas amiento amientos imiento imientos adora ador ación adoras adores aciones ante antes ancia ancias amente mente idad idades iva ivo ivas ivos") {
			cases = append(cases, [2]string{"abac" + suffix, "abac"})
		}
		cases = append(cases,
			[2]string{"abaclogía", "abaclog"}, [2]string{"abaclogías", "abaclog"},
			[2]string{"abacución", "abacu"}, [2]string{"abacuciones", "abacu"},
			[2]string{"abacencia", "abacent"}, [2]string{"abacencias", "abacent"},
		)
		spanishCorpus(t, cases)
	})
	// Literal 96 released verb endings, not imported from production.
	verbs := strings.Fields("en es éis emos arían arías arán arás aríais aría aréis aríamos aremos ará aré erían erías erán erás eríais ería eréis eríamos eremos erá eré irían irías irán irás iríais iría iréis iríamos iremos irá iré aba ada ida ía ara iera ad ed id ase iese aste iste an aban ían aran ieran asen iesen aron ieron ado ido ando iendo ió ar er ir as abas adas idas ías aras ieras ases ieses ís áis abais íais arais ierais aseis ieseis asteis isteis ados idos amos ábamos íamos imos áramos iéramos iésemos ásemos")
	for start := 0; start < len(verbs); start += 24 {
		end := start + 24
		if end > len(verbs) {
			end = len(verbs)
		}
		group := verbs[start:end]
		t.Run("verbs-"+group[0], func(t *testing.T) {
			var cases [][2]string
			for _, suffix := range group {
				cases = append(cases, [2]string{"abac" + suffix, "abac"})
			}
			spanishCorpus(t, cases)
		})
	}
	t.Run("released-y-verbs", func(t *testing.T) {
		var cases [][2]string
		for _, suffix := range strings.Fields("ya ye yan yen yeron yendo yo yó yas yes yais yamos") {
			cases = append(cases, [2]string{"abacu" + suffix, "abacu"})
		}
		spanishCorpus(t, cases)
	})
	t.Run("attached-pronouns", func(t *testing.T) {
		var cases [][2]string
		for _, suffix := range strings.Fields("me se sela selo selas selos la le lo las les los nos") {
			cases = append(cases, [2]string{"abacar" + suffix, "abac"})
		}
		spanishCorpus(t, cases)
	})
}

func spanishCorpus(t *testing.T, cases [][2]string) {
	t.Helper()
	spanishScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		regtsExec(t, p, b, c, "CREATE TEXT SEARCH DICTIONARY spanish_corpus (TEMPLATE=snowball,Language=spanish)", 0)
		const query = "SELECT ts_lexize('spanish_corpus',$1::text) AS lexemes"
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
