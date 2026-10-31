package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePortugueseSnowballStopwords(t *testing.T) {
	// Literal independent REL_18_6 stopwords/portuguese.stop. In particular
	// the released spelling tém is not silently corrected to têm.
	words := strings.Fields(`de
a
o
que
e
do
da
em
um
para
com
não
uma
os
no
se
na
por
mais
as
dos
como
mas
ao
ele
das
à
seu
sua
ou
quando
muito
nos
já
eu
também
só
pelo
pela
até
isso
ela
entre
depois
sem
mesmo
aos
seus
quem
nas
me
esse
eles
você
essa
num
nem
suas
meu
às
minha
numa
pelos
elas
qual
nós
lhe
deles
essas
esses
pelas
este
dele
tu
te
vocês
vos
lhes
meus
minhas
teu
tua
teus
tuas
nosso
nossa
nossos
nossas
dela
delas
esta
estes
estas
aquele
aquela
aqueles
aquelas
isto
aquilo
estou
está
estamos
estão
estive
esteve
estivemos
estiveram
estava
estávamos
estavam
estivera
estivéramos
esteja
estejamos
estejam
estivesse
estivéssemos
estivessem
estiver
estivermos
estiverem
hei
há
havemos
hão
houve
houvemos
houveram
houvera
houvéramos
haja
hajamos
hajam
houvesse
houvéssemos
houvessem
houver
houvermos
houverem
houverei
houverá
houveremos
houverão
houveria
houveríamos
houveriam
sou
somos
são
era
éramos
eram
fui
foi
fomos
foram
fora
fôramos
seja
sejamos
sejam
fosse
fôssemos
fossem
for
formos
forem
serei
será
seremos
serão
seria
seríamos
seriam
tenho
tem
temos
tém
tinha
tínhamos
tinham
tive
teve
tivemos
tiveram
tivera
tivéramos
tenha
tenhamos
tenham
tivesse
tivéssemos
tivessem
tiver
tivermos
tiverem
terei
terá
teremos
terão
teria
teríamos
teriam`)
	for start := 0; start < len(words); start += 24 {
		end := start + 24
		if end > len(words) {
			end = len(words)
		}
		group := words[start:end]
		t.Run(group[0], func(t *testing.T) {
			portugueseScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				const q = "SELECT ts_lexize('portuguese_stem',$1::text) AS lexemes"
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
	t.Run("resource-isolation", func(t *testing.T) {
		portugueseScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			for _, q := range []string{
				"CREATE TEXT SEARCH DICTIONARY pt_simple (TEMPLATE=simple,StopWords=portuguese)",
				"CREATE TEXT SEARCH DICTIONARY pt_plain (TEMPLATE=simple)",
				"CREATE TEXT SEARCH DICTIONARY pt_cross (TEMPLATE=snowball,Language=english,StopWords=portuguese)",
				"CREATE TEXT SEARCH DICTIONARY pt_free (TEMPLATE=snowball,Language=portuguese)",
			} {
				regtsExec(t, p, b, c, q, 0)
			}
			for _, tc := range [][3]string{
				{"pt_simple", "não", "{}"}, {"pt_cross", "não", "{}"},
				{"pt_plain", "não", "{não}"}, {"pt_free", "não", "{nã}"},
				{"pt_simple", "CASAS", "{casas}"}, {"pt_cross", "RUNNING", "{run}"},
				{"pt_free", "RUNNING", "{running}"}, {"pt_free", "casas", "{cas}"},
				{"portuguese_stem", "não", "{}"}, {"portuguese_stem", "tem", "{}"},
				{"portuguese_stem", "tém", "{}"}, {"portuguese_stem", "têm", "{têm}"},
			} {
				portugueseSmokeBoth(t, p, b, c, fixture.Select("SELECT ts_lexize($1::regdictionary,$2::text) AS lexemes", []string{"lexemes"}, []string{"_TEXT"}, [][]any{{tc[2]}}, tc[0], tc[1]))
			}
		})
	})
}
