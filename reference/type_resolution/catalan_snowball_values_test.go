package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

// Release-source corpus; cases→case and gosses→gos are pinned PostgreSQL18.6 observations.
// gossos→goss follows the released residual os deletion, which runs only once.
func TestReferenceCatalanSnowballMorphology(t *testing.T) {
	groups := [][][2]string{
		{{"gat", "gat"}, {"gats", "gat"}, {"GATS", "gat"}, {"casa", "cas"}, {"cases", "case"}, {"gossos", "goss"}, {"gosses", "gos"}, {"homes", "hom"}, {"dones", "don"}, {"llibres", "llibr"}, {"català", "catal"}, {"catalana", "catal"}, {"catalans", "catalan"}, {"catalanes", "catalan"}, {"café", "caf"}, {"cafés", "caf"}, {"cafès", "caf"}, {"qüestió", "quest"}, {"qüestions", "quest"}, {"col·laboracions", "col.labor"}},
		{{"babab-la", "babab"}, {"babab'ls", "babab"}, {"bababselas", "babab"}, {"babab'ho", "babab"}, {"babab’ho", "babab’"}, {"basela", "basel"}, {"babab-me-me", "babab-m"}, {"bababaments", "babab"}, {"bababacions", "babab"}, {"babablogies", "babablog"}, {"bababica", "bababic"}, {"bababquíssima", "bababc"}, {"babada", "bab"}, {"babacions", "babacion"}, {"bababarem", "babab"}, {"bababiré", "babab"}, {"bababàvem", "babab"}, {"bababïsquen", "babab"}, {"bababando", "babab"}, {"babando", "baband"}, {"bababiqu", "bababic"}, {"bababaos", "bababa"}, {"bababamentament", "bababament"}},
	}
	for i, group := range groups {
		t.Run(string(rune('A'+i)), func(t *testing.T) {
			dictionaryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				regtsExec(t, p, b, c, "CREATE TEXT SEARCH DICTIONARY catalan_rules (TEMPLATE=pg_catalog.snowball,Language='CaTaLaN')", 0)
				for _, name := range []string{"pg_catalog.catalan_stem", "catalan_rules"} {
					q := "SELECT ts_lexize('" + name + "'::regdictionary,$1::text) AS lexemes"
					stmt, err := c.PrepareContext(p.Ctx, q)
					if err != nil {
						t.Fatal(err)
					}
					for _, tc := range group {
						t.Run(tc[0], func(t *testing.T) {
							dictionaryResult(t, p, b, c, q, "{"+tc[1]+"}", tc[0])
							dictionaryResult(t, p, b, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}, q, "{"+tc[1]+"}", tc[0])
							literal := strings.ReplaceAll(tc[0], "'", "''")
							dictionaryResult(t, p, b, c, "SELECT ts_lexize('"+name+"','"+literal+"') AS lexemes", "{"+tc[1]+"}")
						})
					}
					stmt.Close()
				}
			})
		})
	}
}

func TestReferenceCatalanSnowballBoundaries(t *testing.T) {
	dictionaryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		q := "SELECT ts_lexize('catalan_stem',$1::text) AS lexemes"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		for _, tc := range [][2]string{
			{"THE", "the"}, {"ONLY", "only"}, {"IS", "is"}, {"de", "de"}, {"i", "i"}, {"la", "la"}, {"les", "les"}, {"els", "el"},
			{"CAFÉ", "cafÉ"}, {"CAFÉS", "cafÉ"}, {"cafe\u0301", "cafe\u0301"}, {"gats!", "gats!"}, {"ç", "ç"}, {"À", "À"}, {"à", "a"}, {"l·l", "l.l"}, {"bíbas", "bib"}, {"bìbas", "bibas"},
			{"bab" + strings.Repeat("é", 498) + "x", "bab" + strings.Repeat("e", 498) + "x"},
			{"BAB" + strings.Repeat("é", 498) + "xx", "bab" + strings.Repeat("é", 498) + "xx"},
		} {
			dictionaryResult(t, p, b, c, q, "{"+tc[1]+"}", tc[0])
			dictionaryResult(t, p, b, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}, q, "{"+tc[1]+"}", tc[0])
		}
	})
}
