package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceRoleDefaultPrivilegesGrammar(t *testing.T) {
	dpScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), prepared bool) {
		dpSchema(run, "dp_s", "dp_owner")
		for _, class := range []string{"TABLES", "SEQUENCES", "FUNCTIONS", "ROUTINES", "TYPES", "SCHEMAS", "LARGE OBJECTS"} {
			for _, option := range []string{"", " WITH GRANT OPTION"} {
				run(fixture.Exec("ALTER DEFAULT PRIVILEGES FOR USER dp_owner,dp_owner GRANT ALL PRIVILEGES ON "+class+" TO GROUP dp_reader,dp_reader"+option, 0))
				run(fixture.Exec("ALTER DEFAULT PRIVILEGES FOR ROLE dp_owner REVOKE GRANT OPTION FOR ALL ON "+class+" FROM dp_reader RESTRICT", 0))
				run(fixture.Exec("ALTER DEFAULT PRIVILEGES FOR USER dp_owner REVOKE ALL PRIVILEGES ON "+class+" FROM dp_reader CASCADE", 0))
			}
			run(fixture.Exec("ALTER DEFAULT PRIVILEGES GRANT ALL ON "+class+" TO CURRENT_USER,CURRENT_ROLE,SESSION_USER", 0))
			run(fixture.Exec("ALTER DEFAULT PRIVILEGES REVOKE GRANT OPTION FOR ALL ON "+class+" FROM PUBLIC", 0))
			if class == "FUNCTIONS" || class == "ROUTINES" || class == "TYPES" {
				run(fixture.Exec("ALTER DEFAULT PRIVILEGES GRANT ALL ON "+class+" TO PUBLIC", 0))
			}
		}
		for _, q := range []string{
			"ALTER DEFAULT PRIVILEGES IN SCHEMA dp_s,dp_s FOR ROLE dp_owner,dp_owner GRANT SELECT,INSERT,UPDATE,DELETE,TRUNCATE,REFERENCES,TRIGGER,MAINTAIN ON TABLES TO dp_reader,dp_reader WITH GRANT OPTION",
			"ALTER DEFAULT PRIVILEGES FOR USER dp_owner IN SCHEMA dp_s REVOKE ALL ON TABLES FROM dp_reader CASCADE",
			"ALTER DEFAULT PRIVILEGES IN SCHEMA dp_s FOR ROLE dp_owner GRANT USAGE,SELECT,UPDATE ON SEQUENCES TO dp_reader",
			"ALTER DEFAULT PRIVILEGES FOR ROLE dp_owner IN SCHEMA dp_s REVOKE ALL ON SEQUENCES FROM dp_reader",
			"ALTER DEFAULT PRIVILEGES FOR ROLE dp_owner IN SCHEMA dp_s GRANT EXECUTE ON FUNCTIONS TO dp_reader",
			"ALTER DEFAULT PRIVILEGES IN SCHEMA dp_s FOR USER dp_owner REVOKE EXECUTE ON ROUTINES FROM dp_reader",
			"ALTER DEFAULT PRIVILEGES FOR ROLE dp_owner IN SCHEMA dp_s GRANT USAGE ON TYPES TO dp_reader",
			"ALTER DEFAULT PRIVILEGES FOR ROLE dp_owner IN SCHEMA dp_s REVOKE USAGE ON TYPES FROM dp_reader",
		} {
			run(fixture.Exec(q, 0))
		}
		run(fixture.Exec("CREATE ROLE \"dp_Quoted\"", 0))
		// Quoted role names remain case-sensitive, never contextual keywords.
		run(fixture.Exec("ALTER DEFAULT PRIVILEGES FOR ROLE \"dp_Quoted\" GRANT SELECT ON TABLES TO dp_reader", 0))
		run(fixture.Exec("ALTER DEFAULT PRIVILEGES FOR ROLE \"dp_Quoted\" REVOKE SELECT ON TABLES FROM dp_reader", 0))
		run(fixture.Exec("DROP ROLE \"dp_Quoted\"", 0))
		run(fixture.Exec("ALTER DEFAULT PRIVILEGES FOR ROLE dp_owner GRANT SELECT,UPDATE ON LARGE OBJECTS TO dp_reader", 0))
		run(fixture.Exec("ALTER DEFAULT PRIVILEGES FOR ROLE dp_owner REVOKE SELECT,UPDATE ON LARGE OBJECTS FROM dp_reader", 0))
		run(fixture.Select("ALTER DEFAULT PRIVILEGES FOR ROLE dp_owner REVOKE SELECT ON TABLES FROM dp_reader", []string{}, []string{}, nil))
		for _, tc := range []struct{ q, token string }{
			{"ALTER DEFAULT PRIVILEGES GRANT SELECT ON TABLE TO dp_reader", "TABLE"},
			{"ALTER DEFAULT PRIVILEGES GRANT SELECT ON TABLES dp_s TO dp_reader", "dp_s"},
			{"ALTER DEFAULT PRIVILEGES GRANT SELECT ON ALL TABLES IN SCHEMA dp_s TO dp_reader", "ALL"},
			{"ALTER DEFAULT PRIVILEGES GRANT SELECT ON TABLES TO dp_reader GRANTED BY CURRENT_USER", "GRANTED"},
			{"ALTER DEFAULT PRIVILEGES GRANT SELECT ON TABLES TO dp_reader; SELECT )", ")"},
			{"ALTER DEFAULT PRIVILEGES GRANT SELECT ON \"TABLES\" TO dp_reader", "\"TABLES\""},
		} {
			dpSyntax(run, tc.q, tc.token, prepared)
		}
	})
}

func TestReferenceRoleDefaultPrivilegesDiagnostics(t *testing.T) {
	dpScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), prepared bool) {
		dpSchema(run, "dp_s", "dp_owner")
		cases := []struct{ q, code, message string }{
			{"ALTER DEFAULT PRIVILEGES GRANT SELECT ON TABLES TO dp_missing", "42704", `role "dp_missing" does not exist`},
			{"ALTER DEFAULT PRIVILEGES FOR ROLE dp_missing GRANT SELECT ON TABLES TO dp_reader", "42704", `role "dp_missing" does not exist`},
			{"ALTER DEFAULT PRIVILEGES FOR ROLE PUBLIC GRANT SELECT ON TABLES TO dp_reader", "42704", `role "public" does not exist`},
			{"ALTER DEFAULT PRIVILEGES IN SCHEMA dp_missing GRANT SELECT ON TABLES TO dp_reader", "3F000", `schema "dp_missing" does not exist`},
			{"ALTER DEFAULT PRIVILEGES GRANT SELECT(x) ON TABLES TO dp_reader", "0LP01", "default privileges cannot be set for columns"},
			{"ALTER DEFAULT PRIVILEGES GRANT ALL(x) ON TABLES TO dp_reader", "0LP01", "default privileges cannot be set for columns"},
			{"ALTER DEFAULT PRIVILEGES GRANT bogus ON TABLES TO dp_reader", "42601", `unrecognized privilege type "bogus"`},
			{"ALTER DEFAULT PRIVILEGES GRANT SELECT ON TABLES TO PUBLIC WITH GRANT OPTION", "0LP01", "grant options can only be granted to roles"},
		}
		for _, tc := range []struct{ class, privilege, kind string }{{"TABLES", "USAGE", "relation"}, {"SEQUENCES", "INSERT", "sequence"}, {"FUNCTIONS", "SELECT", "function"}, {"ROUTINES", "USAGE", "function"}, {"TYPES", "EXECUTE", "type"}, {"SCHEMAS", "SELECT", "schema"}, {"LARGE OBJECTS", "USAGE", "large object"}} {
			cases = append(cases, struct{ q, code, message string }{"ALTER DEFAULT PRIVILEGES GRANT " + tc.privilege + " ON " + tc.class + " TO dp_reader", "0LP01", "invalid privilege type " + tc.privilege + " for " + tc.kind})
			for _, alias := range []string{"TEMP", "TEMPORARY"} {
				cases = append(cases, struct{ q, code, message string }{"ALTER DEFAULT PRIVILEGES GRANT " + alias + " ON " + tc.class + " TO dp_reader", "0LP01", "invalid privilege type TEMP for " + tc.kind})
			}
		}
		for _, class := range []string{"SCHEMAS", "LARGE OBJECTS"} {
			for _, action := range []string{"GRANT ALL ON " + class + " TO", "REVOKE ALL ON " + class + " FROM"} {
				cases = append(cases, struct{ q, code, message string }{"ALTER DEFAULT PRIVILEGES IN SCHEMA dp_s " + action + " dp_reader", "0LP01", "cannot use IN SCHEMA clause when using GRANT/REVOKE ON " + class})
				for _, schemas := range []string{"dp_s,dp_missing", "dp_missing,dp_s"} {
					code, message := "0LP01", "cannot use IN SCHEMA clause when using GRANT/REVOKE ON "+class
					if strings.HasPrefix(schemas, "dp_missing") {
						code, message = "3F000", `schema "dp_missing" does not exist`
					}
					cases = append(cases, struct{ q, code, message string }{"ALTER DEFAULT PRIVILEGES IN SCHEMA " + schemas + " " + action + " dp_reader", code, message})
				}
			}
		}
		for _, schemas := range []string{"dp_s,dp_missing", "dp_missing,dp_s"} {
			code, message := "0LP01", "grant options can only be granted to roles"
			if strings.HasPrefix(schemas, "dp_missing") {
				code, message = "3F000", `schema "dp_missing" does not exist`
			}
			cases = append(cases, struct{ q, code, message string }{"ALTER DEFAULT PRIVILEGES IN SCHEMA " + schemas + " GRANT SELECT ON TABLES TO PUBLIC WITH GRANT OPTION", code, message})
		}
		for _, tc := range cases {
			dpError(run, tc.q, tc.code, tc.message)
		}
		for _, clause := range []string{"FOR ROLE dp_owner FOR USER dp_owner", "IN SCHEMA dp_s IN SCHEMA dp_s"} {
			q := "ALTER DEFAULT PRIVILEGES " + clause + " GRANT SELECT ON TABLES TO dp_reader"
			token := "FOR USER"
			if strings.HasPrefix(clause, "IN") {
				token = "IN SCHEMA"
			}
			s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "42601", Message: "conflicting or redundant options", Position: fixture.Position(strings.LastIndex(q, token) + 1)})
			s.ErrorStage = "execute"
			run(s)
		}
		dpActor(run, "dp_reader", func() {
			dpError(run, "ALTER DEFAULT PRIVILEGES FOR ROLE dp_owner GRANT SELECT ON TABLES TO dp_reader", "42501", "permission denied to change default privileges")
		})
		// Quoted operands preserve case. Lowercase remains a recognized
		// privilege; uppercase/mixed spellings must not publish policy.
		run(fixture.Exec(`ALTER DEFAULT PRIVILEGES GRANT "select" ON TABLES TO dp_reader`, 0))
		dpTable(run, "dp_quote_lower", 7)
		dpActor(run, "dp_reader", func() { dpRead(run, "dp_quote_lower", 7) })
		run(fixture.Exec(`ALTER DEFAULT PRIVILEGES REVOKE "select" ON TABLES FROM dp_reader`, 0))
		run(fixture.Exec("ALTER DEFAULT PRIVILEGES REVOKE USAGE ON TYPES FROM PUBLIC", 0))
		for _, tc := range []struct{ privilege, class, table string }{
			{"SELECT", "TABLES", "dp_quote_select_upper"},
			{"Select", "TABLES", "dp_quote_select_mixed"},
			{"Usage", "TYPES", "dp_quote_usage_mixed"},
			{"TEMP", "TABLES", "dp_quote_temp_upper"},
			{"TEMPORARY", "TABLES", "dp_quote_temporary_upper"},
		} {
			q := `ALTER DEFAULT PRIVILEGES GRANT "` + tc.privilege + `" ON ` + tc.class + " TO dp_reader"
			dpError(run, q, "42601", `unrecognized privilege type "`+tc.privilege+`"`)
			dpTable(run, tc.table, 9)
			dpActor(run, "dp_reader", func() { dpDenied(run, "SELECT x FROM "+tc.table, "table", tc.table) })
			if tc.class == "TYPES" {
				run(fixture.Exec("CREATE TYPE dp_quote_usage_type AS ENUM('a')", 0))
				dpActor(run, "dp_reader", func() {
					fixture.WithPublicSchemaOwner(t, p, b, "dp_reader", func() {
						dpDenied(run, "CREATE TABLE dp_quote_usage_consumer(x dp_quote_usage_type)", "type", "dp_quote_usage_type")
					})
				})
			}
		}
		run(fixture.Exec("ALTER DEFAULT PRIVILEGES GRANT USAGE ON TYPES TO PUBLIC", 0))
		dpActor(run, "dp_reader", func() { dpRead(run, "dp_quote_lower", 7) })
	})
}

func TestReferenceRoleDefaultPrivilegesStoredClasses(t *testing.T) {
	dpScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), prepared bool) {
		for _, tc := range []struct{ class, privilege, description string }{{"TABLES", "SELECT", "relations"}, {"SEQUENCES", "USAGE", "sequences"}, {"FUNCTIONS", "EXECUTE", "functions"}, {"ROUTINES", "EXECUTE", "functions"}, {"TYPES", "USAGE", "types"}, {"SCHEMAS", "CREATE", "schemas"}, {"LARGE OBJECTS", "UPDATE", "large objects"}} {
			run(fixture.Exec("ALTER DEFAULT PRIVILEGES FOR ROLE dp_owner GRANT "+tc.privilege+" ON "+tc.class+" TO dp_reader WITH GRANT OPTION", 0))
			dpDependency(run, "dp_owner", tc.description, "dp_owner", "", true)
			dpDependency(run, "dp_reader", tc.description, "dp_owner", "", false)
			run(fixture.Exec("ALTER DEFAULT PRIVILEGES FOR ROLE dp_owner REVOKE GRANT OPTION FOR "+tc.privilege+" ON "+tc.class+" FROM dp_reader", 0))
			dpDependency(run, "dp_reader", tc.description, "dp_owner", "", false)
			run(fixture.Exec("ALTER DEFAULT PRIVILEGES FOR ROLE dp_owner REVOKE ALL ON "+tc.class+" FROM dp_reader", 0))
		}
		// Alias mutation addresses one routine record, not two separately stored classes.
		run(fixture.Exec("ALTER DEFAULT PRIVILEGES FOR ROLE dp_owner GRANT EXECUTE ON FUNCTIONS TO dp_reader", 0))
		run(fixture.Exec("ALTER DEFAULT PRIVILEGES FOR ROLE dp_owner REVOKE EXECUTE ON ROUTINES FROM dp_reader", 0))
		// Built-in PUBLIC grants and an explicitly empty owner ACL are persistent
		// deviations too, without any user routine or large-object creation provider.
		for _, tc := range []struct{ class, privilege, description string }{{"FUNCTIONS", "EXECUTE", "functions"}, {"TYPES", "USAGE", "types"}} {
			run(fixture.Exec("ALTER DEFAULT PRIVILEGES FOR ROLE dp_owner REVOKE "+tc.privilege+" ON "+tc.class+" FROM PUBLIC", 0))
			dpDependency(run, "dp_owner", tc.description, "dp_owner", "", true)
			run(fixture.Exec("ALTER DEFAULT PRIVILEGES FOR ROLE dp_owner GRANT "+tc.privilege+" ON "+tc.class+" TO PUBLIC", 0))
		}
		run(fixture.Exec("ALTER DEFAULT PRIVILEGES FOR ROLE dp_owner REVOKE ALL ON LARGE OBJECTS FROM dp_owner", 0))
		dpDependency(run, "dp_owner", "large objects", "dp_owner", "", true)
		run(fixture.Exec("ALTER DEFAULT PRIVILEGES FOR ROLE dp_owner GRANT ALL ON LARGE OBJECTS TO dp_owner", 0))
		run(fixture.Exec("DROP ROLE dp_owner", 0))
		run(fixture.Exec("CREATE ROLE dp_owner", 0))
		run(fixture.Exec("DROP ROLE dp_reader", 0))
		run(fixture.Exec("CREATE ROLE dp_reader", 0))
	})
}
