package reference_test

import (
	"fmt"
	"testing"

	"github.com/lib/pq"
	"stuntgres/reference/fixture"
)

func TestReferenceDatabaseCreateOptionDiagnostics(t *testing.T) {
	// Expectations follow REL_18_6 createdb: written-order option recognition,
	// then encoding/provider/limit validation, then owner and actor checks.
	quotedName := "dc_option_a\"b\\c"
	missingName := "dc_option_missing\"b\\c"
	unknownName := "UNKNOWN\"b\\c"
	cases := []struct {
		name, options, role, code, message, position string
		accept, duplicate, selector                  bool
	}{
		{name: "quoted_lowercase", options: `"encoding" 'UTF8'`, accept: true},
		{name: "allow_true", options: "ALLOW_CONNECTIONS true", accept: true},
		{name: "allow_false", options: "ALLOW_CONNECTIONS false", accept: true},
		{name: "allow_default", options: "ALLOW_CONNECTIONS DEFAULT", accept: true},
		{name: "allow_on", options: "ALLOW_CONNECTIONS 'on'", accept: true},
		{name: "allow_off", options: "ALLOW_CONNECTIONS 'off'", accept: true},
		{name: "limit_unlimited", options: "CONNECTION LIMIT -1", accept: true},
		{name: "limit_zero", options: "CONNECTION LIMIT 0", accept: true},
		{name: "limit_one", options: "CONNECTION LIMIT 1", accept: true},
		{name: "limit_default", options: "CONNECTION LIMIT DEFAULT", accept: true},
		{name: "allow_duplicate", options: "ALLOW_CONNECTIONS true ALLOW_CONNECTIONS false", code: "42601", message: "conflicting or redundant options", position: "ALLOW_CONNECTIONS"},
		{name: "quoted_uppercase", options: `"ENCODING" 'UTF8'`, code: "42601", message: `option "ENCODING" not recognized`, position: `"ENCODING"`},
		{name: "recognized_then_distinct_uppercase", options: `ENCODING 'UTF8' "ENCODING" 'UTF8'`, code: "42601", message: `option "ENCODING" not recognized`, position: `"ENCODING"`},
		{name: "unknown_before_redundant", options: `"ENCODING" 'UTF8' ENCODING 'UTF8' ENCODING 'UTF8'`, code: "42601", message: `option "ENCODING" not recognized`, position: `"ENCODING"`},
		{name: "redundant_before_unknown", options: `ENCODING 'UTF8' "encoding" 'UTF8' "ENCODING" 'UTF8'`, code: "42601", message: "conflicting or redundant options", position: `"encoding"`},
		{name: "decoded_duplicate", duplicate: true, code: "42P04", message: "database \"" + quotedName + "\" already exists"},
		{name: "decoded_missing_owner", options: "OWNER " + pq.QuoteIdentifier(missingName), code: "42704", message: "role \"" + missingName + "\" does not exist"},
		{name: "decoded_missing_template", options: "TEMPLATE " + pq.QuoteIdentifier(missingName), code: "3D000", message: "template database \"" + missingName + "\" does not exist"},
		{name: "decoded_unknown_option", options: pq.QuoteIdentifier(unknownName) + " 'x'", code: "42601", message: "option \"" + unknownName + "\" not recognized", position: pq.QuoteIdentifier(unknownName)},
		{name: "decoded_missing_selector", selector: true},
		{name: "provider_invalid", options: "LOCALE_PROVIDER 'not_a_provider'", code: "42P17", message: "unrecognized locale provider: not_a_provider"},
		{name: "provider_before_missing_owner", options: "OWNER dc_option_missing_role LOCALE_PROVIDER 'not_a_provider'", code: "42P17", message: "unrecognized locale provider: not_a_provider"},
		{name: "provider_before_denied_actor", options: "LOCALE_PROVIDER 'not_a_provider'", role: "dc_denied", code: "42P17", message: "unrecognized locale provider: not_a_provider"},
		{name: "provider_before_limit", options: "CONNECTION LIMIT -2 LOCALE_PROVIDER 'not_a_provider'", code: "42P17", message: "unrecognized locale provider: not_a_provider"},
		{name: "encoding_before_provider", options: "LOCALE_PROVIDER 'not_a_provider' ENCODING 'NO_SUCH_ENCODING'", code: "42704", message: "NO_SUCH_ENCODING is not a valid encoding name", position: "ENCODING "},
		{name: "unknown_before_provider", options: "LOCALE_PROVIDER 'not_a_provider' bogus 'x'", code: "42601", message: `option "bogus" not recognized`, position: "bogus"},
		{name: "limit_actual_value", options: "CONNECTION LIMIT -2", code: "22023", message: "invalid connection limit: -2"},
		{name: "provider_before_duplicate", options: "LOCALE_PROVIDER 'not_a_provider'", duplicate: true, code: "42P17", message: "unrecognized locale provider: not_a_provider"},
		{name: "limit_before_duplicate", options: "CONNECTION LIMIT -2", duplicate: true, code: "22023", message: "invalid connection limit: -2"},
		{name: "limit_before_missing_owner", options: "OWNER dc_option_missing_role CONNECTION LIMIT -2", code: "22023", message: "invalid connection limit: -2"},
		{name: "limit_before_denied_actor", options: "CONNECTION LIMIT -2", role: "dc_denied", code: "22023", message: "invalid connection limit: -2"},
		{name: "limit_before_missing_template", options: "TEMPLATE dc_option_missing_template CONNECTION LIMIT -2", code: "22023", message: "invalid connection limit: -2"},
		{name: "redundant_before_limit", options: "CONNECTION LIMIT -2 CONNECTION LIMIT -2", code: "42601", message: "conflicting or redundant options", position: "CONNECTION"},
	}
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fixture.RoleReferenceCleanup(t, databaseCreateRoles)
			names := []string{}
			fixture.DatabaseReferenceCleanup(t, &names)
			p := fixture.NewScenario(t)
			for _, backend := range p.Backends() {
				t.Run(backend.Name, func(t *testing.T) {
					conn, err := backend.DB.Conn(p.Ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer conn.Close()
					c := &databaseCreateCase{t, p, backend, conn, &names}
					original := c.scalar("SELECT current_database()")
					name := fmt.Sprintf("dc_option_%02d", i)
					if tc.duplicate {
						name = quotedName
						c.create(name, "", false)
					}
					c.remember(name)
					if tc.role != "" {
						c.run(fixture.Exec("CREATE ROLE "+tc.role, 0))
						c.run(fixture.Exec("SET ROLE "+tc.role, 0))
					}

					// Keep the same successful prepared query across failed CREATEs
					// and selector admission, proving the physical owner remains usable.
					healthSQL := "SELECT 42 AS healthy"
					healthStmt, err := conn.PrepareContext(p.Ctx, healthSQL)
					if err != nil {
						t.Fatal(err)
					}
					defer healthStmt.Close()
					health := fixture.HeldStatement{Executor: conn, Statement: healthStmt, SQL: healthSQL}
					checkHealth := func() {
						t.Helper()
						fixture.RunStep(t, p.Ctx, health, backend.Recorder, fixture.Select(healthSQL, []string{"healthy"}, []string{"INT4"}, [][]any{{int64(42)}}))
						if got := c.scalar("SELECT current_database()"); got != original {
							t.Fatalf("failed request retargeted database to %q", got)
						}
					}
					if tc.selector {
						c.remember(missingName)
						c.absent(missingName)
						selected, _, _ := c.pool(missingName, "")
						want := databaseCreateDiagnostic("3D000", "database \""+missingName+"\" does not exist")
						want.Severity, want.SeverityUnlocalized = "FATAL", "FATAL"
						fixture.AssertDiagnostic(t, selected.PingContext(p.Ctx), &want)
						c.absent(missingName)
						checkHealth()
						return
					}
					if tc.accept {
						for _, prepared := range []bool{false, true} {
							target := name + "_direct"
							if prepared {
								target = name + "_prepared"
							}
							c.absent(target)
							c.create(target, tc.options, prepared)
							c.run(fixture.Select("SELECT datname FROM pg_database WHERE datname=$1", []string{"datname"}, []string{"NAME"}, [][]any{{target}}, target))
							checkHealth()
						}
						return
					}
					var existingOID string
					checkRegistry := func() {
						t.Helper()
						if !tc.duplicate {
							c.absent(name)
							return
						}
						c.run(fixture.Select("SELECT datname FROM pg_database WHERE datname=$1", []string{"datname"}, []string{"NAME"}, [][]any{{name}}, name))
						oid := c.scalar("SELECT oid::text FROM pg_database WHERE datname=$1", name)
						if existingOID != "" && oid != existingOID {
							t.Fatalf("failed duplicate replaced database OID %s with %s", existingOID, oid)
						}
						existingOID = oid
					}
					query := "CREATE DATABASE /*é*/ " + pq.QuoteIdentifier(name) + " " + tc.options
					want := databaseCreateDiagnostic(tc.code, tc.message)
					if tc.position != "" {
						want.Position = databaseCreatePosition(query, tc.position, true)
					}
					checkRegistry()
					c.run(fixture.ErrorDiagnostic(query, want))
					checkRegistry()
					checkHealth()
					stmt, err := conn.PrepareContext(p.Ctx, query)
					if err != nil {
						t.Fatalf("CREATE preparation must be quiet: %v", err)
					}
					defer stmt.Close()
					checkRegistry()
					held := fixture.HeldStatement{Executor: conn, Statement: stmt, SQL: query}
					for range 2 {
						step := fixture.ErrorDiagnostic(query, want)
						step.ErrorStage = "execute"
						fixture.RunStep(t, p.Ctx, held, backend.Recorder, step)
						checkRegistry()
						checkHealth()
					}
					if tc.role != "" {
						c.run(fixture.Exec("RESET ROLE", 0))
					}
				})
			}
		})
	}
}
