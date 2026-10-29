package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceFTSDebugTokens(t *testing.T) {
	ftsParserScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		ftsParserSelect(t, p, b, "SELECT * FROM ts_debug('english','The Rats')", ftsDebugColumns, ftsDebugTypes, ftsDebugEnglish())
		for _, tc := range []struct {
			document string
			tokens   []ftsParserToken
		}{
			{"foo-bar-beta1", []ftsParserToken{{15, "foo-bar-beta1"}, {11, "foo"}, {12, "-"}, {11, "bar"}, {12, "-"}, {9, "beta1"}}},
			{"http://example.com/path", []ftsParserToken{{14, "http://"}, {5, "example.com/path"}, {6, "example.com"}, {18, "/path"}}},
		} {
			rows := make([][]any, len(tc.tokens))
			descriptors := ftsParserDescriptors()
			for i, token := range tc.tokens {
				d := descriptors[token.id-1]
				rows[i] = []any{d[1], d[2], token.text, int64(i + 1)}
			}
			ftsParserSelect(t, p, b, "SELECT alias,description,token,ordinality FROM ts_debug('simple',"+ftsTypesQuote(tc.document)+") WITH ORDINALITY ORDER BY ordinality", []string{"alias", "description", "token", "ordinality"}, []string{"TEXT", "TEXT", "TEXT", "INT8"}, rows)
		}
	})
}
func TestReferenceFTSDebugDictionaries(t *testing.T) {
	ftsParserScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		c := ftsInspectionConnection(t, p, b)
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("CREATE TEXT SEARCH CONFIGURATION debug_mapping (COPY=simple)", 0))
		run(fixture.Exec("CREATE TEXT SEARCH DICTIONARY debug_stop (TEMPLATE=simple,STOPWORDS=english,ACCEPT=false)", 0))
		run(fixture.Exec("CREATE TEXT SEARCH DICTIONARY debug_reject (TEMPLATE=simple,ACCEPT=false)", 0))
		run(fixture.Exec("CREATE TEXT SEARCH DICTIONARY debug_reject2 (TEMPLATE=simple,ACCEPT=false)", 0))
		check := func(text, mapping string, dictionary, lexemes any) {
			ftsInspectionBoth(t, p, b, c, fixture.Select("SELECT dictionaries,dictionary,lexemes FROM ts_debug('debug_mapping',"+ftsTypesQuote(text)+")", ftsDebugColumns[3:], ftsDebugTypes[3:], [][]any{{mapping, dictionary, lexemes}}))
		}
		run(fixture.Exec("ALTER TEXT SEARCH CONFIGURATION debug_mapping ALTER MAPPING FOR asciiword WITH debug_stop,simple", 0))
		check("The", "{debug_stop,simple}", "debug_stop", "{}")
		check("Rats", "{debug_stop,simple}", "simple", "{rats}")
		run(fixture.Exec("ALTER TEXT SEARCH CONFIGURATION debug_mapping ALTER MAPPING FOR asciiword WITH simple,debug_stop", 0))
		check("The", "{simple,debug_stop}", "simple", "{the}")
		// D1: released SQL chooses the first mapping even when every result is NULL.
		run(fixture.Exec("ALTER TEXT SEARCH CONFIGURATION debug_mapping ALTER MAPPING FOR asciiword WITH debug_reject", 0))
		check("WORD", "{debug_reject}", "debug_reject", nil)
		run(fixture.Exec("ALTER TEXT SEARCH CONFIGURATION debug_mapping ALTER MAPPING FOR asciiword WITH debug_reject,debug_reject2", 0))
		check("WORD", "{debug_reject,debug_reject2}", "debug_reject", nil)
		run(fixture.Exec("ALTER TEXT SEARCH CONFIGURATION debug_mapping DROP MAPPING FOR asciiword", 0))
		check("WORD", "{}", nil, nil)
		for _, tc := range []struct {
			dictionary, text string
			want             any
		}{{"simple", "WORD", "{word}"}, {"debug_stop", "The", "{}"}, {"debug_reject", "WORD", nil}} {
			ftsInspectionBoth(t, p, b, c, fixture.Select("SELECT ts_lexize("+ftsTypesQuote(tc.dictionary)+","+ftsTypesQuote(tc.text)+")", []string{"ts_lexize"}, []string{"_TEXT"}, [][]any{{tc.want}}))
		}
		// D2: filtering is independently inspected, not fed through the converter.
		run(fixture.Exec("CREATE EXTENSION unaccent", 0))
		run(fixture.Exec("ALTER TEXT SEARCH CONFIGURATION debug_mapping ALTER MAPPING FOR word WITH unaccent,simple", 0))
		check("Hôtel", "{unaccent,simple}", "unaccent", "{Hotel}")
		run(fixture.Exec("ALTER TEXT SEARCH CONFIGURATION debug_mapping ALTER MAPPING FOR word WITH simple,unaccent", 0))
		check("Hôtel", "{simple,unaccent}", "simple", "{hôtel}")
	})
	t.Run("later_dictionary_error", func(t *testing.T) {
		ftsParserScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
			c := ftsInspectionConnection(t, p, b)
			run := func(q string) { fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(q, 0)) }
			run("CREATE TEXT SEARCH DICTIONARY debug_sub (TEMPLATE=simple,STOPWORDS=english)")
			run("CREATE TEXT SEARCH DICTIONARY debug_thes (TEMPLATE=thesaurus,DictFile=thesaurus_sample,Dictionary='public.debug_sub')")
			run("CREATE TEXT SEARCH CONFIGURATION debug_late (COPY=simple)")
			run("ALTER TEXT SEARCH CONFIGURATION debug_late ALTER MAPPING FOR asciiword WITH simple,debug_thes")
			run("DROP TEXT SEARCH DICTIONARY debug_sub")
			// D6: no dependency is recorded for the thesaurus's name-valued subdictionary.
			d := fixture.ControlsReferenceDiagnostic("42704", `text search dictionary "public.debug_sub" does not exist`)
			d.Context = `SQL function "ts_debug" statement 1`
			ftsInspectionDiagnostic(t, p, b, c, "SELECT * FROM ts_debug('debug_late','WORD')", d, false)
			ftsInspectionBoth(t, p, b, c, fixture.Select("SELECT * FROM ts_debug('simple','WORD')", ftsDebugColumns, ftsDebugTypes, [][]any{{"asciiword", "Word, all ASCII", "WORD", "{simple}", "simple", "{word}"}}))
		})
	})
}
