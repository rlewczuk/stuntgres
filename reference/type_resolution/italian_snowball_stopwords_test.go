package reference_test

import (
	"database/sql"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceItalianSnowballStopwords(t *testing.T) {
	// Literal REL_18_6 italian.stop; no native resource is read as expected data.
	words := strings.Fields("ad al allo ai agli all agl alla alle con col coi da dal dallo dai dagli dall dagl dalla dalle di del dello dei degli dell degl della delle in nel nello nei negli nell negl nella nelle su sul sullo sui sugli sull sugl sulla sulle per tra contro io tu lui lei noi voi loro mio mia miei mie tuo tua tuoi tue suo sua suoi sue nostro nostra nostri nostre vostro vostra vostri vostre mi ti ci vi lo la li le gli ne il un uno una ma ed se perché anche come dov dove che chi cui non più quale quanto quanti quanta quante quello quelli quella quelle questo questi questa queste si tutto tutti a c e i l o ho hai ha abbiamo avete hanno abbia abbiate abbiano avrò avrai avrà avremo avrete avranno avrei avresti avrebbe avremmo avreste avrebbero avevo avevi aveva avevamo avevate avevano ebbi avesti ebbe avemmo aveste ebbero avessi avesse avessimo avessero avendo avuto avuta avuti avute sono sei è siamo siete sia siate siano sarò sarai sarà saremo sarete saranno sarei saresti sarebbe saremmo sareste sarebbero ero eri era eravamo eravate erano fui fosti fu fummo foste furono fossi fosse fossimo fossero essendo faccio fai facciamo fanno faccia facciate facciano farò farai farà faremo farete faranno farei faresti farebbe faremmo fareste farebbero facevo facevi faceva facevamo facevate facevano feci facesti fece facemmo faceste fecero facessi facesse facessimo facessero facendo sto stai sta stiamo stanno stia stiate stiano starò starai starà staremo starete staranno starei staresti starebbe staremmo stareste starebbero stavo stavi stava stavamo stavate stavano stetti stesti stette stemmo steste stettero stessi stesse stessimo stessero stando")
	if len(words) != 279 {
		t.Fatal("independent stopword inventory", len(words))
	}
	for start := 0; start < len(words); start += 24 {
		end := start + 24
		if end > len(words) {
			end = len(words)
		}
		group := words[start:end]
		t.Run(group[0], func(t *testing.T) {
			dictionaryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				q := "SELECT ts_lexize('italian_stem',$1::text) AS lexemes"
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
						t.Run(input, func(t *testing.T) {
							for _, e := range []fixture.Executor{c, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}} {
								dictionaryResult(t, p, b, e, q, "{}", input)
							}
						})
					}
				}
			})
		})
	}
	t.Run("resource-isolation", func(t *testing.T) {
		dictionaryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			for _, q := range []string{
				"CREATE TEXT SEARCH DICTIONARY it_simple (TEMPLATE=simple,StopWords=italian)",
				"CREATE TEXT SEARCH DICTIONARY it_plain (TEMPLATE=simple)",
				"CREATE TEXT SEARCH DICTIONARY it_cross (TEMPLATE=snowball,Language=english,StopWords=italian)",
				"CREATE TEXT SEARCH DICTIONARY it_free (TEMPLATE=snowball,Language=italian)",
			} {
				regtsExec(t, p, b, c, q, 0)
			}
			for _, tc := range [][3]string{
				{"it_simple", "perché", "{}"}, {"it_cross", "perché", "{}"}, {"it_plain", "perché", "{perché}"}, {"it_free", "perché", "{perc}"},
				{"it_simple", "CANI", "{cani}"}, {"it_cross", "RATS", "{rat}"}, {"it_free", "CANI", "{can}"},
				// Acute normalization happens after stop lookup; the resulting più
				// must remain a lexeme even though più is itself a stopword.
				{"italian_stem", "piú", "{più}"}, {"italian_stem", "più", "{}"},
				{"italian_stem", "PERCHé", "{}"}, {"italian_stem", "PERCHÉ", "{perchÉ}"},
				{"italian_stem", "perchè", "{perc}"}, {"italian_stem", "perche", "{perc}"},
			} {
				italianSmokeBoth(t, p, b, c, fixture.Select("SELECT ts_lexize($1::regdictionary,$2::text) AS lexemes", []string{"lexemes"}, []string{"_TEXT"}, [][]any{{tc[2]}}, tc[0], tc[1]))
			}
		})
	})
}
