package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceSpanishSnowballStopwords(t *testing.T) {
	// Independent literal REL_18_6 spanish.stop: all 313 entries, including
	// vosostros/vosostras and the released sintiendo/sentido family.
	words := strings.Fields(`de
la
que
el
en
y
a
los
del
se
las
por
un
para
con
no
una
su
al
lo
como
más
pero
sus
le
ya
o
este
sí
porque
esta
entre
cuando
muy
sin
sobre
también
me
hasta
hay
donde
quien
desde
todo
nos
durante
todos
uno
les
ni
contra
otros
ese
eso
ante
ellos
e
esto
mí
antes
algunos
qué
unos
yo
otro
otras
otra
él
tanto
esa
estos
mucho
quienes
nada
muchos
cual
poco
ella
estar
estas
algunas
algo
nosotros
mi
mis
tú
te
ti
tu
tus
ellas
nosotras
vosostros
vosostras
os
mío
mía
míos
mías
tuyo
tuya
tuyos
tuyas
suyo
suya
suyos
suyas
nuestro
nuestra
nuestros
nuestras
vuestro
vuestra
vuestros
vuestras
esos
esas
estoy
estás
está
estamos
estáis
están
esté
estés
estemos
estéis
estén
estaré
estarás
estará
estaremos
estaréis
estarán
estaría
estarías
estaríamos
estaríais
estarían
estaba
estabas
estábamos
estabais
estaban
estuve
estuviste
estuvo
estuvimos
estuvisteis
estuvieron
estuviera
estuvieras
estuviéramos
estuvierais
estuvieran
estuviese
estuvieses
estuviésemos
estuvieseis
estuviesen
estando
estado
estada
estados
estadas
estad
he
has
ha
hemos
habéis
han
haya
hayas
hayamos
hayáis
hayan
habré
habrás
habrá
habremos
habréis
habrán
habría
habrías
habríamos
habríais
habrían
había
habías
habíamos
habíais
habían
hube
hubiste
hubo
hubimos
hubisteis
hubieron
hubiera
hubieras
hubiéramos
hubierais
hubieran
hubiese
hubieses
hubiésemos
hubieseis
hubiesen
habiendo
habido
habida
habidos
habidas
soy
eres
es
somos
sois
son
sea
seas
seamos
seáis
sean
seré
serás
será
seremos
seréis
serán
sería
serías
seríamos
seríais
serían
era
eras
éramos
erais
eran
fui
fuiste
fue
fuimos
fuisteis
fueron
fuera
fueras
fuéramos
fuerais
fueran
fuese
fueses
fuésemos
fueseis
fuesen
sintiendo
sentido
sentida
sentidos
sentidas
siente
sentid
tengo
tienes
tiene
tenemos
tenéis
tienen
tenga
tengas
tengamos
tengáis
tengan
tendré
tendrás
tendrá
tendremos
tendréis
tendrán
tendría
tendrías
tendríamos
tendríais
tendrían
tenía
tenías
teníamos
teníais
tenían
tuve
tuviste
tuvo
tuvimos
tuvisteis
tuvieron
tuviera
tuvieras
tuviéramos
tuvierais
tuvieran
tuviese
tuvieses
tuviésemos
tuvieseis
tuviesen
teniendo
tenido
tenida
tenidos
tenidas
tened`)
	for start := 0; start < len(words); start += 24 {
		end := start + 24
		if end > len(words) {
			end = len(words)
		}
		group := words[start:end]
		t.Run(group[0], func(t *testing.T) {
			spanishScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				const q = "SELECT ts_lexize('spanish_stem',$1::text) AS lexemes"
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
		spanishScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			for _, q := range []string{
				"CREATE TEXT SEARCH DICTIONARY es_simple (TEMPLATE=simple,StopWords=spanish)",
				"CREATE TEXT SEARCH DICTIONARY es_plain (TEMPLATE=simple)",
				"CREATE TEXT SEARCH DICTIONARY es_cross (TEMPLATE=snowball,Language=english,StopWords=spanish)",
				"CREATE TEXT SEARCH DICTIONARY es_free (TEMPLATE=snowball,Language=spanish)",
			} {
				regtsExec(t, p, b, c, q, 0)
			}
			for _, tc := range [][3]string{
				{"es_simple", "más", "{}"}, {"es_cross", "más", "{}"},
				{"es_plain", "más", "{más}"}, {"es_free", "más", "{mas}"},
				{"es_simple", "CASAS", "{casas}"}, {"es_cross", "RUNNING", "{run}"},
				{"es_free", "RUNNING", "{running}"}, {"es_free", "casas", "{cas}"},
				{"spanish_stem", "más", "{}"}, {"spanish_stem", "vosostros", "{}"},
				{"spanish_stem", "sintiendo", "{}"}, {"spanish_stem", "vosotros", "{vosotr}"},
			} {
				spanishSmokeBoth(t, p, b, c, fixture.Select("SELECT ts_lexize($1::regdictionary,$2::text) AS lexemes", []string{"lexemes"}, []string{"_TEXT"}, [][]any{{tc[2]}}, tc[0], tc[1]))
			}
		})
	})
}
