package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

// Historical literal pairs from snowball-data 8a2cdafcfde47bdb01bd9f5646a822fa73b94b75,
// lithuanian/{voc,output}.txt (Wikipedia-derived CC-BY-SA 3.0 corpus).
// Synthetic witnesses follow Snowball d19326ac6c1b9a417fc872f7c2f845265a5e9ece,
// the algorithm shipped in PostgreSQL REL_18_6, not Snowball 3.1.
func TestReferenceLithuanianSnowballMorphology(t *testing.T) {
	groups := []struct {
		name string
		pairs [][2]string
	}{
		{"historical", [][2]string{
			{"advokate", "advokat"}, {"advokatės", "advokat"}, {"akiniais", "akin"}, {"auginti", "aug"},
			{"brolis", "brol"}, {"bėgdamas", "bėg"}, {"dirbti", "dirb"}, {"draugauti", "draug"},
			{"gaidys", "gaid"}, {"galva", "galv"}, {"galvos", "galv"}, {"geresnis", "ger"},
			{"geriausias", "ger"}, {"gervė", "gerv"}, {"gervės", "gerv"}, {"ir", "ir"},
			{"keliai", "kel"}, {"kelias", "kel"}, {"lenkiškas", "lenk"}, {"lietuvaitės", "lietuvait"},
			{"seserimis", "seser"}, {"seseris", "seser"}, {"sesers", "ses"}, {"sūnus", "sūn"},
			{"the", "the"}, {"tvarkingas", "tvark"}, {"vandens", "vand"}, {"vanduo", "vand"},
			{"vyrai", "vyr"}, {"vyrams", "vyr"}, {"vyras", "vyr"}, {"vyrą", "vyr"},
			{"vyrų", "vyr"}, {"žaidžia", "žaid"},
		}},
		{"stages", [][2]string{
			{"bababingokas", "babab"}, {"bababčiai", "bababt"}, {"bababdžia", "bababd"},
			{"bababčingas", "bababt"}, {"bababdžingas", "bababd"}, {"bababgdas", "bababg"},
			{"beria", "ber"}, {"benimis", "ben"},
		}},
		{"conflicts", [][2]string{
			{"bababaite", "bababait"}, {"bababaitės", "bababait"},
			{"bababuote", "babab"}, {"bababuotės", "babab"}, {"bababėjime", "bababėjim"},
			{"bababesiu", "bababes"}, {"bababasius", "bababas"}, {"bababavime", "bababavim"},
			{"bababojime", "bababojim"}, {"bababokate", "bababokat"}, {"bababokatės", "bababokat"},
		}},
	}
	for _, group := range groups {
		t.Run(group.name, func(t *testing.T) { lithuanianCorpus(t, group.pairs) })
	}
}

func lithuanianCorpus(t *testing.T, pairs [][2]string) {
	t.Helper()
	dictionaryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		const q = "SELECT ts_lexize($1::regdictionary,$2::text) AS lexemes"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		for _, pair := range pairs {
			want := "{" + pair[1] + "}"
			if strings.ContainsAny(pair[1], " \t") {
				want = "{\"" + pair[1] + "\"}"
			}
			dictionaryResult(t, p, b, c, q, want, "lithuanian_stem", pair[0])
			for range 2 {
				dictionaryResult(t, p, b, held, q, want, "lithuanian_stem", pair[0])
			}
		}
	})
}

func TestReferenceLithuanianSnowballBoundaries(t *testing.T) {
	lithuanianCorpus(t, [][2]string{
		{"as", "as"}, {"aba", "ab"}, {"abė", "ab"}, {"bcdfgh", "bcdfgh"}, {"a", "a"}, {"ir", "ir"},
		{"VYRAS", "vyr"}, {"GERVė", "gerv"}, {"GERVĖ", "gervĖ"},
		{"gervė!", "gervė!"}, {"gerve\u0307", "gerve\u0307"}, {"café", "café"}, {"CAFÉS", "cafÉ"},
		{"ONLY", "onl"}, {"THE", "the"}, {"RUNNING", "runn"},
		{"parking'as", "parking'"}, {"babab'", "babab'"}, {"A B", "a b"},
		{"babab" + strings.Repeat("a", 993) + "as", "babab" + strings.Repeat("a", 993)},
		{"BABAB" + strings.Repeat("A", 994) + "as", "babab" + strings.Repeat("a", 994) + "as"},
		{"babab" + strings.Repeat("ą", 496) + "ias", "babab" + strings.Repeat("ą", 496)},
		{"BABAB" + strings.Repeat("ą", 496) + "aias", "babab" + strings.Repeat("ą", 496) + "aias"},
	})
}

func TestReferenceLithuanianSnowballStopwords(t *testing.T) {
	t.Run("english127", lithuanianEnglishStoplist)
	lithuanianCorpus(t, [][2]string{{"a", "a"}, {"ir", "ir"}, {"THE", "the"}, {"ONLY", "onl"}})
}
