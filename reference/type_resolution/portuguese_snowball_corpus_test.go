package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

// Historical pairs are from snowball-data 4f982ec072a091771b321ab7861841471942e14a
// portuguese/{voc,output}.txt. Synthetic suffix expectations independently trace
// REL_18_6 stem_UTF_8_portuguese.c, not the provider's tables or native output.
func TestReferencePortugueseSnowballCorpus(t *testing.T) {
	t.Run("historical-vocabulary", func(t *testing.T) {
		portugueseCorpus(t, [][2]string{
			{"ações", "açõ"}, {"ativamente", "ativ"}, {"cadeiras", "cadeir"},
			{"casa", "cas"}, {"casas", "cas"}, {"ciência", "ciênc"},
			{"corações", "coraçõ"}, {"correndo", "corr"}, {"correr", "corr"},
			{"felicidade", "felic"}, {"lápis", "láp"}, {"mães", "mã"},
			{"pague", "pag"}, {"rapidamente", "rapid"},
		})
	})
	t.Run("standard-actions", func(t *testing.T) {
		// abac has R1=2, R2=4 and RV=3. Each deletion below begins
		// in R2; the fixed consonant tail survives residual-form processing.
		var cases [][2]string
		for _, suffix := range strings.Fields("ica ância adora osa ista iva eza idade ante mente amente ável ível ico ismo oso amento imento ivo ação ador icas adoras osas istas ivas ezas idades adores antes ações icos ismos osos amentos imentos ivos") {
			cases = append(cases, [2]string{"abac" + suffix, "abac"})
		}
		cases = append(cases,
			[2]string{"abaclogia", "abaclog"}, [2]string{"abaclogias", "abaclog"},
			[2]string{"abacução", "abacu"}, [2]string{"abacuções", "abacu"},
			[2]string{"abacência", "abacent"}, [2]string{"abacências", "abacent"},
			[2]string{"abaceira", "abaceir"}, [2]string{"abaceiras", "abaceir"},
			[2]string{"abacativamente", "abac"}, [2]string{"abacosamente", "abac"},
			[2]string{"abacicamente", "abac"}, [2]string{"abacadamente", "abac"},
			[2]string{"abacantemente", "abac"}, [2]string{"abacavelmente", "abac"},
			[2]string{"abacívelmente", "abac"}, [2]string{"abacabilidades", "abac"},
			[2]string{"abacicidade", "abac"}, [2]string{"abacividade", "abac"},
			[2]string{"abacativo", "abac"}, [2]string{"abacatividades", "abacat"},
		)
		portugueseCorpus(t, cases)
	})
	// All 120 released verb endings have literal independent suffix witnesses.
	// A standard ira/iras match lacking preceding e fails, allowing the verb
	// stage to remove that ending. No production table is consulted here.
	verbs := strings.Fields("ada ida ia aria eria iria ara era ira ava asse esse isse aste este iste ei arei erei irei am iam ariam eriam iriam aram eram iram avam em arem erem irem assem essem issem ado ido ando endo indo arão erão irão ar er ir as adas idas ias arias erias irias aras eras iras avas es ardes erdes irdes ares eres ires asses esses isses astes estes istes is ais eis areis ereis ireis áreis éreis íreis ásseis ésseis ísseis áveis íeis aríeis eríeis iríeis ados idos amos áramos éramos íramos ávamos íamos aríamos eríamos iríamos emos aremos eremos iremos ássemos êssemos íssemos imos armos ermos irmos ámos arás erás irás eu iu ou ará erá irá")
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
			portugueseCorpus(t, cases)
		})
	}
}

func portugueseCorpus(t *testing.T, cases [][2]string) {
	t.Helper()
	portugueseScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		regtsExec(t, p, b, c, "CREATE TEXT SEARCH DICTIONARY portuguese_corpus (TEMPLATE=snowball,Language=portuguese)", 0)
		const query = "SELECT ts_lexize('portuguese_corpus',$1::text) AS lexemes"
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
