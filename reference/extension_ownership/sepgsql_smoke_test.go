package reference_test

import (
	"database/sql"
	"strings"
	"sync"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func sepgsqlSmokeConfig(config *stuntgres.SepgsqlSimulationConfig) {
	config.Policy.TrustedTransitions = append(config.Policy.TrustedTransitions,
		stuntgres.SepgsqlTrustedTransition{Source: sepgsqlR, Procedure: sepgsqlU, Result: sepgsqlT})
	config.Policy.CreateTransitions = append(config.Policy.CreateTransitions,
		stuntgres.SepgsqlCreateTransition{Source: "*", Parent: sepgsqlP, Class: "db_sequence", Name: "sepgsql_smoke_sequence_q", Result: sepgsqlS},
		stuntgres.SepgsqlCreateTransition{Source: "*", Parent: sepgsqlP, Class: "db_sequence", Name: "sepgsql_smoke_sequence_denied", Result: sepgsqlS})
	config.Policy.Rules = append(config.Policy.Rules,
		stuntgres.SepgsqlRule{Source: sepgsqlR, Target: sepgsqlP, Class: "db_schema", Allow: []string{"add_name"}},
		stuntgres.SepgsqlRule{Source: sepgsqlR, Target: sepgsqlP, Class: "db_sequence", Allow: []string{"create"}},
		stuntgres.SepgsqlRule{Source: sepgsqlR, Target: sepgsqlP, Class: "db_schema", Allow: []string{"remove_name"}},
		stuntgres.SepgsqlRule{Source: sepgsqlR, Target: sepgsqlP, Class: "db_table", Allow: []string{"drop"}},
		stuntgres.SepgsqlRule{Source: sepgsqlR, Target: sepgsqlP, Class: "db_column", Allow: []string{"drop"}},
		stuntgres.SepgsqlRule{Source: sepgsqlR, Target: sepgsqlP, Class: "db_sequence", Allow: []string{"drop"}},
		stuntgres.SepgsqlRule{Source: sepgsqlT, Target: sepgsqlS, Class: "db_procedure", Deny: []string{"execute"}},
		stuntgres.SepgsqlRule{Source: sepgsqlT, Target: "*", Class: "process", Allow: []string{"setcurrent", "dyntransition"}})
}

// These are genuine feature operations in one physical-session scenario, not
// calls to other tests or a suite alias. Stock active-policy parity is not claimed.
func sepgsqlSmokeActive(t *testing.T, p *fixture.Pair, c *sql.Conn, run sepgsqlRun) {
	sepgsqlViewMetadata(run, true)
	sepgsqlTable(run)
	run(fixture.Exec("CREATE SEQUENCE sepgsql_smoke_sequence_p START 5 RESTART 9 CACHE 3", 0))
	run(fixture.Exec("CREATE SEQUENCE sepgsql_smoke_sequence_q", 0))
	sepgsqlSequenceAssociation(run, "sepgsql_smoke_sequence_p", sepgsqlP, true)
	sepgsqlSequenceAssociation(run, "sepgsql_smoke_sequence_q", sepgsqlS, true)
	sepgsqlSequenceState(run, "sepgsql_smoke_sequence_p", 9, false)
	run(sepgsqlSet(sepgsqlR))
	run(sepgsqlDenied("CREATE SEQUENCE sepgsql_smoke_sequence_denied"))
	run(sepgsqlCount("SELECT count(*) FROM pg_class WHERE relname='sepgsql_smoke_sequence_denied'", 0))
	run(sepgsqlBool("SELECT sepgsql_setcon(NULL) AS changed", "changed", true))
	run(sepgsqlText("SELECT sepgsql_getcon() AS context", "context", sepgsqlA))
	run(fixture.ImplicitError("SECURITY LABEL FOR sepgsql ON TABLE sepgsql_items IS NULL", fixture.Diagnostic{Code: "22023", Message: "security label provider \"sepgsql\" is not loaded"}))
	run(fixture.Exec("CREATE FUNCTION sepgsql_smoke_context() RETURNS text LANGUAGE sql AS 'SELECT sepgsql_getcon()'", 0))
	run(fixture.Exec("SECURITY LABEL ON FUNCTION sepgsql_smoke_context() IS '"+sepgsqlS+"'", 0))
	run(sepgsqlSet(sepgsqlR))
	run(sepgsqlText("SELECT sepgsql_smoke_context() AS context", "context", sepgsqlT))
	run(sepgsqlText("SELECT sepgsql_getcon() AS context", "context", sepgsqlR))
	run(sepgsqlCount("SELECT count(*) FROM sepgsql_items", 2))
	run(sepgsqlDenied("UPDATE sepgsql_items SET v='denied'"))
	run(fixture.Exec("BEGIN", 0))
	run(fixture.Exec("SAVEPOINT smoke_context", 0))
	run(sepgsqlBool("SELECT sepgsql_setcon(NULL) AS changed", "changed", true))
	run(fixture.Exec("ROLLBACK TO smoke_context", 0))
	run(sepgsqlText("SELECT sepgsql_getcon() AS context", "context", sepgsqlR))
	run(fixture.Exec("COMMIT", 0))
	run(sepgsqlBool("SELECT sepgsql_setcon(NULL) AS changed", "changed", true))

	run(fixture.Exec("CREATE FUNCTION smoke_argument(value boolean) RETURNS text LANGUAGE sql VOLATILE AS 'SELECT sepgsql_getcon()'", 0))
	run(fixture.Exec("CREATE FUNCTION smoke_denied_argument(value boolean) RETURNS text LANGUAGE sql VOLATILE AS 'SELECT sepgsql_getcon()'", 0))
	run(fixture.Exec("SECURITY LABEL ON FUNCTION smoke_argument(boolean) IS '"+sepgsqlS+"'", 0))
	run(fixture.Exec("SECURITY LABEL ON FUNCTION smoke_denied_argument(boolean) IS '"+sepgsqlU+"'", 0))
	run(sepgsqlSet(sepgsqlR))
	// R execute is consumed before the argument changes the actual subject.
	// T explicitly denies execute on S, but no postargument replay is lawful.
	run(sepgsqlText("SELECT smoke_argument(sepgsql_setcon('"+sepgsqlT+"')) AS context", "context", sepgsqlT))
	run(sepgsqlText("SELECT sepgsql_getcon() AS context", "context", sepgsqlT))
	run(sepgsqlBool("SELECT sepgsql_setcon(NULL) AS changed", "changed", true))
	run(sepgsqlSet(sepgsqlR))
	run(sepgsqlDenied("SELECT smoke_denied_argument(sepgsql_setcon('" + sepgsqlT + "'))"))
	run(sepgsqlText("SELECT sepgsql_getcon() AS context", "context", sepgsqlR))
	run(sepgsqlBool("SELECT sepgsql_setcon(NULL) AS changed", "changed", true))

	// Real helper descriptors, a VPC empty invocation and materialized output.
	run(fixture.Exec("CREATE EXTENSION intagg", 0))
	run(fixture.Exec("CREATE EXTENSION tablefunc", 0))
	run(fixture.Exec("CREATE EXTENSION unaccent", 0))
	run(fixture.Exec("SECURITY LABEL ON FUNCTION int_agg_state(internal,integer) IS '"+sepgsqlS+"'", 0))
	run(fixture.Exec("SECURITY LABEL ON FUNCTION normal_rand(integer,double precision,double precision) IS '"+sepgsqlS+"'", 0))
	run(sepgsqlSet(sepgsqlR))
	run(sepgsqlText("SELECT int_array_aggregate(id ORDER BY id)::text AS values FROM sepgsql_items", "values", "{1,2}"))
	run(sepgsqlCount("SELECT count(*) FROM normal_rand(0,0,1)", 0))
	run(sepgsqlCount("SELECT count(*) FROM crosstab('SELECT 1,1,7') AS result(row_id integer,value integer)", 1))
	run(sepgsqlCount("SELECT count(*) FROM ts_debug('pg_catalog.simple','word')", 1))
	run(sepgsqlText("SELECT sepgsql_getcon() AS context", "context", sepgsqlR))
	run(fixture.ImplicitError("SELECT int_agg_state(NULL,1)", fixture.Diagnostic{Code: "42883", Message: "function int_agg_state(unknown, integer) does not exist", Hint: "No function matches the given name and argument types. You might need to add explicit type casts.", Position: "8"}))
	run(sepgsqlBool("SELECT sepgsql_setcon(NULL) AS changed", "changed", true))

	// Warming is genuine work, but no warm receipt is inherited by a new session.
	run(sepgsqlText("SELECT ts_lexize('unaccent','café')::text AS lexemes", "lexemes", "{cafe}"))
	run(fixture.Exec("SECURITY LABEL ON FUNCTION unaccent_init(internal) IS '"+sepgsqlU+"'", 0))
	run(sepgsqlSet(sepgsqlR))
	run(sepgsqlText("SELECT ts_lexize('unaccent','café')::text AS lexemes", "lexemes", "{cafe}"))
	peer := sql.OpenDB(p.NativeConnector.WithSepgsqlContext(sepgsqlR))
	defer peer.Close()
	_, err := peer.ExecContext(p.Ctx, "SELECT ts_lexize('unaccent','word')")
	fixture.AssertDiagnostic(t, err, &fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42501", Message: "SELinux: security policy violation"})
	run(sepgsqlBool("SELECT sepgsql_setcon(NULL) AS changed", "changed", true))

	// One denied owned child must prevent every selected root effect.
	run(fixture.Exec("CREATE TABLE smoke_drop_first(id integer)", 0))
	run(fixture.Exec("CREATE TABLE smoke_drop_last(id integer)", 0))
	run(fixture.Exec("INSERT INTO smoke_drop_first VALUES(7)", 1))
	run(fixture.Exec("CREATE SEQUENCE smoke_owned OWNED BY smoke_drop_first.id", 0))
	run(fixture.Exec("SECURITY LABEL ON SEQUENCE smoke_owned IS '"+sepgsqlS+"'", 0))
	run(sepgsqlSet(sepgsqlR))
	run(sepgsqlDenied("DROP TABLE smoke_drop_first,smoke_drop_last"))
	run(sepgsqlCount("SELECT count(*) FROM smoke_drop_first", 1))
	run(sepgsqlCount("SELECT count(*) FROM pg_class WHERE relname IN ('smoke_drop_first','smoke_drop_last','smoke_owned')", 3))
	run(sepgsqlBool("SELECT sepgsql_setcon(NULL) AS changed", "changed", true))
	run(fixture.Exec("SECURITY LABEL ON SEQUENCE smoke_owned IS '"+sepgsqlP+"'", 0))
	run(sepgsqlSet(sepgsqlR))
	run(fixture.Exec("DROP TABLE smoke_drop_first,smoke_drop_last", 0))
	run(sepgsqlBool("SELECT sepgsql_setcon(NULL) AS changed", "changed", true))

	run(fixture.ImplicitError("SET sepgsql.permissive=on", fixture.Diagnostic{Code: "55P02", Message: "parameter \"sepgsql.permissive\" cannot be changed now"}))
	run(sepgsqlText("SELECT sepgsql_mcstrans_out('"+sepgsqlP+"') AS label", "label", "Public display label"))
	run(fixture.Exec("SECURITY LABEL ON TABLE sepgsql_items IS '"+sepgsqlS+"'", 0))
	run(sepgsqlBool("SELECT sepgsql_restorecon('public') AS restored", "restored", true))
	run(sepgsqlCount("SELECT count(*) FROM pg_seclabel WHERE objoid='sepgsql_items'::regclass AND objsubid=0 AND label='"+sepgsqlP+"'", 1))
	run(fixture.Exec("SECURITY LABEL ON TABLE sepgsql_items IS '"+sepgsqlS+"'", 0))
	dump := fixture.ExtensionDump(t, p.Ctx, p.Native)
	if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(dump)); err != nil {
		t.Fatal(err)
	}
	run(sepgsqlCount("SELECT count(*) FROM sepgsql_items", 2))
	run(sepgsqlCount("SELECT count(*) FROM pg_seclabels WHERE objoid='sepgsql_items'::regclass AND label='"+sepgsqlS+"'", 1))
	sepgsqlSequenceAssociation(run, "sepgsql_smoke_sequence_p", sepgsqlP, true)
	sepgsqlSequenceAssociation(run, "sepgsql_smoke_sequence_q", sepgsqlS, true)
	sepgsqlSequenceState(run, "sepgsql_smoke_sequence_p", 9, false)
	run(sepgsqlSet(sepgsqlR))
	run(sepgsqlDenied("SELECT id FROM sepgsql_items"))
}

func sepgsqlSmokePermissive(t *testing.T) {
	t.Run("permissive_preserves_real_denial", func(t *testing.T) {
		var mu sync.Mutex
		var events []stuntgres.SepgsqlAuditEvent
		sepgsqlCase(t, func(t *testing.T, p *fixture.Pair, backend fixture.Backend, c *sql.Conn, run sepgsqlRun) {
			sepgsqlOrdinaryTable(t, p, c, run)
			run(fixture.Exec("SET client_min_messages=warning", 0))
			run(sepgsqlText("SELECT current_setting('client_min_messages') AS setting", "setting", "warning"))
		}, func(config *stuntgres.SepgsqlSimulationConfig) {
			config.Permissive = true
			config.Policy.Rules = append(config.Policy.Rules,
				stuntgres.SepgsqlRule{Source: sepgsqlR, Target: sepgsqlP, Class: "db_schema", Allow: []string{"add_name"}})
			config.Audit = func(event stuntgres.SepgsqlAuditEvent) { mu.Lock(); events = append(events, event); mu.Unlock() }
		}, func(t *testing.T, p *fixture.Pair, backend fixture.Backend, c *sql.Conn, run sepgsqlRun) {
			sepgsqlTable(run)
			run(fixture.Exec("SECURITY LABEL ON TABLE sepgsql_items IS '"+sepgsqlS+"'", 0))
			run(sepgsqlSet(sepgsqlR))
			run(sepgsqlCount("SELECT count(*) FROM sepgsql_items", 2))
			run(fixture.Exec("CREATE SEQUENCE sepgsql_smoke_permissive START 47", 0))
			sepgsqlSequenceAssociation(run, "sepgsql_smoke_permissive", sepgsqlP, true)
			mu.Lock()
			captured := append([]stuntgres.SepgsqlAuditEvent(nil), events...)
			mu.Unlock()
			found := false
			sequenceDenied := false
			for _, event := range captured {
				if event.Subject == sepgsqlR && event.Target == sepgsqlS && event.Class == "db_table" && event.Permissive && len(event.Denied) == 1 && event.Denied[0] == "select" {
					found = true
				}
				if event.Subject == sepgsqlR && event.Target == sepgsqlP && event.Class == "db_sequence" && event.Permissive && len(event.Denied) == 1 && event.Denied[0] == "create" && strings.Contains(event.Name, "sepgsql_smoke_permissive") {
					sequenceDenied = true
				}
			}
			if !found {
				t.Fatal("permissive smoke did not retain actual denied select", captured)
			}
			if !sequenceDenied {
				t.Fatal("permissive smoke did not retain actual denied sequence create", captured)
			}
			sepgsqlAuditClient(t, p)
		})
	})
}
