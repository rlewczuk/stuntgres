package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceThesaurusPrepared(t *testing.T) {
	thesaurusScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		thesaurusSetup(t, p, b, c, "thesaurus_sample")
		const vectorSQL = "SELECT to_tsvector('public.thes_cfg',$1::text) AS normalized"
		const lexizeSQL = "SELECT ts_lexize('public.thes_dict',$1::text) AS lexemes"
		vector, err := c.PrepareContext(p.Ctx, vectorSQL)
		if err != nil {
			t.Fatal(err)
		}
		defer vector.Close()
		lexize, err := c.PrepareContext(p.Ctx, lexizeSQL)
		if err != nil {
			t.Fatal(err)
		}
		defer lexize.Close()
		literal, err := c.PrepareContext(p.Ctx, "SELECT to_tsvector('public.thes_cfg','one two three') AS normalized")
		if err != nil {
			t.Fatal(err)
		}
		defer literal.Close()
		scalarLiteral, err := c.PrepareContext(p.Ctx, "SELECT ts_lexize('public.thes_dict','one') AS lexemes")
		if err != nil {
			t.Fatal(err)
		}
		defer scalarLiteral.Close()
		run := func(step fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, step) }
		retainedConstants := func() {
			t.Helper()
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: literal, SQL: "SELECT to_tsvector('public.thes_cfg','one two three') AS normalized"}, b.Recorder, ftsConfigurationVector("SELECT to_tsvector('public.thes_cfg','one two three') AS normalized", "'123':1"))
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: scalarLiteral, SQL: "SELECT ts_lexize('public.thes_dict','one') AS lexemes"}, b.Recorder, fixture.Select("SELECT ts_lexize('public.thes_dict','one') AS lexemes", []string{"lexemes"}, []string{"_TEXT"}, [][]any{{"{1}"}}))
		}
		observe := func(booking string, diagnostic *fixture.Diagnostic) {
			t.Helper()
			for _, tc := range []struct{ input, want string }{{"one", "'1':1"}, {"one two three", "'123':1"}, {"booking the tickets", booking}} {
				step := ftsConfigurationVector(vectorSQL, tc.want, tc.input)
				if diagnostic != nil {
					step = fixture.ErrorDiagnostic(vectorSQL, *diagnostic, tc.input)
				}
				fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: vector, SQL: vectorSQL}, b.Recorder, step)
				step.Prepared = true
				run(step)
			}
			scalar := fixture.Select(lexizeSQL, []string{"lexemes"}, []string{"_TEXT"}, [][]any{{"{1}"}}, "one")
			if diagnostic != nil {
				scalar = fixture.ErrorDiagnostic(lexizeSQL, *diagnostic, "one")
			}
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: lexize, SQL: lexizeSQL}, b.Recorder, scalar)
			scalar.Prepared = true
			run(scalar)
			fresh, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer fresh.Close()
			step := ftsConfigurationVector(vectorSQL, "'123':1", "one two three")
			if diagnostic != nil {
				step = fixture.ErrorDiagnostic(vectorSQL, *diagnostic, "one two three")
			}
			step.Prepared = true
			fixture.RunStep(t, p.Ctx, fresh, b.Recorder, step)
		}
		const booking = "'cards':3 'invitation':2 'order':1"
		observe(booking, nil)
		retainedConstants()
		run(fixture.Exec("ALTER TEXT SEARCH DICTIONARY public.thes_sub (Accept=false)", 0))
		rejected := thesaurusDiagnostic("F0000", `thesaurus sample word "one" isn't recognized by subdictionary (rule 1)`)
		observe("", &rejected)
		retainedConstants()
		for _, query := range []string{"SELECT ts_lexize('public.thes_dict','one') AS lexemes", "SELECT to_tsvector('public.thes_cfg','one two three') AS normalized"} {
			run(fixture.ErrorDiagnostic(query, rejected))
			run(fixture.Prepared(fixture.ErrorDiagnostic(query, rejected)))
		}
		run(fixture.Exec("ALTER TEXT SEARCH DICTIONARY public.thes_sub (Accept=true)", 0))
		observe(booking, nil)
		retainedConstants()
		run(fixture.Exec("ALTER TEXT SEARCH DICTIONARY public.thes_dict (DictFile=stuntgres_thes_ties)", 0))
		retainedConstants()
		for _, prepared := range []bool{false, true} {
			scalar := fixture.Select("SELECT ts_lexize('public.thes_dict','one') AS lexemes", []string{"lexemes"}, []string{"_TEXT"}, [][]any{{nil}})
			scalar.Prepared = prepared
			run(scalar)
			freshVector := ftsConfigurationVector("SELECT to_tsvector('public.thes_cfg','one two three') AS normalized", "'one':1 'three':3 'two':2")
			freshVector.Prepared = prepared
			run(freshVector)
		}
		fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: vector, SQL: vectorSQL}, b.Recorder, ftsConfigurationVector(vectorSQL, "'last':1", "alpha beta"))
		fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: lexize, SQL: lexizeSQL}, b.Recorder, fixture.Select(lexizeSQL, []string{"lexemes"}, []string{"_TEXT"}, [][]any{{nil}}, "one"))
		// Isolate the pinned own-option fold contract before the separate
		// mapping/drop/recreate lifecycle below changes the resolution environment.
		run(fixture.Exec("ALTER TEXT SEARCH DICTIONARY public.thes_dict (DictFile=thesaurus_sample)", 0))
		run(fixture.Exec("ALTER TEXT SEARCH DICTIONARY public.thes_sub (StopWords)", 0))
		observe("'booking':1 'the':2 'tickets':3", nil)
		run(fixture.Exec("ALTER TEXT SEARCH DICTIONARY public.thes_sub (StopWords=english)", 0))
		observe(booking, nil)
		var before, after int64
		if err := c.QueryRowContext(p.Ctx, "SELECT 'public.thes_sub'::regdictionary::oid::bigint").Scan(&before); err != nil {
			t.Fatal(err)
		}
		run(fixture.Exec("ALTER TEXT SEARCH CONFIGURATION public.thes_cfg ALTER MAPPING FOR asciiword WITH public.thes_dict", 0))
		run(fixture.Exec("DROP TEXT SEARCH DICTIONARY public.thes_sub", 0))
		missing := thesaurusDiagnostic("42704", `text search dictionary "public.thes_sub" does not exist`)
		observe("", &missing)
		run(fixture.Exec("CREATE TEXT SEARCH DICTIONARY public.thes_sub (TEMPLATE=pg_catalog.simple,STOPWORDS=english)", 0))
		if err := c.QueryRowContext(p.Ctx, "SELECT 'public.thes_sub'::regdictionary::oid::bigint").Scan(&after); err != nil {
			t.Fatal(err)
		}
		if before == after {
			t.Fatal("normalizer identity was reused", before)
		}
		observe(booking, nil)
		run(fixture.Exec("ALTER TEXT SEARCH DICTIONARY public.thes_dict (Dictionary='public.thes_sub')", 0))
		observe(booking, nil)
		run(fixture.Exec("ALTER TEXT SEARCH DICTIONARY public.thes_sub RENAME TO thes_renamed_sub", 0))
		observe("", &missing)
		run(fixture.Exec("ALTER TEXT SEARCH DICTIONARY public.thes_renamed_sub RENAME TO thes_sub", 0))
		observe(booking, nil)
	})
}
