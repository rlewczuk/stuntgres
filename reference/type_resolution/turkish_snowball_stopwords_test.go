package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceTurkishSnowballStopwords(t *testing.T) {
	// Exact independent REL_18_6 stopwords/turkish.stop, including birşey.
	words := strings.Fields("acaba ama aslında az bazı belki biri birkaç birşey biz bu çok çünkü da daha de defa diye eğer en gibi hem hep hepsi her hiç için ile ise kez ki kim mı mu mü nasıl ne neden nerde nerede nereye niçin niye o sanki şey siz şu tüm ve veya ya yani")
	if len(words) != 53 {
		t.Fatal("independent stoplist length", len(words))
	}
	for start := 0; start < len(words); start += 18 {
		end := start + 18
		if end > len(words) {
			end = len(words)
		}
		group := words[start:end]
		t.Run(group[0], func(t *testing.T) {
			turkishScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				const q = "SELECT ts_lexize('turkish_stem',$1::text) AS lexemes"
				stmt, err := c.PrepareContext(p.Ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				for _, word := range group {
					upper := strings.Map(func(r rune) rune {
						if r >= 'a' && r <= 'z' {
							return r - 'a' + 'A'
						}
						return r
					}, word)
					for _, input := range []string{word, upper} {
						for _, e := range []fixture.Executor{c, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}} {
							dictionaryResult(t, p, b, e, q, "{}", input)
						}
					}
				}
			})
		})
	}
	t.Run("precheck-and-resource-independence", func(t *testing.T) {
		turkishScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			for _, q := range []string{
				"CREATE TEXT SEARCH DICTIONARY tr_simple (TEMPLATE=simple,StopWords=turkish)",
				"CREATE TEXT SEARCH DICTIONARY tr_cross (TEMPLATE=snowball,Language=english,StopWords=turkish)",
				"CREATE TEXT SEARCH DICTIONARY tr_free (TEMPLATE=snowball,Language=turkish)",
				"CREATE TEXT SEARCH DICTIONARY tr_empty (TEMPLATE=snowball,Language=turkish,StopWords='')",
				"CREATE TEXT SEARCH DICTIONARY tr_english (TEMPLATE=snowball,Language=turkish,StopWords=english)",
			} {
				regtsExec(t, p, b, c, q, 0)
			}
			for _, tc := range [][3]string{
				{"turkish_stem", "mi", "{mi}"}, {"turkish_stem", "bir", "{bir}"},
				{"turkish_stem", "cok", "{cok}"}, {"turkish_stem", "ÇOK", "{Çok}"},
				{"turkish_stem", "bize", "{biz}"}, {"turkish_stem", "biz", "{}"},
				{"tr_simple", "BIZ", "{}"}, {"tr_cross", "BIZ", "{}"},
				{"tr_simple", "KITAPLAR", "{kitaplar}"}, {"tr_cross", "RUNNING", "{run}"},
				{"tr_free", "biz", "{biz}"}, {"tr_empty", "biz", "{biz}"},
				{"tr_english", "ONLY", "{}"}, {"turkish_stem", "ONLY", "{only}"},
				{"tr_free", "KITAPLAR", "{kitap}"}, {"tr_empty", "KITAPLAR", "{kitap}"},
			} {
				turkishSmokeBoth(t, p, b, c, fixture.Select("SELECT ts_lexize($1::regdictionary,$2::text) AS lexemes", []string{"lexemes"}, []string{"_TEXT"}, [][]any{{tc[2]}}, tc[0], tc[1]))
			}
		})
	})
}
