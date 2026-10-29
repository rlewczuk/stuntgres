package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceFTSDebugPrepared(t *testing.T) {
	fixture.SchemaReferenceCleanup(t, "debug_moved")
	ftsParserScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		c := ftsInspectionConnection(t, p, b)
		run := func(q string) { fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(q, 0)) }
		defer c.ExecContext(p.Ctx, "RESET default_text_search_config")
		query := "SELECT * FROM ts_debug($1::text)"
		stmt, err := c.PrepareContext(p.Ctx, query)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: query}
		for _, config := range []string{"english", "simple", "english"} {
			run("SET default_text_search_config=" + config)
			want := ftsDebugEnglish()
			if config == "simple" {
				want = ftsDebugSimple()
			}
			fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Select(query, ftsDebugColumns, ftsDebugTypes, want, "The Rats"))
			ftsInspectionBoth(t, p, b, c, fixture.Select("SELECT * FROM ts_debug('english','The Rats')", ftsDebugColumns, ftsDebugTypes, ftsDebugEnglish()))
		}
		run("BEGIN")
		run("SET LOCAL default_text_search_config=simple")
		fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Select(query, ftsDebugColumns, ftsDebugTypes, ftsDebugSimple(), "The Rats"))
		run("ROLLBACK")
		fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Select(query, ftsDebugColumns, ftsDebugTypes, ftsDebugEnglish(), "The Rats"))
		run("CREATE TEXT SEARCH CONFIGURATION debug_live (COPY=english)")
		for _, sql := range []string{"SELECT * FROM ts_debug('debug_live',$1::text)", "SELECT * FROM ts_debug('debug_live','Rats')"} {
			statement, err := c.PrepareContext(p.Ctx, sql)
			if err != nil {
				t.Fatal(err)
			}
			owner := fixture.HeldStatement{Executor: c, Statement: statement, SQL: sql}
			args := []any(nil)
			if sql == "SELECT * FROM ts_debug('debug_live',$1::text)" {
				args = []any{"Rats"}
			}
			for _, dictionary := range []string{"english_stem", "simple", "english_stem"} {
				run("ALTER TEXT SEARCH CONFIGURATION debug_live ALTER MAPPING FOR asciiword WITH " + dictionary)
				lexemes := "{rat}"
				if dictionary == "simple" {
					lexemes = "{rats}"
				}
				fixture.RunStep(t, p.Ctx, owner, b.Recorder, fixture.Select(sql, ftsDebugColumns, ftsDebugTypes, [][]any{{"asciiword", "Word, all ASCII", "Rats", "{" + dictionary + "}", dictionary, lexemes}}, args...))
			}
			statement.Close()
		}
		run("CREATE TEXT SEARCH DICTIONARY debug_options (TEMPLATE=simple,ACCEPT=false)")
		run("ALTER TEXT SEARCH CONFIGURATION debug_live ALTER MAPPING FOR asciiword WITH debug_options")
		for _, sql := range []string{"SELECT token,lexemes FROM ts_debug('debug_live',$1::text)", "SELECT token,lexemes FROM ts_debug('debug_live','Rats')"} {
			statement, err := c.PrepareContext(p.Ctx, sql)
			if err != nil {
				t.Fatal(err)
			}
			owner := fixture.HeldStatement{Executor: c, Statement: statement, SQL: sql}
			var args []any
			if sql == "SELECT token,lexemes FROM ts_debug('debug_live',$1::text)" {
				args = []any{"Rats"}
			}
			for _, accept := range []bool{false, true, false} {
				option := "false"
				var lexemes any
				if accept {
					option = "true"
					lexemes = "{rats}"
				}
				run("ALTER TEXT SEARCH DICTIONARY debug_options (ACCEPT=" + option + ")")
				fixture.RunStep(t, p.Ctx, owner, b.Recorder, fixture.Select(sql, []string{"token", "lexemes"}, []string{"TEXT", "_TEXT"}, [][]any{{"Rats", lexemes}}, args...))
			}
			statement.Close()
		}
		run("ALTER TEXT SEARCH CONFIGURATION debug_live ALTER MAPPING FOR asciiword WITH english_stem")
		run("ALTER TEXT SEARCH CONFIGURATION debug_live RENAME TO debug_renamed")
		run("CREATE SCHEMA debug_moved")
		run("ALTER TEXT SEARCH CONFIGURATION debug_renamed SET SCHEMA debug_moved")
		ftsInspectionBoth(t, p, b, c, fixture.Select("SELECT token,lexemes FROM ts_debug('debug_moved.debug_renamed','Rats')", []string{"token", "lexemes"}, []string{"TEXT", "_TEXT"}, [][]any{{"Rats", "{rat}"}}))
		run("DROP TEXT SEARCH CONFIGURATION debug_moved.debug_renamed")
	})
}
