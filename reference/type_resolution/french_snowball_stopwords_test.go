package reference_test

import (
	"database/sql"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

// Verbatim independent REL_18_6 src/backend/snowball/stopwords/french.stop.
// In particular les, ils, elles, où and être are not entries in this release.
var frenchStopwordGoldens = strings.Fields(`au
aux
avec
ce
ces
dans
de
des
du
elle
en
et
eux
il
je
la
le
leur
lui
ma
mais
me
même
mes
moi
mon
ne
nos
notre
nous
on
ou
par
pas
pour
qu
que
qui
sa
se
ses
son
sur
ta
te
tes
toi
ton
tu
un
une
vos
votre
vous
c
d
j
l
à
m
n
s
t
y
été
étée
étées
étés
étant
étante
étants
étantes
suis
es
est
sommes
êtes
sont
serai
seras
sera
serons
serez
seront
serais
serait
serions
seriez
seraient
étais
était
étions
étiez
étaient
fus
fut
fûmes
fûtes
furent
sois
soit
soyons
soyez
soient
fusse
fusses
fût
fussions
fussiez
fussent
ayant
ayante
ayantes
ayants
eu
eue
eues
eus
ai
as
avons
avez
ont
aurai
auras
aura
aurons
aurez
auront
aurais
aurait
aurions
auriez
auraient
avais
avait
avions
aviez
avaient
eut
eûmes
eûtes
eurent
aie
aies
ait
ayons
ayez
aient
eusse
eusses
eût
eussions
eussiez
eussent`)

func TestReferenceFrenchSnowballStopwords(t *testing.T) {
	if len(frenchStopwordGoldens) != 155 {
		t.Fatal("independent released inventory", len(frenchStopwordGoldens))
	}
	for start := 0; start < len(frenchStopwordGoldens); start += 24 {
		end := start + 24
		if end > len(frenchStopwordGoldens) {
			end = len(frenchStopwordGoldens)
		}
		group := frenchStopwordGoldens[start:end]
		t.Run(group[0], func(t *testing.T) {
			dictionaryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				q := "SELECT ts_lexize('french_stem',$1::text) AS lexemes"
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
		dictionaryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			for _, q := range []string{
				"CREATE TEXT SEARCH DICTIONARY fr_english (TEMPLATE=snowball,Language=english,StopWords=english)",
				"CREATE TEXT SEARCH DICTIONARY fr_simple (TEMPLATE=simple,StopWords=french)",
				"CREATE TEXT SEARCH DICTIONARY fr_danish (TEMPLATE=snowball,Language=danish,StopWords=danish)",
				"CREATE TEXT SEARCH DICTIONARY fr_dutch (TEMPLATE=snowball,Language=dutch,StopWords=dutch)",
				"CREATE TEXT SEARCH DICTIONARY fr_french (TEMPLATE=snowball,Language=french,StopWords=french)",
				"CREATE TEXT SEARCH DICTIONARY fr_cross (TEMPLATE=snowball,Language=english,StopWords=french)",
				"CREATE TEXT SEARCH DICTIONARY fr_free (TEMPLATE=snowball,Language='FrEnCh')",
			} {
				regtsExec(t, p, b, c, q, 0)
			}
			// Revisit previously initialized dictionaries after alternating providers and resources.
			for _, tc := range []struct{ name, input, want string }{
				{"fr_english", "THE", "{}"}, {"fr_simple", "été", "{}"}, {"fr_danish", "OG", "{}"},
				{"fr_dutch", "DE", "{}"}, {"fr_french", "CHEVAUX", "{cheval}"}, {"fr_cross", "été", "{}"},
				{"fr_simple", "CHEVAUX", "{chevaux}"}, {"fr_cross", "CHEVAUX", "{chevaux}"},
				{"fr_free", "avions", "{avion}"}, {"fr_french", "avions", "{}"},
				{"fr_french", "les", "{le}"}, {"fr_french", "où", "{où}"},
				{"fr_french", "l'été", "{été}"}, {"fr_french", "été", "{}"}, {"fr_french", "éTé", "{}"},
				{"fr_french", "ÉTÉ", "{ÉtÉ}"}, {"fr_english", "THE", "{}"}, {"fr_simple", "été", "{}"},
				{"fr_danish", "OG", "{}"}, {"fr_dutch", "DE", "{}"}, {"fr_french", "CHEVAUX", "{cheval}"},
			} {
				q := "SELECT ts_lexize($1::regdictionary,$2::text) AS lexemes"
				stmt, err := c.PrepareContext(p.Ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				for _, e := range []fixture.Executor{c, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}} {
					dictionaryResult(t, p, b, e, q, tc.want, tc.name, tc.input)
				}
				stmt.Close()
			}
		})
	})
}
