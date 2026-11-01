package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

// Historical pairs: snowball-data 4f982ec072a091771b321ab7861841471942e14a,
// russian/{voc,output}.txt, corresponding lines 1–65. Synthetic witnesses trace
// REL_18_6 stem_UTF_8_russian.c and its matching d19326a russian.sbl independently;
// neither expected stems nor suffix inventories come from native provider tables.
func TestReferenceRussianSnowballCorpus(t *testing.T) {
	t.Run("historical-vocabulary", func(t *testing.T) {
		russianCorpus(t, [][2]string{
			{"абиссинию", "абиссин"}, {"абонемента", "абонемент"},
			{"абонировался", "абонирова"}, {"абонируйся", "абонир"},
			{"абонируюсь", "абонир"}, {"абрикосы", "абрикос"},
			{"августа", "август"}, {"авдотье", "авдот"}, {"авиация", "авиац"},
			{"авось", "ав"}, {"австрийский", "австрийск"},
			{"автобиографию", "автобиограф"}, {"автомобили", "автомоб"},
			{"автомобиль", "автомобил"}, {"авторитеты", "авторитет"},
			{"агроном", "агрон"}, {"агрономии", "агроном"},
			{"административный", "административн"}, {"администратор", "администратор"},
			{"адресовано", "адресова"},
		})
	})
	// Each group has a separate bounded fixture. The prefix аб has RV after а;
	// an added а is therefore a lawful conditional predecessor inside RV.
	groups := []struct{ name, prefix, suffixes, stem string }{
		{"perfective-conditional", "аба", "в вши вшись", "аба"},
		{"perfective-unconditional", "аб", "ив ивши ившись ыв ывши ывшись", "аб"},
		{"adjective", "аб", "ее ие ые ое ими ыми ей ий ый ой ем им ым ом его ого ему ому их ых ую юю ая яя ою ею", "аб"},
		{"participle-conditional", "аба", "ем нн вш ющ щ", "аба"},
		{"participle-unconditional", "аб", "ивш ывш ующ", "аб"},
		{"verb-conditional", "аба", "ла на ете йте ли й л ем н ло но ет ют ны ть ешь нно", "аба"},
		{"verb-unconditional", "аб", "ила ыла ена ейте уйте ите или ыли ей уй ил ыл им ым ен ило ыло ено ят ует уют ит ыт ены ить ыть ишь ую ю", "аб"},
		{"noun", "аб", "а ев ов ие ье е иями ями ами еи ии и ией ей ой ий й иям ям ием ем ам ом о у ах иях ях ы ь ию ью ю ия ья я", "аб"},
	}
	for _, group := range groups {
		t.Run(group.name, func(t *testing.T) {
			var cases [][2]string
			for _, suffix := range strings.Fields(group.suffixes) {
				input := group.prefix + suffix
				if strings.HasPrefix(group.name, "participle-") {
					input += "ая" // adjective must succeed before participle processing.
				}
				cases = append(cases, [2]string{input, group.stem})
			}
			russianCorpus(t, cases)
		})
	}
}

func russianCorpus(t *testing.T, cases [][2]string) {
	t.Helper()
	portugueseScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		regtsExec(t, p, b, c, "CREATE TEXT SEARCH DICTIONARY russian_corpus (TEMPLATE=snowball,Language=russian)", 0)
		const query = "SELECT ts_lexize('russian_corpus',$1::text) AS lexemes"
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
