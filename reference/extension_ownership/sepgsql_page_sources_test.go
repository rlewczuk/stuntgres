package reference_test

import (
	"database/sql"
	"slices"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"sync"
	"testing"
)

func TestReferenceSepgsqlSimulationPageSources(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"callback_reader"})
	const dblinkProcedure = "test_u:object_r:dblink_source_t:s0"
	const dblinkSubject = "test_u:test_r:dblink_source_t:s0"
	labelFor := func(name string) string {
		if name == "dblink_get_pkey" {
			return dblinkProcedure
		}
		return sepgsqlS
	}
	var mu sync.Mutex
	var events []stuntgres.SepgsqlAuditEvent
	setup := func(run sepgsqlRun) {
		for _, extension := range []string{"hstore", "dblink", "pg_visibility", "pageinspect"} {
			run(fixture.Exec("CREATE EXTENSION "+extension, 0))
		}
		run(fixture.Exec("CREATE TABLE callback_keys(a integer,b integer,PRIMARY KEY(b,a))", 0))
		run(fixture.Exec("CREATE TABLE callback_empty(a integer)", 0))
		run(fixture.Exec("CREATE TABLE callback_nulls(raw bytea,rel regclass,name text)", 0))
		run(fixture.Exec("INSERT INTO callback_nulls VALUES(NULL,NULL,NULL)", 1))
		run(fixture.Exec("CREATE ROLE callback_reader", 0))
		run(fixture.Exec("GRANT USAGE ON SCHEMA public TO callback_reader", 0))
	}
	cases := []struct {
		query, name, signature string
		rows, entries          int64
	}{
		{"SELECT count(*) AS count FROM skeys('a=>1,b=>2'::hstore)", "skeys", "skeys(hstore)", 2, 3},
		{"SELECT count(*) AS count FROM svals('a=>NULL,b=>2'::hstore)", "svals", "svals(hstore)", 2, 3},
		{"SELECT count(*) AS count FROM dblink_get_pkey('callback_keys')", "dblink_get_pkey", "dblink_get_pkey(text)", 2, 3},
		{"SELECT count(*) AS count FROM dblink_get_pkey((SELECT name FROM callback_nulls))", "dblink_get_pkey", "dblink_get_pkey(text)", 0, 0},
		{"SELECT count(*) AS count FROM pg_visibility_map('callback_empty')", "pg_visibility_map", "pg_visibility_map(regclass)", 0, 1},
		{"SELECT count(*) AS count FROM pg_visibility_map((SELECT rel FROM callback_nulls))", "pg_visibility_map", "pg_visibility_map(regclass)", 0, 0},
		{"SELECT count(*) AS count FROM pg_visibility('callback_empty')", "pg_visibility", "pg_visibility(regclass)", 0, 1},
		{"SELECT count(*) AS count FROM pg_visibility((SELECT rel FROM callback_nulls))", "pg_visibility", "pg_visibility(regclass)", 0, 0},
		{"SELECT count(*) AS count FROM heap_page_items(decode(repeat('00',24),'hex'))", "heap_page_items", "heap_page_items(bytea)", 0, 1},
		{"SELECT count(*) AS count FROM heap_page_items((SELECT raw FROM callback_nulls))", "heap_page_items", "heap_page_items(bytea)", 0, 0},
		{"SELECT count(*) AS count FROM bt_page_items(decode(repeat('00',8192),'hex'))", "bt_page_items", "bt_page_items(bytea)", 1, 1},
	}
	sepgsqlCase(t, func(t *testing.T, p *fixture.Pair, backend fixture.Backend, c *sql.Conn, run sepgsqlRun) {
		setup(run)
		for _, tc := range cases {
			step := sepgsqlCount(tc.query, tc.rows)
			run(step)
			run(fixture.Prepared(step))
		}
		run(fixture.Exec("SET ROLE callback_reader", 0))
		denied := fixture.ImplicitError("SELECT count(*) AS count FROM dblink_get_pkey('callback_keys')", fixture.Diagnostic{Code: "42501", Message: "permission denied for table callback_keys"})
		run(denied)
		run(fixture.Prepared(denied))
		run(fixture.Exec("RESET ROLE", 0))
		run(fixture.Exec("GRANT SELECT ON callback_keys TO callback_reader", 0))
		run(fixture.Exec("SET ROLE callback_reader", 0))
		allowed := sepgsqlCount("SELECT count(*) AS count FROM dblink_get_pkey('callback_keys')", 2)
		run(allowed)
		run(fixture.Prepared(allowed))
		run(fixture.Exec("RESET ROLE", 0))
	}, func(config *stuntgres.SepgsqlSimulationConfig) {
		config.Policy.Contexts = append(config.Policy.Contexts, dblinkProcedure, dblinkSubject)
		config.Policy.Rules = append(config.Policy.Rules,
			stuntgres.SepgsqlRule{Source: sepgsqlR, Target: dblinkProcedure, Class: "db_procedure", Allow: []string{"execute", "entrypoint"}},
			stuntgres.SepgsqlRule{Source: dblinkSubject, Target: sepgsqlP, Class: "db_database", Allow: []string{"access"}},
			stuntgres.SepgsqlRule{Source: dblinkSubject, Target: sepgsqlP, Class: "db_schema", Allow: []string{"search"}},
		)
		config.Policy.TrustedTransitions = append(config.Policy.TrustedTransitions, stuntgres.SepgsqlTrustedTransition{Source: sepgsqlR, Procedure: dblinkProcedure, Result: dblinkSubject})
		// The entered dblink subject has no procedure execute or table/column
		// SELECT. Released dblink.c reads PK catalog metadata, not SQL tuples.
		config.AuditAllowed = true
		config.Audit = func(event stuntgres.SepgsqlAuditEvent) { mu.Lock(); events = append(events, event); mu.Unlock() }
	}, func(t *testing.T, p *fixture.Pair, backend fixture.Backend, c *sql.Conn, run sepgsqlRun) {
		setup(run)
		for _, tc := range cases {
			run(fixture.Exec("SECURITY LABEL ON FUNCTION "+tc.signature+" IS '"+labelFor(tc.name)+"'", 0))
		}
		run(fixture.Exec("SECURITY LABEL ON TABLE callback_keys IS '"+sepgsqlS+"'", 0))
		run(fixture.Exec("SET ROLE callback_reader", 0))
		denied := fixture.ImplicitError("SELECT count(*) AS count FROM dblink_get_pkey('callback_keys')", fixture.Diagnostic{Code: "42501", Message: "permission denied for table callback_keys"})
		run(denied)
		run(fixture.Prepared(denied))
		run(fixture.Exec("RESET ROLE", 0))
		run(fixture.Exec("GRANT SELECT ON callback_keys TO callback_reader", 0))
		run(sepgsqlSet(sepgsqlR))
		for _, tc := range cases {
			for _, prepared := range []bool{false, true} {
				mu.Lock()
				events = nil
				mu.Unlock()
				step := sepgsqlCount(tc.query, tc.rows)
				if prepared {
					step = fixture.Prepared(step)
				}
				run(step)
				mu.Lock()
				observed := slices.Clone(events)
				mu.Unlock()
				entries := int64(0)
				for _, event := range observed {
					if event.Subject == sepgsqlR && event.Target == labelFor(tc.name) && event.Class == "db_procedure" && strings.Contains(event.Name, tc.name) && slices.Contains(event.Required, "entrypoint") && len(event.Denied) == 0 {
						entries++
					}
				}
				if entries != tc.entries {
					t.Fatalf("native %s actual row/DONE entries: got %d want %d: %#v", tc.name, entries, tc.entries, observed)
				}
				if tc.name == "dblink_get_pkey" {
					executions := 0
					for _, event := range observed {
						if event.Class == "db_procedure" && event.Target == dblinkProcedure && slices.Contains(event.Required, "execute") {
							if event.Subject != sepgsqlR || len(event.Denied) != 0 {
								t.Fatalf("dblink replayed execute inside its borrowed body: %#v", event)
							}
							executions++
						}
						if event.Subject == dblinkSubject && (event.Class == "db_table" || event.Class == "db_column") {
							t.Fatalf("dblink invented DML MAC for PK metadata: %#v", event)
						}
					}
					if executions != 1 {
						t.Fatalf("dblink lost actual expression initialization: got %d want 1: %#v", executions, observed)
					}
				}
				run(sepgsqlText("SELECT sepgsql_getcon() AS context", "context", sepgsqlR))
			}
		}
		// Ordinary table DML still has MAC even though PK metadata does not.
		run(sepgsqlDenied("SELECT count(*) AS count FROM callback_keys"))
		run(fixture.Prepared(sepgsqlDenied("SELECT count(*) AS count FROM callback_keys")))
		run(fixture.Exec("SET ROLE callback_reader", 0))
		allowed := sepgsqlCount("SELECT count(*) AS count FROM dblink_get_pkey('callback_keys')", 2)
		run(allowed)
		run(fixture.Prepared(allowed))
		run(fixture.Exec("RESET ROLE", 0))
	})
}
