package reference_test

import (
	"database/sql"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceFTSConfigurationDiscovery(t *testing.T) {
	cases := []struct {
		name, query, code, message string
		setup                      []string
		args                       []any
	}{
		{"duplicate_configuration", "CREATE TEXT SEARCH CONFIGURATION public.config_discovery (COPY=pg_catalog.simple)", "23505", `duplicate key value violates unique constraint "pg_ts_config_cfgname_index"`, nil, nil},
		{"duplicate_mapping", "ALTER TEXT SEARCH CONFIGURATION public.config_discovery ADD MAPPING FOR asciiword WITH simple", "23505", `duplicate key value violates unique constraint "pg_ts_config_map_index"`, nil, nil},
		{"unknown_token", "ALTER TEXT SEARCH CONFIGURATION public.config_discovery DROP MAPPING IF EXISTS FOR not_a_token", "22023", `token type "not_a_token" does not exist`, nil, nil},
		{"missing_mapping", "ALTER TEXT SEARCH CONFIGURATION public.config_discovery DROP MAPPING FOR blank", "42704", `mapping for token type "blank" does not exist`, nil, nil},
		{"regconfig_literal", "/*é🙂*/ SELECT 'public.config_absent'::regconfig", "42704", `text search configuration "public.config_absent" does not exist`, nil, nil},
		{"regconfig_bound", "SELECT $1::regconfig", "42704", `text search configuration "public.config_absent" does not exist`, nil, []any{"public.config_absent"}},
		{"regconfig_runtime_text", "SELECT $1::text::regconfig", "42704", `text search configuration "public.config_absent" does not exist`, nil, []any{"public.config_absent"}},
		{"owner_denial", "ALTER TEXT SEARCH CONFIGURATION public.config_discovery DROP MAPPING FOR asciiword", "42501", "must be owner of text search configuration public.config_discovery",
			[]string{"GRANT USAGE ON SCHEMA public TO config_discovery_actor", "SET ROLE config_discovery_actor"}, nil},
		{"schema_denial", "CREATE TEXT SEARCH CONFIGURATION config_discovery_private.denied (COPY=pg_catalog.simple)", "42501", "permission denied for schema config_discovery_private",
			[]string{"SET ROLE config_discovery_actor"}, nil},
		{"owner_recipient_denial", "ALTER TEXT SEARCH CONFIGURATION public.config_discovery OWNER TO config_discovery_recipient", "42501", `must be able to SET ROLE "config_discovery_recipient"`,
			[]string{"GRANT USAGE,CREATE ON SCHEMA public TO config_discovery_actor", "ALTER TEXT SEARCH CONFIGURATION public.config_discovery OWNER TO config_discovery_actor", "SET ROLE config_discovery_actor"}, nil},
		{"dictionary_dependency", "DROP TEXT SEARCH DICTIONARY public.config_discovery_dict RESTRICT", "2BP01", "cannot drop text search dictionary config_discovery_dict because other objects depend on it",
			[]string{"CREATE TEXT SEARCH DICTIONARY public.config_discovery_dict (TEMPLATE=pg_catalog.simple)", "ALTER TEXT SEARCH CONFIGURATION public.config_discovery ALTER MAPPING FOR asciiword WITH public.config_discovery_dict"}, nil},
		{"schema_dependency", "DROP SCHEMA config_discovery_private RESTRICT", "2BP01", "cannot drop schema config_discovery_private because other objects depend on it",
			[]string{"CREATE TEXT SEARCH CONFIGURATION config_discovery_private.config_dependent (COPY=pg_catalog.simple)"}, nil},
	}
	for _, tc := range cases {
		for _, prepared := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/prepared_%t", tc.name, prepared), func(t *testing.T) {
				fixture.RoleReferenceCleanup(t, []string{"config_discovery_actor", "config_discovery_recipient"})
				fixture.SchemaReferenceCleanup(t, "config_discovery_private")
				ftsConfigurationScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, conn *sql.Conn) {
					for _, query := range append([]string{
						"CREATE ROLE config_discovery_actor", "CREATE ROLE config_discovery_recipient",
						"CREATE SCHEMA config_discovery_private",
						"CREATE TEXT SEARCH CONFIGURATION public.config_discovery (COPY=pg_catalog.simple)",
					}, tc.setup...) {
						fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Exec(query, 0))
					}
					defer conn.ExecContext(p.Ctx, "RESET ROLE")
					d := fixture.ControlsReferenceDiagnostic(tc.code, tc.message)
					switch tc.name {
					case "duplicate_configuration", "duplicate_mapping":
						// Read allocator identities independently from the catalogs, not
						// from the failing command's diagnostic or another backend.
						var namespace, configuration int64
						if err := conn.QueryRowContext(p.Ctx, "SELECT oid::bigint FROM pg_catalog.pg_namespace WHERE nspname='public'").Scan(&namespace); err != nil {
							t.Fatal(err)
						}
						if err := conn.QueryRowContext(p.Ctx, "SELECT oid::bigint FROM pg_catalog.pg_ts_config WHERE cfgnamespace=$1::oid AND cfgname='config_discovery'", namespace).Scan(&configuration); err != nil {
							t.Fatal(err)
						}
						if namespace <= 0 || configuration <= 0 {
							t.Fatalf("invalid catalog identities: namespace=%d configuration=%d", namespace, configuration)
						}
						d.Schema = "pg_catalog"
						if tc.name == "duplicate_configuration" {
							d.Table, d.Constraint = "pg_ts_config", "pg_ts_config_cfgname_index"
							d.Detail = fmt.Sprintf("Key (cfgname, cfgnamespace)=(config_discovery, %d) already exists.", namespace)
						} else {
							d.Table, d.Constraint = "pg_ts_config_map", "pg_ts_config_map_index"
							d.Detail = fmt.Sprintf("Key (mapcfg, maptokentype, mapseqno)=(%d, 1, 1) already exists.", configuration)
						}
					case "regconfig_literal":
						d.Position = "15"
					case "regconfig_bound":
						d.Context = "unnamed portal parameter $1 = '...'"
					case "dictionary_dependency":
						d.Detail = "text search configuration config_discovery depends on text search dictionary config_discovery_dict"
						d.Hint = "Use DROP ... CASCADE to drop the dependent objects too."
					case "schema_dependency":
						d.Detail = "text search configuration config_discovery_private.config_dependent depends on schema config_discovery_private"
						d.Hint = "Use DROP ... CASCADE to drop the dependent objects too."
					}
					s := fixture.ErrorDiagnostic(tc.query, d, tc.args...)
					s.Prepared, s.ErrorStage = prepared, "execute"
					if prepared && tc.name == "regconfig_literal" {
						s.ErrorStage = "prepare"
					}
					fixture.RunStep(t, p.Ctx, conn, b.Recorder, s)
				})
			})
		}
	}
}

func TestReferenceFTSConfigurationNotices(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprintf("prepared_%t", prepared), func(t *testing.T) {
			ftsConfigurationScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, _ *sql.Conn) {
				var notices []fixture.Diagnostic
				_, conn, recorder := fixture.NoticeReferencePool(t, p, b.PG, &notices)
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, recorder, s) }
				run(fixture.Exec("CREATE TEXT SEARCH CONFIGURATION public.config_notice (PARSER=pg_catalog.default)", 0))
				query := "ALTER TEXT SEARCH CONFIGURATION public.config_notice DROP MAPPING IF EXISTS FOR asciiword"
				var stmt *sql.Stmt
				if prepared {
					var err error
					stmt, err = conn.PrepareContext(p.Ctx, query)
					if err != nil {
						t.Fatal(err)
					}
					defer stmt.Close()
					if len(notices) != 0 {
						t.Errorf("Prepare notices=%#v, want none", notices)
					}
				}
				if prepared {
					fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: conn, Statement: stmt, SQL: query}, recorder, fixture.Exec(query, 0))
				} else {
					run(fixture.Exec(query, 0))
				}
				want := []fixture.Diagnostic{{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: `mapping for token type "asciiword" does not exist, skipping`}}
				if !reflect.DeepEqual(notices, want) {
					t.Errorf("notices=%#v want=%#v", notices, want)
				}
				notices = nil
				s := ftsConfigurationError("ALTER TEXT SEARCH CONFIGURATION public.config_notice DROP MAPPING IF EXISTS FOR not_a_token", "22023", `token type "not_a_token" does not exist`)
				s.Prepared, s.ErrorStage = prepared, "execute"
				run(s)
				if len(notices) != 0 {
					t.Errorf("unknown-token notices=%#v, want none", notices)
				}
				sized := ftsConfigurationVector("SELECT to_tsvector('simple'::regconfig,$1::text) AS normalized", "'rats':1", strings.Repeat("a", 2047)+" Rats")
				sized.Prepared = prepared
				run(sized)
				want = []fixture.Diagnostic{{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "54000", Message: "word is too long to be indexed", Detail: "Words longer than 2047 characters are ignored."}}
				if !reflect.DeepEqual(notices, want) {
					t.Fatalf("conversion boundary notices=%#v want=%#v", notices, want)
				}
			})
		})
	}
}
