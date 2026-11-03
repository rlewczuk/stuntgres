package reference_test

import (
	"database/sql"
	"strings"
	"testing"
	"unicode/utf8"

	"stuntgres/reference/fixture"
)

func TestReferenceFTSHeadlineDiagnostics(t *testing.T) {
	for _, tc := range []struct{ name, options, code, message string }{
		{"incomplete", "StartSel=", "42601", `invalid parameter list format: "StartSel="`},
		{"bare_key", "StartSel", "42601", `invalid parameter list format: "StartSel"`},
		{"leading_equal_missing_separator", "=x", "42601", `invalid parameter list format: "=x"`},
		{"leading_equal_key", "==x", "22023", `unrecognized headline parameter: "="`},
		{"quoted_empty_key", `""=x`, "22023", `unrecognized headline parameter: ""`},
		{"unclosed_double", `StartSel="x`, "42601", `invalid parameter list format: "StartSel="x"`},
		{"unclosed_single", `StartSel='x`, "42601", `invalid parameter list format: "StartSel='x"`},
		{"unknown_case", "wHaT=1", "22023", `unrecognized headline parameter: "wHaT"`},
		{"unicode_highlightall_key", "HİghlightAll=true", "22023", `unrecognized headline parameter: "HİghlightAll"`},
		{"unicode_minwords_key", "MİnWords=1", "22023", `unrecognized headline parameter: "MİnWords"`},
		{"unknown_not_overridden", "Unknown=1,Unknown=2", "22023", `unrecognized headline parameter: "Unknown"`},
		{"quoted_key_escape", `"Start""Sel"="x"`, "22023", `unrecognized headline parameter: "Start"Sel"`},
		{"integer_syntax", "MinWords=nonsense", "22P02", `invalid input syntax for type integer: "nonsense"`},
		{"integer_decimal", "MaxWords=1.5", "22P02", `invalid input syntax for type integer: "1.5"`},
		{"integer_overflow", "MaxWords=2147483648", "22003", `value "2147483648" is out of range for type integer`},
		{"integer_underflow", "MinWords=-2147483649", "22003", `value "-2147483649" is out of range for type integer`},
		{"min_ge_max", "MinWords=35", "22023", "MinWords must be less than MaxWords"},
		{"min_eq_max", "MinWords=1,MaxWords=1", "22023", "MinWords must be less than MaxWords"},
		{"min_positive", "MinWords=0", "22023", "MinWords must be positive"},
		{"min_negative", "MinWords=-1", "22023", "MinWords must be positive"},
		{"shortword_negative", "ShortWord=-1", "22023", "ShortWord must be >= 0"},
		{"fragments_negative", "MaxFragments=-1", "22023", "MaxFragments must be >= 0"},
		{"range_order_1", "MaxWords=-2,MinWords=-1,ShortWord=-1,MaxFragments=-1", "22023", "MinWords must be less than MaxWords"},
		{"range_order_2", "MinWords=0,ShortWord=-1,MaxFragments=-1", "22023", "MinWords must be positive"},
		{"range_order_3", "ShortWord=-1,MaxFragments=-1", "22023", "ShortWord must be >= 0"},
		{"all_does_not_skip_conversion", "HighlightAll=true,MaxWords=no", "22P02", `invalid input syntax for type integer: "no"`},
		{"all_does_not_skip_unknown", "HighlightAll=true,No=1", "22023", `unrecognized headline parameter: "No"`},
		{"duplicate_boolean_last_false", "HighlightAll=true,HighlightAll=false,MinWords=0", "22023", "MinWords must be positive"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ftsConfigurationScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				query := "SELECT ts_headline('english'::regconfig,$1::text,$2::tsquery,$3::text)"
				d := fixture.ControlsReferenceDiagnostic(tc.code, tc.message)
				ftsRankDiagnosticRoutes(t, p, b, c, query, d, false, "Cat rat", "cat", tc.options)
				stmt, err := c.PrepareContext(p.Ctx, query)
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				for range 2 {
					for _, selectRows := range []bool{false, true} {
						s := fixture.ErrorDiagnostic(query, d, "Cat rat", "cat", tc.options)
						s.SelectRows, s.ErrorStage = selectRows, "execute"
						fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: query}, b.Recorder, s)
					}
				}
				rows, err := stmt.QueryContext(p.Ctx, "Cat rat", "cat", "HighlightAll=true")
				if err != nil {
					t.Fatal(err)
				}
				ftsRankRead(t, p, b, rows, "ts_headline", "TEXT", "<b>Cat</b> rat", false)
			})
		})
	}
	ftsConfigurationScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, marker := range []string{"StartSel", "StopSel", "FragmentDelimiter"} {
			for _, value := range []string{strings.Repeat("x", 32768), strings.Repeat("é", 16384)} {
				d := fixture.ControlsReferenceDiagnostic("22023", `value for "`+marker+`" is too long`)
				ftsRankDiagnosticRoutes(t, p, b, c, "SELECT ts_headline('simple'::regconfig,'Cat','cat'::tsquery,$1::text)", d, false, marker+"=\""+value+"\"")
			}
		}
		// Marker validation order is StartSel, StopSel, FragmentDelimiter.
		tooLong := strings.Repeat("x", 32768)
		ftsRankDiagnosticRoutes(t, p, b, c, "SELECT ts_headline('simple'::regconfig,'Cat','cat'::tsquery,$1::text)", fixture.ControlsReferenceDiagnostic("22023", `value for "StartSel" is too long`), false, "StopSel="+tooLong+",FragmentDelimiter="+tooLong+",StartSel="+tooLong)
		for _, tc := range []struct{ args, types string }{
			{"", ""}, {"1,2", "integer, integer"},
			{"'cat'::text,'cat'::text", "text, text"},
			{"'cat'::tsvector,'cat'::tsquery", "tsvector, tsquery"},
			{"'english'::regconfig,'cat'::text,'cat'::text", "regconfig, text, text"},
			{"'cat'::text,'cat'::tsquery,0", "text, tsquery, integer"},
			{"NULL::regconfig,true,'cat'::tsquery", "regconfig, boolean, tsquery"},
			{"document => 'cat'::text, query => 'cat'::tsquery", "document => text, query => tsquery"},
		} {
			ftsRankDiagnosticRoutes(t, p, b, c, "SELECT ts_headline("+tc.args+")", ftsRankMissing("ts_headline("+tc.types+")", 8), true)
		}
		ftsRankDecorations(t, p, b, c, "ts_headline", "'Cat'::text", "text", "''::text")
		unicodeSQL := "/*é🙂*/ SELECT ts_headline(1,2)"
		ftsRankDiagnosticRoutes(t, p, b, c, unicodeSQL, ftsRankMissing("ts_headline(integer, integer)", utf8.RuneCountInString(unicodeSQL[:strings.Index(unicodeSQL, "ts_headline")])+1), true)
		// All unknown categories are chosen against the same overload set.
		// Their simultaneous string preferences eliminate both text forms,
		// so the released resolver retains the candidates and reports ambiguity.
		ambiguous := fixture.ControlsReferenceDiagnostic("42725", "function ts_headline(unknown, unknown, unknown) is not unique")
		ambiguous.Position = "8"
		ambiguous.Hint = "Could not choose a best candidate function. You might need to add explicit type casts."
		ftsRankDiagnosticRoutes(t, p, b, c, "SELECT ts_headline('english','cat','cat')", ambiguous, true)
		ftsRankRoutes(t, p, b, c, "SELECT ts_headline('english'::regconfig,'Cat','cat')", "ts_headline", "TEXT", "<b>Cat</b>")
		ftsRankRoutes(t, p, b, c, "SELECT ts_headline('Cat','cat'::tsquery,'HighlightAll=true')", "ts_headline", "TEXT", "<b>Cat</b>")
		literal := "SELECT ts_headline(NULL::text,'a &'::tsquery)"
		d := fixture.ControlsReferenceDiagnostic("42601", `no operand in tsquery: "a &"`)
		d.Position = fixture.Position(strings.Index(literal, "'a &'") + 1)
		ftsRankDiagnosticRoutes(t, p, b, c, literal, d, true)
		d.Position, d.Context = "", "unnamed portal parameter $2 = '...'"
		ftsRankDiagnosticRoutes(t, p, b, c, "SELECT ts_headline($1::text,$2::tsquery)", d, false, nil, "a &")
		missing := fixture.ControlsReferenceDiagnostic("42704", `text search configuration "headline_missing" does not exist`)
		missing.Position = "20"
		ftsRankDiagnosticRoutes(t, p, b, c, "SELECT ts_headline('headline_missing'::regconfig,'cat','cat'::tsquery)", missing, true)
		missing.Position, missing.Context = "", "unnamed portal parameter $1 = '...'"
		ftsRankDiagnosticRoutes(t, p, b, c, "SELECT ts_headline($1::regconfig,'cat','cat'::tsquery)", missing, false, "headline_missing")
		ftsRankDiagnosticRoutes(t, p, b, c, "SELECT ts_headline(0::regconfig,'cat','cat'::tsquery,'Bad=1')", fixture.ControlsReferenceDiagnostic("XX000", "cache lookup failed for text search configuration 0"), false)
		for _, sql := range []string{parserDDLCreate("public.headline_no_callback")} {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(sql, 0))
		}
		for _, sql := range parserDDLConfigure("public.headline_no_callback_cfg", "public.headline_no_callback") {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(sql, 0))
		}
		// Parser capability failure precedes document parsing and bad options.
		ftsRankDiagnosticRoutes(t, p, b, c, "SELECT ts_headline('public.headline_no_callback_cfg'::regconfig,$1::text,'cat'::tsquery,'Bad=1')", fixture.ControlsReferenceDiagnostic("0A000", "text search parser does not support headline creation"), false, strings.Repeat("x", 2047))
		ftsRankRoutes(t, p, b, c, "SELECT ts_headline('public.headline_no_callback_cfg'::regconfig,NULL::text,'cat'::tsquery,'Bad=1')", "ts_headline", "TEXT", nil)
		ftsRankRoutes(t, p, b, c, "SELECT ts_headline(0::regconfig,NULL::text,'cat'::tsquery,'Bad=1')", "ts_headline", "TEXT", nil)
	})
}
