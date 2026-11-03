package reference_test

import (
	"database/sql"
	"reflect"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceFTSHeadlineDefaultsPrepared(t *testing.T) {
	t.Run("default_and_parameters", func(t *testing.T) {
		ftsConfigurationScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			defer ftsConvertSetting(t, p, c)()
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
			query := "SELECT ts_headline($1::text,$2::tsquery) AS excerpt"
			explicit := "SELECT ts_headline('english'::regconfig,$1::text,$2::tsquery) AS excerpt"
			stmt, err := c.PrepareContext(p.Ctx, query)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			fixed, err := c.PrepareContext(p.Ctx, explicit)
			if err != nil {
				t.Fatal(err)
			}
			defer fixed.Close()
			literal, err := c.PrepareContext(p.Ctx, "SELECT ts_headline('Rats'::text,'rat'::tsquery) AS excerpt")
			if err != nil {
				t.Fatal(err)
			}
			defer literal.Close()
			for _, setting := range []string{"simple", "english", "simple", "english"} {
				run(fixture.Exec("SET default_text_search_config='"+setting+"'", 0))
				want := "<b>Rats</b>"
				if setting == "simple" {
					want = "Rats"
				}
				ftsRankRoutes(t, p, b, c, query, "excerpt", "TEXT", want, "Rats", "rat")
				rows, err := stmt.QueryContext(p.Ctx, "Rats", "rat")
				if err != nil {
					t.Fatal(err)
				}
				ftsRankRead(t, p, b, rows, "excerpt", "TEXT", want, false)
				rows, err = fixed.QueryContext(p.Ctx, "Rats", "rat")
				if err != nil {
					t.Fatal(err)
				}
				ftsRankRead(t, p, b, rows, "excerpt", "TEXT", "<b>Rats</b>", false)
				rows, err = literal.QueryContext(p.Ctx)
				if err != nil {
					t.Fatal(err)
				}
				ftsRankRead(t, p, b, rows, "excerpt", "TEXT", want, false)
			}
			// Prepare under simple, execute after English; neither Prepare direction
			// may capture the default configuration as immutable authority.
			run(fixture.Exec("SET default_text_search_config='simple'", 0))
			reverse, err := c.PrepareContext(p.Ctx, query)
			if err != nil {
				t.Fatal(err)
			}
			defer reverse.Close()
			run(fixture.Exec("SET default_text_search_config='english'", 0))
			rows, err := reverse.QueryContext(p.Ctx, "Rats", "rat")
			if err != nil {
				t.Fatal(err)
			}
			ftsRankRead(t, p, b, rows, "excerpt", "TEXT", "<b>Rats</b>", false)
			run(fixture.Exec("BEGIN", 0))
			run(fixture.Exec("SET LOCAL default_text_search_config='simple'", 0))
			rows, err = stmt.QueryContext(p.Ctx, "Rats", "rat")
			if err != nil {
				t.Fatal(err)
			}
			ftsRankRead(t, p, b, rows, "excerpt", "TEXT", "Rats", false)
			run(fixture.Exec("ROLLBACK", 0))
			rows, err = stmt.QueryContext(p.Ctx, "Rats", "rat")
			if err != nil {
				t.Fatal(err)
			}
			ftsRankRead(t, p, b, rows, "excerpt", "TEXT", "<b>Rats</b>", false)
			parameterSQL := "SELECT ts_headline($1::regconfig,$2::text,$3::tsquery,$4::text) AS excerpt"
			parameters, err := c.PrepareContext(p.Ctx, parameterSQL)
			if err != nil {
				t.Fatal(err)
			}
			defer parameters.Close()
			for _, tc := range []struct {
				args []any
				want any
			}{
				{[]any{"english", "Rats & cats", "rat", "HighlightAll=true"}, "<b>Rats</b> & cats"},
				{[]any{"simple", "Rats & cats", "rat", "HighlightAll=true"}, "Rats & cats"},
				{[]any{"english", "Cats & rats", "cat", "StartSel=[,StopSel=],HighlightAll=true"}, "[Cats] & rats"},
				{[]any{"english", nil, "cat", "StartSel="}, nil},
				{[]any{nil, "Cats", "cat", "Bad=1"}, nil},
			} {
				ftsRankRoutes(t, p, b, c, parameterSQL, "excerpt", "TEXT", tc.want, tc.args...)
				rows, err := parameters.QueryContext(p.Ctx, tc.args...)
				if err != nil {
					t.Fatal(err)
				}
				ftsRankRead(t, p, b, rows, "excerpt", "TEXT", tc.want, false)
			}
			for range 2 {
				s := fixture.ErrorDiagnostic(parameterSQL, fixture.ControlsReferenceDiagnostic("42601", `invalid parameter list format: "StartSel="`), "english", "Cat", "cat", "StartSel=")
				s.SelectRows, s.ErrorStage = true, "execute"
				fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: parameters, SQL: parameterSQL}, b.Recorder, s)
			}
			rows, err = parameters.QueryContext(p.Ctx, "english", "Cat", "cat", "HighlightAll=true")
			if err != nil {
				t.Fatal(err)
			}
			ftsRankRead(t, p, b, rows, "excerpt", "TEXT", "<b>Cat</b>", false)
		})
	})
	t.Run("copied_parser_and_live_dictionary", func(t *testing.T) {
		ftsConfigurationScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
			for _, sql := range []string{
				"CREATE TEXT SEARCH PARSER public.headline_parser (" + parserDDLMethods + ",HEADLINE=pg_catalog.prsd_headline)",
				"ALTER TEXT SEARCH PARSER public.headline_parser RENAME TO headline_parser_renamed",
				"CREATE TEXT SEARCH CONFIGURATION public.headline_cfg (PARSER=public.headline_parser_renamed)",
				"ALTER TEXT SEARCH CONFIGURATION public.headline_cfg ADD MAPPING FOR asciiword WITH pg_catalog.english_stem",
				"ALTER TEXT SEARCH CONFIGURATION public.headline_cfg RENAME TO headline_cfg_renamed",
			} {
				run(fixture.Exec(sql, 0))
			}
			query := "SELECT ts_headline($1::regconfig,$2::text,$3::tsquery,'HighlightAll=true') AS excerpt"
			stmt, err := c.PrepareContext(p.Ctx, query)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			for _, mapping := range []string{"english_stem", "simple", "english_stem"} {
				run(fixture.Exec("ALTER TEXT SEARCH CONFIGURATION public.headline_cfg_renamed ALTER MAPPING FOR asciiword WITH pg_catalog."+mapping, 0))
				want := "<b>Rats</b>"
				if mapping == "simple" {
					want = "Rats"
				}
				ftsRankRoutes(t, p, b, c, query, "excerpt", "TEXT", want, "public.headline_cfg_renamed", "Rats", "rat")
				rows, err := stmt.QueryContext(p.Ctx, "public.headline_cfg_renamed", "Rats", "rat")
				if err != nil {
					t.Fatal(err)
				}
				ftsRankRead(t, p, b, rows, "excerpt", "TEXT", want, false)
			}
			thesaurusSetup(t, p, b, c, "thesaurus_sample")
			for _, tc := range []struct{ document, query, want string }{
				{"ONE TWO THREE", "123", "<b>ONE</b><b> </b><b>TWO</b><b> </b><b>THREE</b>"},
				{"supernovae stars", "sn", "<b>supernovae</b><b> </b><b>stars</b>"},
				{"BOOKING THE TICKETS", "order & invitation & cards", "<b>BOOKING</b><b> </b><b>THE</b><b> </b><b>TICKETS</b>"},
			} {
				ftsRankRoutes(t, p, b, c, query, "excerpt", "TEXT", tc.want, "public.thes_cfg", tc.document, tc.query)
				rows, err := stmt.QueryContext(p.Ctx, "public.thes_cfg", tc.document, tc.query)
				if err != nil {
					t.Fatal(err)
				}
				ftsRankRead(t, p, b, rows, "excerpt", "TEXT", tc.want, false)
			}
			run(fixture.Exec("ALTER TEXT SEARCH DICTIONARY public.thes_dict (DictFile=stuntgres_thes_ties)", 0))
			ftsRankRoutes(t, p, b, c, query, "excerpt", "TEXT", "<b>Alpha</b><b> </b><b>Beta</b>", "public.thes_cfg", "Alpha Beta", "last")
			rows, err := stmt.QueryContext(p.Ctx, "public.thes_cfg", "Alpha Beta", "last")
			if err != nil {
				t.Fatal(err)
			}
			ftsRankRead(t, p, b, rows, "excerpt", "TEXT", "<b>Alpha</b><b> </b><b>Beta</b>", false)
		})
	})
	t.Run("synonym_and_filter_associations", func(t *testing.T) {
		ftsConfigurationScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
			for _, sql := range []string{
				"CREATE TEXT SEARCH DICTIONARY public.headline_synonym (TEMPLATE=pg_catalog.synonym,Synonyms=synonym_sample)",
				"CREATE TEXT SEARCH CONFIGURATION public.headline_synonym_cfg (COPY=pg_catalog.simple)",
				"ALTER TEXT SEARCH CONFIGURATION public.headline_synonym_cfg ALTER MAPPING FOR asciiword WITH public.headline_synonym,pg_catalog.simple",
				"CREATE EXTENSION unaccent",
				"CREATE TEXT SEARCH CONFIGURATION public.headline_filter_cfg (COPY=pg_catalog.simple)",
				"ALTER TEXT SEARCH CONFIGURATION public.headline_filter_cfg ALTER MAPPING FOR word,asciiword WITH public.unaccent,pg_catalog.simple",
			} {
				run(fixture.Exec(sql, 0))
			}
			query := "SELECT ts_headline($1::regconfig,$2::text,$3::tsquery,'HighlightAll=true')"
			stmt, err := c.PrepareContext(p.Ctx, query)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			for _, sensitive := range []string{"false", "true", "false"} {
				run(fixture.Exec("ALTER TEXT SEARCH DICTIONARY public.headline_synonym (CaseSensitive="+sensitive+")", 0))
				want := "<b>POSTGRES</b> WORD"
				if sensitive == "true" {
					want = "POSTGRES WORD"
				}
				ftsRankRoutes(t, p, b, c, query, "ts_headline", "TEXT", want, "public.headline_synonym_cfg", "POSTGRES WORD", "pgsql")
				rows, err := stmt.QueryContext(p.Ctx, "public.headline_synonym_cfg", "POSTGRES WORD", "pgsql")
				if err != nil {
					t.Fatal(err)
				}
				ftsRankRead(t, p, b, rows, "ts_headline", "TEXT", want, false)
			}
			for _, mapping := range []string{"public.unaccent,pg_catalog.simple", "pg_catalog.simple,public.unaccent", "public.unaccent,pg_catalog.simple"} {
				run(fixture.Exec("ALTER TEXT SEARCH CONFIGURATION public.headline_filter_cfg ALTER MAPPING FOR word,asciiword WITH "+mapping, 0))
				want := "<b>Hôtel</b> plain"
				if mapping == "pg_catalog.simple,public.unaccent" {
					want = "Hôtel plain"
				}
				ftsRankRoutes(t, p, b, c, query, "ts_headline", "TEXT", want, "public.headline_filter_cfg", "Hôtel plain", "hotel")
				rows, err := stmt.QueryContext(p.Ctx, "public.headline_filter_cfg", "Hôtel plain", "hotel")
				if err != nil {
					t.Fatal(err)
				}
				ftsRankRead(t, p, b, rows, "ts_headline", "TEXT", want, false)
			}
			run(fixture.Exec("ALTER TEXT SEARCH DICTIONARY public.unaccent RENAME TO headline_filter_renamed", 0))
			rows, err := stmt.QueryContext(p.Ctx, "public.headline_filter_cfg", "Hôtel plain", "hotel")
			if err != nil {
				t.Fatal(err)
			}
			ftsRankRead(t, p, b, rows, "ts_headline", "TEXT", "<b>Hôtel</b> plain", false)
		})
	})
	t.Run("normalization_notices_before_options", func(t *testing.T) {
		ftsConfigurationScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, _ *sql.Conn) {
			var notices []fixture.Diagnostic
			_, c, recorder := fixture.NoticeReferencePool(t, p, b.PG, &notices)
			defer ftsConvertSetting(t, p, c)()
			query := "SELECT ts_headline($1::text,$2::tsquery,$3::text) AS excerpt"
			stmt, err := c.PrepareContext(p.Ctx, query)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			if len(notices) != 0 {
				t.Fatal("Prepare emitted notice", notices)
			}
			want := []fixture.Diagnostic{{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "54000", Message: "word is too long to be indexed", Detail: "Words longer than 2047 characters are ignored."}}
			doc := strings.Repeat("x", 2047) + " Cat"
			for range 2 {
				s := fixture.Select(query, []string{"excerpt"}, []string{"TEXT"}, [][]any{{" <b>Cat</b>"}}, doc, "cat", "HighlightAll=true")
				fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: query}, recorder, s)
				if !reflect.DeepEqual(notices, want) {
					t.Fatalf("Execute notices=%#v want=%#v", notices, want)
				}
				notices = nil
			}
			s := fixture.ErrorDiagnostic(query, fixture.ControlsReferenceDiagnostic("22023", `unrecognized headline parameter: "Bad"`), doc, "cat", "Bad=1")
			s.SelectRows, s.ErrorStage = true, "execute"
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: query}, recorder, s)
			if !reflect.DeepEqual(notices, want) {
				t.Fatalf("parse-before-options notices=%#v want=%#v", notices, want)
			}
			notices = nil
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: query}, recorder, fixture.Select(query, []string{"excerpt"}, []string{"TEXT"}, [][]any{{nil}}, nil, "cat", "Bad=1"))
			if len(notices) != 0 {
				t.Fatal("strict NULL emitted notice", notices)
			}
			constantSQL := "SELECT ts_headline(" + ftsTypesQuote(doc) + ",'cat'::tsquery,'HighlightAll=true') AS excerpt"
			constant, err := c.PrepareContext(p.Ctx, constantSQL)
			if err != nil {
				t.Fatal(err)
			}
			defer constant.Close()
			if len(notices) != 0 {
				t.Fatal("stable literal Prepare emitted notice", notices)
			}
			for range 2 {
				fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: constant, SQL: constantSQL}, recorder, fixture.Select(constantSQL, []string{"excerpt"}, []string{"TEXT"}, [][]any{{" <b>Cat</b>"}}))
				if !reflect.DeepEqual(notices, want) {
					t.Fatalf("stable literal Execute notices=%#v want=%#v", notices, want)
				}
				notices = nil
			}
		})
	})
}
