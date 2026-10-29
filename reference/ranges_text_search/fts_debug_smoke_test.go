package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceFTSDebugStatFeatureSmoke(t *testing.T) {
	ftsParserScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		c := ftsInspectionConnection(t, p, b)
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		defer c.ExecContext(p.Ctx, "RESET default_text_search_config")
		ftsInspectionBoth(t, p, b, c, fixture.Select("SELECT * FROM ts_debug('english','The Rats')", ftsDebugColumns, ftsDebugTypes, ftsDebugEnglish()))
		query := "SELECT * FROM ts_debug($1::text)"
		stmt, err := c.PrepareContext(p.Ctx, query)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: query}
		for _, config := range []string{"english", "simple"} {
			run(fixture.Exec("SET default_text_search_config="+config, 0))
			want := ftsDebugEnglish()
			if config == "simple" {
				want = ftsDebugSimple()
			}
			fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Select(query, ftsDebugColumns, ftsDebugTypes, want, "The Rats"))
			ftsInspectionBoth(t, p, b, c, fixture.Select("SELECT * FROM ts_debug('english','The Rats')", ftsDebugColumns, ftsDebugTypes, ftsDebugEnglish()))
		}
		ftsStatSetup(t, p, b, c)
		statSQL := "SELECT * FROM ts_stat($1::text) ORDER BY word"
		stat, err := c.PrepareContext(p.Ctx, statSQL)
		if err != nil {
			t.Fatal(err)
		}
		defer stat.Close()
		statHeld := fixture.HeldStatement{Executor: c, Statement: stat, SQL: statSQL}
		inner := "SELECT vector FROM fts_stat_documents"
		ftsInspectionBoth(t, p, b, c, fixture.Select(statSQL, ftsStatColumns, ftsStatTypes, ftsStatBase(), inner))
		fixture.RunStep(t, p.Ctx, statHeld, b.Recorder, fixture.Select(statSQL, ftsStatColumns, ftsStatTypes, ftsStatBase(), inner))
		ftsInspectionBoth(t, p, b, c, fixture.Select("SELECT * FROM ts_stat($1::text,'ab') ORDER BY word", ftsStatColumns, ftsStatTypes, [][]any{{"cat", int64(1), int64(2)}, {"dog", int64(1), int64(1)}}, inner))
		run(fixture.Exec("UPDATE fts_stat_documents SET vector='cat:1A' WHERE id=2", 1))
		changed := [][]any{{"cat", int64(2), int64(3)}, {"dog", int64(1), int64(1)}, {"solo", int64(1), int64(1)}}
		fixture.RunStep(t, p.Ctx, statHeld, b.Recorder, fixture.Select(statSQL, ftsStatColumns, ftsStatTypes, changed, inner))
		shape := fixture.ControlsReferenceDiagnostic("22023", "ts_stat query must return one tsvector column")
		ftsInspectionDiagnostic(t, p, b, c, "SELECT * FROM ts_stat('SELECT 1')", shape, false)
		missing := fixture.ControlsReferenceDiagnostic("42703", `column "missing" does not exist`)
		missing.InternalPosition = "8"
		missing.InternalQuery = "SELECT missing::tsvector"
		ftsInspectionDiagnostic(t, p, b, c, "SELECT * FROM ts_stat('SELECT missing::tsvector')", missing, false)
		fixture.RunStep(t, p.Ctx, statHeld, b.Recorder, fixture.Select(statSQL, ftsStatColumns, ftsStatTypes, changed, inner))
		for _, s := range []fixture.Step{fixture.Select("SELECT * FROM ts_debug(NULL::text)", ftsDebugColumns, ftsDebugTypes, nil), fixture.Select("SELECT * FROM ts_stat(NULL::text)", ftsStatColumns, ftsStatTypes, nil)} {
			ftsInspectionBoth(t, p, b, c, s)
		}
		run(fixture.Exec("CREATE TEXT SEARCH DICTIONARY debug_smoke_stop (TEMPLATE=simple,STOPWORDS=english,ACCEPT=false)", 0))
		ftsInspectionBoth(t, p, b, c, fixture.Select("SELECT ts_lexize('simple','WORD') AS known,ts_lexize('debug_smoke_stop','The') AS stop,ts_lexize('debug_smoke_stop','WORD') AS unknown", []string{"known", "stop", "unknown"}, []string{"_TEXT", "_TEXT", "_TEXT"}, [][]any{{"{word}", "{}", nil}}))
	})
}
