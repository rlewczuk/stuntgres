package reference_test

import (
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/lib/pq"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceExternalSQLProviderCredentials(t *testing.T) {
	const role = "external_sql_'role"
	const password = `p'a\ss:@/ ?#%`
	fixture.RoleReferenceCleanup(t, []string{role})
	p, provider, _ := externalSQLPhysicalScenario(t)
	bridge, err := fixture.Container.ContainerIP(p.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	var method string
	if err := fixture.Admin.QueryRowContext(p.Ctx, "SELECT auth_method FROM pg_hba_file_rules WHERE type IN ('host','hostnossl') AND address='all' AND error IS NULL ORDER BY rule_number LIMIT 1").Scan(&method); err != nil || method != "scram-sha-256" {
		t.Fatal("actual host password rule", method, err)
	}
	var broadTrust int64
	if err := fixture.Admin.QueryRowContext(p.Ctx, "SELECT count(*) FROM pg_hba_file_rules WHERE type IN ('host','hostnossl') AND auth_method='trust' AND address NOT IN ('127.0.0.1','::1')").Scan(&broadTrust); err != nil || broadTrust != 0 {
		t.Fatal("unexpected earlier trusted network rule", broadTrust, err)
	}
	for _, q := range []string{
		"CREATE ROLE " + pq.QuoteIdentifier(role) + " LOGIN PASSWORD " + pq.QuoteLiteral(password),
		"GRANT USAGE ON SCHEMA external_sql_source TO " + pq.QuoteIdentifier(role),
		"GRANT SELECT ON external_sql_source.data TO " + pq.QuoteIdentifier(role),
		"CREATE VIEW external_sql_source.identity AS SELECT current_user::text AS role,current_database()::text AS db",
		"GRANT SELECT ON external_sql_source.identity TO " + pq.QuoteIdentifier(role),
	} {
		fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, fixture.Exec(q, 0))
	}
	config, err := pgx.ParseConfig(fixture.DSN)
	if err != nil {
		t.Fatal(err)
	}
	uri := (&url.URL{Scheme: "postgres", User: url.UserPassword(role, password), Host: net.JoinHostPort(config.Host, strconv.Itoa(int(config.Port))), Path: "/" + config.Database, RawQuery: "sslmode=disable"}).String()
	quote := func(s string) string { return "'" + strings.NewReplacer(`\`, `\\`, `'`, `\'`).Replace(s) + "'" }
	keyword := fmt.Sprintf("host=%s port=%d dbname=%s user=%s password=%s sslmode=disable", quote(config.Host), config.Port, quote(config.Database), quote(role), quote(password))
	for _, conninfo := range []string{keyword, uri} {
		c := externalSQLRawConnection(t, p, provider, stuntgres.ExternalSQLConnectRequest{Conninfo: conninfo})
		if c.Info().User != role || c.Info().Database != config.Database || c.Info().BackendPID <= 0 {
			t.Fatal("real reserved credential identity", c.Info())
		}
		if err := c.Close(p.Ctx); err != nil {
			t.Fatal(err)
		}
		externalSQLBackendGone(t, p.Ctx, c.Info().BackendPID)
	}
	before := len(provider.snapshot())
	wrong := stuntgres.ExternalSQLConnectRequest{ServerOptions: []stuntgres.ForeignOption{{Name: "host", Value: config.Host}, {Name: "port", Value: strconv.Itoa(int(config.Port))}, {Name: "dbname", Value: config.Database}}, MappingOptions: []stuntgres.ForeignOption{{Name: "user", Value: role}, {Name: "password", Value: "wrong"}}, MappingPresent: true}
	failed, err := provider.Open(p.Ctx, wrong)
	if failed != nil {
		failed.Close(p.Ctx)
		t.Fatal("failed credentials published a handle")
	}
	fatal := fixture.Diagnostic{Severity: "FATAL", SeverityUnlocalized: "FATAL", Code: "28P01", Message: fmt.Sprintf("password authentication failed for user %q", role)}
	fixture.AssertDiagnostic(t, err, &fatal)
	if len(provider.snapshot()) != before {
		t.Fatal("failed authentication registered a successful backend")
	}
	wrong.MappingOptions[1].Value = password
	valid := externalSQLRawConnection(t, p, provider, wrong)
	if valid.Info().User != role {
		t.Fatal("credential retry did not authenticate requested user", valid.Info())
	}
	badEndpoint := stuntgres.ExternalSQLConnectRequest{ServerOptions: []stuntgres.ForeignOption{{Name: "host", Value: "/external_sql_fixture_missing_socket"}, {Name: "port", Value: "5432"}}}
	if c, err := provider.Open(p.Ctx, badEndpoint); err == nil || c != nil {
		if c != nil {
			c.Close(p.Ctx)
		}
		t.Fatal("invalid endpoint published success", err)
	} else if _, ok := err.(interface{ SQLState() string }); ok {
		t.Fatal("transport error fabricated SQLSTATE", err)
	}

	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c := externalSQLProviderConnection(t, p, b)
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
			externalSQLProviderInstallPhysical(t, b, run)
			if b.PG {
				run(fixture.Exec("ALTER SERVER external_sql_live OPTIONS(SET host "+pq.QuoteLiteral(bridge)+")", 0))
			}
			run(fixture.Exec("ALTER USER MAPPING FOR CURRENT_USER SERVER external_sql_live OPTIONS(SET user "+pq.QuoteLiteral(role)+",SET password "+pq.QuoteLiteral(password)+")", 0))
			run(fixture.Exec("CREATE FOREIGN TABLE external_sql_identity(role text,db text) SERVER external_sql_live OPTIONS(schema_name 'external_sql_source',table_name 'identity')", 0))
			identity := fixture.Select("SELECT role,db FROM external_sql_identity", []string{"role", "db"}, []string{"TEXT", "TEXT"}, [][]any{{role, config.Database}})
			run(identity)
			run(fixture.Prepared(identity))
			run(fixture.Exec("ALTER USER MAPPING FOR CURRENT_USER SERVER external_sql_live OPTIONS(SET password 'wrong')", 0))
			want := fatal
			if b.PG {
				want = fixture.ControlsReferenceDiagnostic("08001", `could not connect to server "external_sql_live"`)
				want.Detail = fmt.Sprintf("connection to server at %q, port 5432 failed: FATAL:  password authentication failed for user %q", bridge, role)
			}
			for _, prepared := range []bool{false, true} {
				s := fixture.ErrorDiagnostic("SELECT role,db FROM external_sql_identity", want)
				s.Prepared, s.SelectRows = prepared, true
				run(s)
			}
			run(fixture.Exec("ALTER USER MAPPING FOR CURRENT_USER SERVER external_sql_live OPTIONS(SET password "+pq.QuoteLiteral(password)+")", 0))
			run(identity)
			run(fixture.Prepared(identity))
			run(fixture.Exec("DROP USER MAPPING FOR CURRENT_USER SERVER external_sql_live", 0))
			localRole := "postgres"
			if b.PG {
				localRole = "stuntgres"
			}
			for _, prepared := range []bool{false, true} {
				missing := fixture.ErrorDiagnostic("SELECT role,db FROM external_sql_identity", fixture.ControlsReferenceDiagnostic("42704", fmt.Sprintf("user mapping not found for user %q, server %q", localRole, "external_sql_live")))
				missing.SelectRows, missing.Prepared = true, prepared
				run(missing)
			}
		})
	}
}
