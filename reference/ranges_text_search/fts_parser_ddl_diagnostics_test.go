package reference_test

import (
	"database/sql"
	"fmt"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
	"unicode/utf8"
)

func TestReferenceFTSParserDDLDiagnostics(t *testing.T) {
	fixture.SchemaReferenceCleanup(t, "parser_collision_schema")
	for _, tc := range []struct{ name, options, code, message string }{
		{"start", "END=pg_catalog.prsd_end", "42P17", "text search parser start method is required"},
		{"token", "START=pg_catalog.prsd_start", "42P17", "text search parser gettoken method is required"},
		{"end", "START=pg_catalog.prsd_start,GETTOKEN=pg_catalog.prsd_nexttoken", "42P17", "text search parser end method is required"},
		{"types", "START=pg_catalog.prsd_start,GETTOKEN=pg_catalog.prsd_nexttoken,END=pg_catalog.prsd_end", "42P17", "text search parser lextypes method is required"},
		{"unknown", "bogus=pg_catalog.prsd_start", "42601", `text search parser parameter "bogus" not recognized`},
		{"bare", "START", "42601", "start requires a parameter"},
		{"numeric", "START=123", "42601", "argument of start must be a name"},
		{"inputs", "START=pg_catalog.prsd_end", "42883", "function pg_catalog.prsd_end(internal, integer) does not exist"},
		{"result", "END=pg_catalog.prsd_lextype", "42P17", "function pg_catalog.prsd_lextype(internal) should return type void"},
		{"types_result", "LEXTYPES=pg_catalog.prsd_end", "42P17", "function pg_catalog.prsd_end(internal) should return type internal"},
		{"invalid_duplicate", "START=pg_catalog.prsd_end," + parserDDLMethods, "42883", "function pg_catalog.prsd_end(internal, integer) does not exist"},
		{"missing_routine", "START=public.parser_callback_absent", "42883", "function public.parser_callback_absent(internal, integer) does not exist"},
	} {
		for _, prepared := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/prepared_%t", tc.name, prepared), func(t *testing.T) {
				ftsConfigurationScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
					run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
					s := ftsConfigurationError("CREATE TEXT SEARCH PARSER public.parser_bad ("+tc.options+")", tc.code, tc.message)
					s.Prepared, s.ErrorStage = prepared, "execute"
					run(s)
					run(fixture.Select("SELECT prsname FROM pg_ts_parser WHERE prsname='parser_bad'", []string{"prsname"}, []string{"NAME"}, nil))
					s = fixture.Exec(parserDDLCreate("public.parser_bad"), 0)
					s.Prepared = prepared
					run(s)
					run(parserDDLWord("public.parser_bad"))
				})
			})
		}
	}
	for _, prefix := range []string{"", "/*é🙂*/ "} {
		for _, prepared := range []bool{false, true} {
			t.Run(fmt.Sprintf("missing_callback_schema/prefix_%t/prepared_%t", prefix != "", prepared), func(t *testing.T) {
				ftsConfigurationScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
					run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
					query := prefix + "CREATE TEXT SEARCH PARSER public.parser_bad (" +
						"START=parser_callback_absent_schema.prsd_start," +
						"GETTOKEN=pg_catalog.prsd_nexttoken,END=pg_catalog.prsd_end," +
						"LEXTYPES=pg_catalog.prsd_lextype)"
					// Utility callback lookup has no expression location. Assert
					// the whole diagnostic, including an intentionally empty Position.
					diagnostic := fixture.ControlsReferenceDiagnostic("3F000", `schema "parser_callback_absent_schema" does not exist`)
					failed := fixture.ErrorDiagnostic(query, diagnostic)
					failed.Prepared, failed.ErrorStage = prepared, "execute"
					run(failed)
					run(fixture.Select("SELECT prsname FROM pg_ts_parser WHERE prsname='parser_bad'", []string{"prsname"}, []string{"NAME"}, nil))
					corrected := fixture.Exec(prefix+parserDDLCreate("public.parser_bad"), 0)
					corrected.Prepared = prepared
					run(corrected)
					run(parserDDLCallbacks("parser_bad", 0))
					run(parserDDLWord("public.parser_bad"))
				})
			})
		}
	}
	for _, prefix := range []string{"", "/*é🙂*/ "} {
		for _, tc := range []struct{ query, token string }{
			{parserDDLCreate("public.parser_syntax") + " garbage", "garbage"},
			{"ALTER TEXT SEARCH PARSER public.parser_syntax OWNER TO postgres", "OWNER"},
			{"ALTER TEXT SEARCH PARSER public.parser_syntax (START=pg_catalog.prsd_start)", "("},
			{"CREATE OR REPLACE TEXT SEARCH PARSER public.parser_syntax (" + parserDDLMethods + ")", "TEXT"},
		} {
			t.Run(prefix+tc.query, func(t *testing.T) {
				ftsConfigurationScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
					query, token := tc.query, tc.token
					full := prefix + query
					message := fmt.Sprintf(`syntax error at or near "%s"`, token)
					d := fixture.ControlsReferenceDiagnostic("42601", message)
					d.Position = fixture.Position(utf8.RuneCountInString(full[:strings.Index(full, token)]) + 1)
					for _, prepared := range []bool{false, true} {
						s := fixture.ErrorDiagnostic(full, d)
						s.Prepared, s.ErrorStage = prepared, "execute"
						if prepared {
							s.ErrorStage = "prepare"
						}
						fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
					}
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT prsname FROM pg_ts_parser WHERE prsname='parser_syntax'", []string{"prsname"}, []string{"NAME"}, nil))
				})
			})
		}
	}
	ftsConfigurationScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec(parserDDLCreate("public.parser_duplicate"), 0))
		run(fixture.Exec(parserDDLCreate("public.parser_collision"), 0))
		run(fixture.Exec("CREATE SCHEMA parser_collision_schema", 0))
		run(fixture.Exec(parserDDLCreate("parser_collision_schema.parser_duplicate"), 0))
		var namespace int64
		if err := c.QueryRowContext(p.Ctx, "SELECT oid::bigint FROM pg_namespace WHERE nspname='public'").Scan(&namespace); err != nil {
			t.Fatal(err)
		}
		d := fixture.ControlsReferenceDiagnostic("23505", `duplicate key value violates unique constraint "pg_ts_parser_prsname_index"`)
		d.Detail = fmt.Sprintf("Key (prsname, prsnamespace)=(parser_duplicate, %d) already exists.", namespace)
		d.Schema = "pg_catalog"
		d.Table = "pg_ts_parser"
		d.Constraint = "pg_ts_parser_prsname_index"
		for _, s := range []fixture.Step{
			fixture.ErrorDiagnostic(parserDDLCreate("public.parser_duplicate"), d),
			ftsConfigurationError("ALTER TEXT SEARCH PARSER public.parser_duplicate RENAME TO parser_collision", "42710", `text search parser "parser_collision" already exists in schema "public"`),
			ftsConfigurationError("ALTER TEXT SEARCH PARSER public.parser_duplicate SET SCHEMA parser_collision_schema", "42710", `text search parser "parser_duplicate" already exists in schema "parser_collision_schema"`),
			ftsConfigurationError("ALTER TEXT SEARCH PARSER public.parser_absent RENAME TO parser_collision", "42704", `text search parser "public.parser_absent" does not exist`),
			ftsConfigurationError("ALTER TEXT SEARCH PARSER public.parser_duplicate SET SCHEMA parser_absent_schema", "3F000", `schema "parser_absent_schema" does not exist`),
		} {
			run(s)
			run(fixture.Prepared(s))
			run(parserDDLWord("public.parser_duplicate"))
		}
		run(fixture.Exec("DROP TEXT SEARCH PARSER IF EXISTS public.parser_absent", 0))
		run(fixture.Prepared(fixture.Exec("DROP TEXT SEARCH PARSER IF EXISTS public.parser_absent", 0)))
	})
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprintf("notice_prepared_%t", prepared), func(t *testing.T) {
			ftsConfigurationScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, _ *sql.Conn) {
				var notices []fixture.Diagnostic
				_, conn, recorder := fixture.NoticeReferencePool(t, p, b.PG, &notices)
				query := "DROP TEXT SEARCH PARSER IF EXISTS public.parser_notice_absent"
				step := fixture.Exec(query, 0)
				step.Prepared = prepared
				fixture.RunStep(t, p.Ctx, conn, recorder, step)
				if len(notices) != 1 {
					t.Fatalf("notice count: %#v", notices)
				}
				want := fixture.Diagnostic{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: `text search parser "public.parser_notice_absent" does not exist, skipping`}
				if notices[0] != want {
					t.Fatalf("notice %#v, want %#v", notices[0], want)
				}
			})
		})
	}
}
func TestReferenceFTSParserDDLPrivileges(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"parser_schema_owner", "parser_unrelated", "parser_super"})
	fixture.SchemaReferenceCleanup(t, "parser_owned_schema")
	fixture.SchemaReferenceCleanup(t, "parser_destination_schema")
	ftsConfigurationScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		for _, q := range []string{"CREATE ROLE parser_schema_owner", "CREATE ROLE parser_unrelated", "CREATE ROLE parser_super SUPERUSER", "CREATE SCHEMA parser_owned_schema AUTHORIZATION parser_schema_owner", "GRANT USAGE ON SCHEMA parser_owned_schema TO parser_unrelated", "SET ROLE parser_super", parserDDLCreate("parser_owned_schema.parser_privilege"), "RESET ROLE"} {
			run(fixture.Exec(q, 0))
		}
		run(fixture.Exec("CREATE SCHEMA parser_destination_schema", 0))
		run(fixture.Exec("GRANT USAGE,CREATE ON SCHEMA parser_destination_schema TO parser_schema_owner,parser_unrelated", 0))
		defer func() {
			if _, err := c.ExecContext(p.Ctx, "RESET ROLE"); err != nil {
				t.Error(err)
			}
		}()
		for _, role := range []string{"parser_schema_owner", "parser_unrelated"} {
			run(fixture.Exec("SET ROLE "+role, 0))
			for _, s := range []fixture.Step{
				ftsConfigurationError(parserDDLCreate("parser_owned_schema.parser_forbidden"), "42501", "must be superuser to create text search parsers"),
				ftsConfigurationError("ALTER TEXT SEARCH PARSER parser_owned_schema.parser_privilege RENAME TO parser_forbidden", "42501", "must be superuser to rename text search parser parser_owned_schema.parser_privilege"),
				ftsConfigurationError("ALTER TEXT SEARCH PARSER parser_owned_schema.parser_privilege SET SCHEMA public", "42501", "permission denied for schema public"),
				ftsConfigurationError("ALTER TEXT SEARCH PARSER parser_owned_schema.parser_privilege SET SCHEMA parser_destination_schema", "42501", "must be superuser to set schema of text search parser parser_owned_schema.parser_privilege"),
			} {
				s.ErrorStage = "execute"
				run(s)
				run(fixture.Prepared(s))
			}
			run(parserDDLWord("parser_owned_schema.parser_privilege"))
			if role == "parser_unrelated" {
				s := ftsConfigurationError("DROP TEXT SEARCH PARSER parser_owned_schema.parser_privilege", "42501", "must be superuser")
				run(s)
				run(fixture.Prepared(s))
			}
			run(fixture.Exec("RESET ROLE", 0))
		}
		run(fixture.Exec("SET ROLE parser_schema_owner", 0))
		run(fixture.Prepared(fixture.Exec("DROP TEXT SEARCH PARSER parser_owned_schema.parser_privilege", 0)))
		run(fixture.Exec("RESET ROLE", 0))
		run(fixture.Select("SELECT prsname FROM pg_ts_parser WHERE prsname IN ('parser_forbidden','parser_privilege')", []string{"prsname"}, []string{"NAME"}, nil))
	})
}
