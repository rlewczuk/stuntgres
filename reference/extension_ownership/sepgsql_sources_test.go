package reference_test

import (
	"database/sql"
	"slices"
	"strings"
	"sync"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceSepgsqlSimulationSources(t *testing.T) {
	var mu sync.Mutex
	var events []stuntgres.SepgsqlAuditEvent
	install := func(run sepgsqlRun) {
		run(fixture.Exec("CREATE EXTENSION tablefunc", 0))
		run(fixture.Exec("CREATE EXTENSION pgcrypto", 0))
	}
	normal := func(query string, rows [][]any) fixture.Step {
		return fixture.Select(query, []string{"value"}, []string{"FLOAT8"}, rows)
	}
	sepgsqlCase(t, func(t *testing.T, p *fixture.Pair, backend fixture.Backend, c *sql.Conn, run sepgsqlRun) {
		install(run)
		run(normal("SELECT value FROM normal_rand(2,5,0) AS n(value)", [][]any{{float64(5)}, {float64(5)}}))
		run(fixture.Prepared(normal("SELECT value FROM normal_rand(2,5,0) AS n(value)", [][]any{{float64(5)}, {float64(5)}})))
		run(fixture.TablefuncNormalNegative("SELECT * FROM normal_rand(-1,0,1)"))
		run(normal("SELECT value FROM normal_rand(0,5,0) AS n(value)", nil))
		run(normal("SELECT value FROM normal_rand(NULL::integer,5,0) AS n(value)", nil))
		run(fixture.PgcryptoArmorHeaders("SELECT * FROM pgp_armor_headers($1)", [][]any{{"foo", "bar"}}, fixture.PgcryptoArmorZooka))
		run(fixture.PgcryptoArmorCorrupt("SELECT * FROM pgp_armor_headers($1)", "not armor"))
	}, func(config *stuntgres.SepgsqlSimulationConfig) {
		config.AuditAllowed = true
		config.Audit = func(event stuntgres.SepgsqlAuditEvent) { mu.Lock(); events = append(events, event); mu.Unlock() }
		config.Policy.Rules = append(config.Policy.Rules, stuntgres.SepgsqlRule{Source: sepgsqlR, Target: sepgsqlU, Class: "db_procedure", Allow: []string{"execute"}})
		config.Policy.TrustedTransitions = append(config.Policy.TrustedTransitions, stuntgres.SepgsqlTrustedTransition{Source: sepgsqlR, Procedure: sepgsqlU, Result: sepgsqlT})
	}, func(t *testing.T, p *fixture.Pair, backend fixture.Backend, c *sql.Conn, run sepgsqlRun) {
		install(run)
		for _, signature := range []string{"normal_rand(integer,double precision,double precision)", "pgp_armor_headers(text)"} {
			run(fixture.Exec("SECURITY LABEL ON FUNCTION "+signature+" IS '"+sepgsqlS+"'", 0))
		}
		run(sepgsqlSet(sepgsqlR))
		countEntries := func(query fixture.Step, name string, want int) {
			t.Helper()
			mu.Lock()
			events = nil
			mu.Unlock()
			run(query)
			mu.Lock()
			observed := slices.Clone(events)
			mu.Unlock()
			count := 0
			for _, event := range observed {
				if event.Subject == sepgsqlR && event.Target == sepgsqlS && event.Class == "db_procedure" && strings.Contains(event.Name, name) && slices.Contains(event.Required, "entrypoint") && len(event.Denied) == 0 {
					count++
				}
			}
			// Exact native protocol: first-init+row is ONE invocation, each later row
			// and consumed DONE is another. This is not a PostgreSQL AVC-count oracle.
			if count != want {
				t.Fatalf("%s native invocation entries: got%d want%d: %#v", name, count, want, observed)
			}
			run(sepgsqlText("SELECT sepgsql_getcon() AS context", "context", sepgsqlR))
		}
		countEntries(fixture.Select("SELECT value,sepgsql_getcon() AS subject FROM normal_rand(2,5,0) AS n(value)", []string{"value", "subject"}, []string{"FLOAT8", "TEXT"}, [][]any{{float64(5), sepgsqlR}, {float64(5), sepgsqlR}}), "normal_rand", 3)
		countEntries(normal("SELECT value FROM normal_rand(0,5,0) AS n(value)", nil), "normal_rand", 1)
		countEntries(normal("SELECT value FROM normal_rand(NULL::integer,5,0) AS n(value)", nil), "normal_rand", 0)
		countEntries(fixture.PgcryptoArmorHeaders("SELECT * FROM pgp_armor_headers($1)", [][]any{{"foo", "bar"}}, fixture.PgcryptoArmorZooka), "pgp_armor_headers", 2)
		countEntries(fixture.PgcryptoArmorHeaders("SELECT * FROM pgp_armor_headers(NULL::text)", nil), "pgp_armor_headers", 0)
		run(sepgsqlBool("SELECT sepgsql_setcon(NULL) AS changed", "changed", true))
		for _, signature := range []string{"normal_rand(integer,double precision,double precision)", "pgp_armor_headers(text)"} {
			run(fixture.Exec("SECURITY LABEL ON FUNCTION "+signature+" IS '"+sepgsqlU+"'", 0))
		}
		run(sepgsqlSet(sepgsqlR))
		run(sepgsqlDenied("SELECT * FROM normal_rand(-1,0,1)"))
		run(sepgsqlDenied("SELECT * FROM pgp_armor_headers('not armor')"))
		run(sepgsqlText("SELECT sepgsql_getcon() AS context", "context", sepgsqlR))
		run(sepgsqlBool("SELECT sepgsql_setcon(NULL) AS changed", "changed", true))
		for _, signature := range []string{"normal_rand(integer,double precision,double precision)", "pgp_armor_headers(text)"} {
			run(fixture.Exec("SECURITY LABEL ON FUNCTION "+signature+" IS '"+sepgsqlS+"'", 0))
		}
		run(sepgsqlSet(sepgsqlR))
		run(fixture.TablefuncNormalNegative("SELECT * FROM normal_rand(-1,0,1)"))
		run(fixture.PgcryptoArmorCorrupt("SELECT * FROM pgp_armor_headers($1)", "not armor"))
		run(sepgsqlText("SELECT sepgsql_getcon() AS context", "context", sepgsqlR))
	})
}
