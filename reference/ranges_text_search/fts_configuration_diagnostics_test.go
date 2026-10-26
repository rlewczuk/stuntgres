package reference_test

import (
	"database/sql"
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	"stuntgres/reference/fixture"
)

func TestReferenceFTSConfigurationDiagnostics(t *testing.T) {
	for _, tc := range []struct{ name, query, code, message string }{
		{"no_parser", "CREATE TEXT SEARCH CONFIGURATION public.config_bad", "42601", "syntax error at end of input"},
		{"both_options", "CREATE TEXT SEARCH CONFIGURATION public.config_bad (PARSER=pg_catalog.default,COPY=pg_catalog.simple)", "42601", "cannot specify both PARSER and COPY options"},
		{"unknown_option", "CREATE TEXT SEARCH CONFIGURATION public.config_bad (unknown=pg_catalog.simple)", "42601", `text search configuration parameter "unknown" not recognized`},
		{"missing_config", "ALTER TEXT SEARCH CONFIGURATION public.config_absent DROP MAPPING FOR asciiword", "42704", `text search configuration "public.config_absent" does not exist`},
		{"missing_parser", "CREATE TEXT SEARCH CONFIGURATION public.config_bad (PARSER=config_absent_parser)", "42704", `text search parser "config_absent_parser" does not exist`},
		{"parser_parameter", "CREATE TEXT SEARCH CONFIGURATION public.config_bad (PARSER)", "42601", "parser requires a parameter"},
		{"copy_numeric", "CREATE TEXT SEARCH CONFIGURATION public.config_bad (COPY=42)", "42601", "argument of copy must be a name"},
		{"unknown_token", "ALTER TEXT SEARCH CONFIGURATION public.config_diagnostics DROP MAPPING FOR not_a_token", "22023", `token type "not_a_token" does not exist`},
		{"if_exists_unknown_token", "ALTER TEXT SEARCH CONFIGURATION public.config_diagnostics DROP MAPPING IF EXISTS FOR not_a_token", "22023", `token type "not_a_token" does not exist`},
		{"missing_mapping", "ALTER TEXT SEARCH CONFIGURATION public.config_diagnostics DROP MAPPING FOR asciiword", "42704", `mapping for token type "asciiword" does not exist`},
	} {
		for _, prepared := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/prepared_%t", tc.name, prepared), func(t *testing.T) {
				ftsConfigurationScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, conn *sql.Conn) {
					fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Exec("CREATE TEXT SEARCH CONFIGURATION public.config_diagnostics (PARSER=pg_catalog.default)", 0))
					s := ftsConfigurationError(tc.query, tc.code, tc.message)
					s.Prepared, s.ErrorStage = prepared, "execute"
					if tc.name == "no_parser" {
						s.Diagnostic.Position = "51"
						if prepared {
							s.ErrorStage = "prepare"
						}
					}
					fixture.RunStep(t, p.Ctx, conn, b.Recorder, s)
				})
			})
		}
	}
	for _, prefix := range []string{"", "/*é🙂*/ "} {
		t.Run(fmt.Sprintf("syntax_multibyte_%t", prefix != ""), func(t *testing.T) {
			ftsConfigurationScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, conn *sql.Conn) {
				query := prefix + "CREATE TEXT SEARCH CONFIGURATION public.config_syntax (COPY=pg_catalog.simple) garbage"
				d := fixture.ControlsReferenceDiagnostic("42601", `syntax error at or near "garbage"`)
				d.Position = fixture.Position(utf8.RuneCountInString(query[:strings.Index(query, "garbage")]) + 1)
				for _, prepared := range []bool{false, true} {
					s := fixture.ErrorDiagnostic(query, d)
					s.Prepared, s.ErrorStage = prepared, "execute"
					if prepared {
						s.ErrorStage = "prepare"
					}
					fixture.RunStep(t, p.Ctx, conn, b.Recorder, s)
				}
				fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Select("SELECT cfgname FROM pg_ts_config WHERE cfgname='config_syntax'", []string{"cfgname"}, []string{"NAME"}, nil))
			})
		})
	}
}
