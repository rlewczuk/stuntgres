package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

// REL_18_6 spell.c:NINormalizeWord assigns a distinct variant to each
// standalone normalization, and one shared variant to each compound's stems.
// SQL query text must retain that OR-of-ANDs contract, while ts_lexize keeps
// its ordinary ordered, flattened public text[] representation.
func TestReferenceIspellConfigurationVariants(t *testing.T) {
	ispellScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(step fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, step) }
		ispellCreate(t, p, b, c, "ispell_variants_dictionary", "legacy", "")
		ispellCreate(t, p, b, c, "ispell_variants_stopped", "legacy", ",StopWords=tr_ispell_book")
		for _, definition := range []struct{ name, dictionary string }{
			{"ispell_variants", "ispell_variants_dictionary"},
			{"ispell_variants_stop", "ispell_variants_stopped"},
		} {
			run(fixture.Exec("CREATE TEXT SEARCH CONFIGURATION "+definition.name+" (COPY=pg_catalog.simple)", 0))
			run(fixture.Exec("ALTER TEXT SEARCH CONFIGURATION "+definition.name+" ALTER MAPPING FOR asciiword,word WITH "+definition.dictionary+",pg_catalog.simple", 0))
		}

		const normalization = "SELECT to_tsquery($1::regconfig,$2::text)::text AS normalized_query,to_tsvector($1::regconfig,$2::text)::text AS normalized_vector"
		const lexical = "SELECT ts_lexize($1::regdictionary,$2::text) AS lexemes"
		const vector = "SELECT to_tsvector($1::regconfig,$2::text)::text AS normalized_vector"
		const representation = "SELECT to_tsquery('ispell_variants'::regconfig,$1::text)::text=$2::text AS represented"
		statements := make(map[string]*sql.Stmt)
		for _, query := range []string{normalization, lexical, vector, representation} {
			statement, err := c.PrepareContext(p.Ctx, query)
			if err != nil {
				t.Fatal(err)
			}
			defer statement.Close()
			statements[query] = statement
		}
		both := func(step fixture.Step) {
			run(step)
			held := fixture.HeldStatement{Executor: c, Statement: statements[step.Query], SQL: step.Query}
			fixture.RunStep(t, p.Ctx, held, b.Recorder, step)
		}
		for _, token := range []struct {
			input, query, vector, lexemes string
		}{
			{"book", "'book'", "'book':1", "{book}"},
			{"skies", "'sky'", "'sky':1", "{sky}"},
			{"booking", "'booking' | 'book'", "'book':1 'booking':1", "{booking,book}"},
			{"footklubber", "'foot' & 'klubber'", "'foot':1 'klubber':1", "{foot,klubber}"},
			{"footballklubber", "'footballklubber' | 'foot' & 'ball' & 'klubber' | 'football' & 'klubber'", "'ball':1 'foot':1 'football':1 'footballklubber':1 'klubber':1", "{footballklubber,foot,ball,klubber,football,klubber}"},
		} {
			both(fixture.Select(normalization, []string{"normalized_query", "normalized_vector"}, []string{"TEXT", "TEXT"}, [][]any{{token.query, token.vector}}, "ispell_variants", token.input))
			// Keep the existing SQL-array contract independently alongside rich
			// configuration consumers; it must not acquire variant markers.
			for _, executor := range []fixture.Executor{c, fixture.HeldStatement{Executor: c, Statement: statements[lexical], SQL: lexical}} {
				dictionaryResult(t, p, b, executor, lexical, token.lexemes, "ispell_variants_dictionary", token.input)
			}
		}

		// Supported TEXT equality distinguishes alternatives from conjunctive
		// stems, without requiring the unrelated tsvector/tsquery match operator.
		for _, probe := range []struct {
			token, text string
			equal       bool
		}{
			{"booking", "'booking' | 'book'", true},
			{"booking", "'booking' & 'book'", false},
			{"footklubber", "'foot' & 'klubber'", true},
			{"footklubber", "'foot' | 'klubber'", false},
			{"footballklubber", "'footballklubber' | 'foot' & 'ball' & 'klubber' | 'football' & 'klubber'", true},
			{"footballklubber", "'footballklubber' & 'foot' & 'ball' & 'klubber' & 'football' & 'klubber'", false},
		} {
			both(fixture.Select(representation, []string{"represented"}, []string{"BOOL"}, [][]any{{probe.equal}}, probe.token, probe.text))
		}

		// Recognized stopwords suppress fallback; unknown words permit simple.
		// Surviving booking alternatives and vector gaps are independently fixed.
		for _, executor := range []fixture.Executor{c, fixture.HeldStatement{Executor: c, Statement: statements[lexical], SQL: lexical}} {
			dictionaryResult(t, p, b, executor, lexical, "{}", "ispell_variants_stopped", "book")
			dictionaryResult(t, p, b, executor, lexical, "{booking}", "ispell_variants_stopped", "booking")
			dictionaryResult(t, p, b, executor, lexical, nil, "ispell_variants_stopped", "zzzzunknown")
		}
		both(fixture.Select(normalization, []string{"normalized_query", "normalized_vector"}, []string{"TEXT", "TEXT"}, [][]any{{"'zzzzunknown'", "'zzzzunknown':1"}}, "ispell_variants_stop", "zzzzunknown"))
		both(fixture.Select(normalization, []string{"normalized_query", "normalized_vector"}, []string{"TEXT", "TEXT"}, [][]any{{"'booking'", "'booking':1"}}, "ispell_variants_stop", "booking"))
		both(fixture.Select(vector, []string{"normalized_vector"}, []string{"TEXT"}, [][]any{{""}}, "ispell_variants_stop", "book"))
		both(fixture.Select(vector, []string{"normalized_vector"}, []string{"TEXT"}, [][]any{{"'booking':3 'zzzzunknown':2"}}, "ispell_variants_stop", "book zzzzunknown booking"))
		run(fixture.Select("SELECT 11 AS healthy", []string{"healthy"}, []string{"INT4"}, [][]any{{int64(11)}}))
	})
}
