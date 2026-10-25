package reference_test

import (
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceFTSOpsGeneratedNotControls(t *testing.T) {
	ftsOpsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
		run(fixture.Exec("CREATE TABLE fts_ops_generated_not(id integer,q tsquery,v tsvector)", 0))
		expression := "'a'::tsquery"
		for range 64 {
			expression = "!!(" + expression + ")"
		}
		run(fixture.Exec("INSERT INTO fts_ops_generated_not VALUES(1,"+expression+",'a:1')", 1))
		want := strings.Repeat("!", 64) + "'a'"
		saved := fixture.Select("SELECT id,q,v FROM fts_ops_generated_not ORDER BY id", []string{"id", "q", "v"}, []string{"INT4", "TSQUERY", "TSVECTOR"}, [][]any{{int64(1), want, "'a':1"}})
		run(saved)
		run(fixture.Prepared(saved))
		if !b.PG {
			ftsOpsNativeControls(t, p, b, "fts_ops_generated_not", saved)
		}
	})
}
