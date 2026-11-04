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

// REL_18_6 pg_buffercache_pages and pg_check_visible/frozen collect on the
// first actual VPC invocation, then return one row or DONE per invocation.
// The ordinary phase is a real inactive PostgreSQL oracle; the active phase
// independently specifies the native simulation, not a SELinux AVC-count oracle.
func TestReferenceSepgsqlSimulationIncomingVPC(t *testing.T) {
	const buffer8 = "pg_buffercache_pages() AS b(bufferid integer,node oid,space oid,db oid,fork smallint,block bigint,dirty boolean,usage smallint)"
	const buffer9 = "pg_buffercache_pages() AS b(bufferid integer,node oid,space oid,db oid,fork smallint,block bigint,dirty boolean,usage smallint,pins integer)"
	const pageQuery = "SELECT blkno,all_visible,all_frozen,pd_all_visible FROM pg_visibility('incoming_heap') ORDER BY blkno"
	signatures := []string{"pg_buffercache_pages()", "pg_check_visible(regclass)", "pg_check_frozen(regclass)", "pg_visibility(regclass)", "pg_visibility_map(regclass)", "pg_truncate_visibility_map(regclass)"}
	setup := func(run sepgsqlRun) {
		run(fixture.Exec("CREATE EXTENSION pg_buffercache", 0))
		run(fixture.Exec("CREATE EXTENSION pg_visibility", 0))
		run(fixture.Exec("CREATE TABLE incoming_heap(k integer)", 0))
		run(fixture.Exec("INSERT INTO incoming_heap VALUES(7)", 1))
		run(fixture.Exec("CREATE TABLE incoming_nulls(rel regclass)", 0))
		run(fixture.Exec("INSERT INTO incoming_nulls VALUES(NULL)", 1))
		run(fixture.Exec("VACUUM (FREEZE) incoming_heap", 0))
	}
	capacity := func(t *testing.T, p *fixture.Pair, c *sql.Conn) int64 {
		t.Helper()
		var n int64
		if err := c.QueryRowContext(p.Ctx, "SELECT setting::bigint FROM pg_settings WHERE name='shared_buffers'").Scan(&n); err != nil || n <= 0 {
			t.Fatal("configured shared-buffer capacity", n, err)
		}
		return n
	}
	page := func(visible, frozen, pd bool) fixture.Step {
		return fixture.Select(pageQuery, []string{"blkno", "all_visible", "all_frozen", "pd_all_visible"}, []string{"INT8", "BOOL", "BOOL", "BOOL"}, [][]any{{int64(0), visible, frozen, pd}})
	}
	check := func(name, arg string) fixture.Step {
		return fixture.Select("SELECT * FROM "+name+"("+arg+")", []string{"t_ctid"}, []string{"TID"}, nil)
	}
	void := func(query string, value any) fixture.Step {
		return fixture.Select(query, []string{"result"}, []string{"VOID"}, [][]any{{value}})
	}
	failure := func(query, code, message string) fixture.Step {
		step := fixture.ImplicitError(query, fixture.Diagnostic{Code: code, Message: message})
		step.SelectRows, step.ErrorStage = true, "execute"
		return step
	}
	badShape := failure("SELECT * FROM pg_buffercache_pages() AS b(id integer)", "XX000", "incorrect number of output arguments")
	badType := failure("SELECT * FROM pg_buffercache_pages() AS b(id bigint,node oid,space oid,db oid,fork smallint,block bigint,dirty boolean,usage smallint)", "42804", "function return row and query-specified return row do not match")
	badType.Diagnostic.Detail = "Returned type integer at ordinal position 1, but query expects bigint."
	nullSteps := []struct {
		name string
		step fixture.Step
	}{
		{"pg_check_visible", check("pg_check_visible", "(SELECT rel FROM incoming_nulls)")},
		{"pg_check_frozen", check("pg_check_frozen", "(SELECT rel FROM incoming_nulls)")},
		{"pg_visibility", fixture.Select("SELECT * FROM pg_visibility((SELECT rel FROM incoming_nulls))", []string{"blkno", "all_visible", "all_frozen", "pd_all_visible"}, []string{"INT8", "BOOL", "BOOL", "BOOL"}, nil)},
		{"pg_visibility_map", fixture.Select("SELECT * FROM pg_visibility_map((SELECT rel FROM incoming_nulls))", []string{"blkno", "all_visible", "all_frozen"}, []string{"INT8", "BOOL", "BOOL"}, nil)},
		{"pg_truncate_visibility_map", void("SELECT pg_truncate_visibility_map((SELECT rel FROM incoming_nulls)) AS result", nil)},
		{"pg_truncate_visibility_map", void("SELECT result FROM pg_truncate_visibility_map((SELECT rel FROM incoming_nulls)) AS t(result)", nil)},
	}
	errors := []struct {
		name string
		step fixture.Step
	}{
		{"pg_buffercache_pages", badShape},
		{"pg_buffercache_pages", badType},
		{"pg_check_visible", failure("SELECT * FROM pg_check_visible(0::regclass)", "XX000", "could not open relation with OID 0")},
		{"pg_check_frozen", failure("SELECT * FROM pg_check_frozen(0::regclass)", "XX000", "could not open relation with OID 0")},
		{"pg_visibility", failure("SELECT * FROM pg_visibility(0::regclass)", "XX000", "could not open relation with OID 0")},
		{"pg_visibility_map", failure("SELECT * FROM pg_visibility_map(0::regclass)", "XX000", "could not open relation with OID 0")},
		{"pg_truncate_visibility_map", failure("SELECT pg_truncate_visibility_map(0::regclass) AS result", "XX000", "could not open relation with OID 0")},
		{"pg_truncate_visibility_map", failure("SELECT result FROM pg_truncate_visibility_map(0::regclass) AS t(result)", "XX000", "could not open relation with OID 0")},
	}
	var mu sync.Mutex
	var events []stuntgres.SepgsqlAuditEvent
	sepgsqlCase(t, func(t *testing.T, p *fixture.Pair, backend fixture.Backend, c *sql.Conn, run sepgsqlRun) {
		setup(run)
		n := capacity(t, p, c)
		for _, prepared := range []bool{false, true} {
			both := func(step fixture.Step) {
				if prepared {
					step = fixture.Prepared(step)
				}
				run(step)
			}
			for _, from := range []string{buffer8, buffer9} {
				both(sepgsqlCount("SELECT count(*) AS count FROM "+from, n))
			}
			for _, name := range []string{"pg_check_visible", "pg_check_frozen"} {
				both(check(name, "'incoming_heap'"))
			}
			run(fixture.Exec("VACUUM (FREEZE) incoming_heap", 0))
			both(page(true, true, true))
			both(fixture.Select("SELECT * FROM pg_visibility_map('incoming_heap')", []string{"blkno", "all_visible", "all_frozen"}, []string{"INT8", "BOOL", "BOOL"}, [][]any{{int64(0), true, true}}))
			for _, tc := range nullSteps {
				both(tc.step)
			}
			both(page(true, true, true)) // NULL truncate must have no effect.
			for _, query := range []string{"SELECT pg_truncate_visibility_map('incoming_heap') AS result", "SELECT result FROM pg_truncate_visibility_map('incoming_heap') AS t(result)"} {
				run(fixture.Exec("VACUUM (FREEZE) incoming_heap", 0))
				both(void(query, ""))
				both(page(false, false, true)) // VM and physical PD are independent.
				both(sepgsqlCount("SELECT count(*) AS count FROM incoming_heap", 1))
			}
			for _, tc := range errors {
				both(tc.step)
			}
			both(check("pg_check_frozen", "'incoming_heap'"))
		}
	}, func(config *stuntgres.SepgsqlSimulationConfig) {
		config.AuditAllowed = true
		config.Audit = func(event stuntgres.SepgsqlAuditEvent) { mu.Lock(); events = append(events, event); mu.Unlock() }
		config.Policy.Rules = append(config.Policy.Rules, stuntgres.SepgsqlRule{Source: sepgsqlR, Target: sepgsqlU, Class: "db_procedure", Allow: []string{"execute"}})
		config.Policy.TrustedTransitions = append(config.Policy.TrustedTransitions, stuntgres.SepgsqlTrustedTransition{Source: sepgsqlR, Procedure: sepgsqlU, Result: sepgsqlT})
	}, func(t *testing.T, p *fixture.Pair, backend fixture.Backend, c *sql.Conn, run sepgsqlRun) {
		setup(run)
		n := capacity(t, p, c)
		label := func(target string) {
			run(sepgsqlBool("SELECT sepgsql_setcon(NULL) AS changed", "changed", true))
			for _, signature := range signatures {
				run(fixture.Exec("SECURITY LABEL ON FUNCTION "+signature+" IS '"+target+"'", 0))
			}
			run(sepgsqlSet(sepgsqlR))
		}
		label(sepgsqlS)
		expect := func(step fixture.Step, name, target string, allow, deny int64, prepared bool) {
			t.Helper()
			mu.Lock()
			events = nil
			mu.Unlock()
			if prepared {
				step = fixture.Prepared(step)
			}
			run(step)
			mu.Lock()
			observed := slices.Clone(events)
			mu.Unlock()
			var gotAllow, gotDeny int64
			for _, event := range observed {
				if event.Subject != sepgsqlR || event.Target != target || event.Class != "db_procedure" || !strings.Contains(event.Name, name) || !slices.Contains(event.Required, "entrypoint") {
					continue
				}
				if slices.Contains(event.Denied, "entrypoint") {
					gotDeny++
				} else if len(event.Denied) == 0 {
					gotAllow++
				}
			}
			if gotAllow != allow || gotDeny != deny {
				t.Fatalf("%s prepared=%v actual body allow/deny got %d/%d want %d/%d: %#v", name, prepared, gotAllow, gotDeny, allow, deny, observed)
			}
			run(sepgsqlText("SELECT sepgsql_getcon() AS context", "context", sepgsqlR))
		}
		freeze := func() {
			run(sepgsqlBool("SELECT sepgsql_setcon(NULL) AS changed", "changed", true))
			run(fixture.Exec("VACUUM (FREEZE) incoming_heap", 0))
			run(sepgsqlSet(sepgsqlR))
		}
		for _, prepared := range []bool{false, true} {
			for _, from := range []string{buffer8, buffer9} {
				expect(sepgsqlCount("SELECT count(*) AS count FROM "+from, n), "pg_buffercache_pages", sepgsqlS, n+1, 0, prepared)
			}
			for _, name := range []string{"pg_check_visible", "pg_check_frozen"} {
				expect(check(name, "'incoming_heap'"), name, sepgsqlS, 1, 0, prepared)
			}
			freeze()
			expect(page(true, true, true), "pg_visibility", sepgsqlS, 2, 0, prepared)
			expect(fixture.Select("SELECT * FROM pg_visibility_map('incoming_heap')", []string{"blkno", "all_visible", "all_frozen"}, []string{"INT8", "BOOL", "BOOL"}, [][]any{{int64(0), true, true}}), "pg_visibility_map", sepgsqlS, 2, 0, prepared)
			for _, tc := range nullSteps {
				expect(tc.step, tc.name, sepgsqlS, 0, 0, prepared)
			}
			expect(page(true, true, true), "pg_visibility", sepgsqlS, 2, 0, prepared)
			for _, query := range []string{"SELECT pg_truncate_visibility_map('incoming_heap') AS result", "SELECT result FROM pg_truncate_visibility_map('incoming_heap') AS t(result)"} {
				freeze()
				expect(void(query, ""), "pg_truncate_visibility_map", sepgsqlS, 1, 0, prepared)
				expect(page(false, false, true), "pg_visibility", sepgsqlS, 2, 0, prepared)
				run(sepgsqlCount("SELECT count(*) AS count FROM incoming_heap", 1))
			}
			for _, tc := range errors {
				expect(tc.step, tc.name, sepgsqlS, 1, 0, prepared)
			}
			expect(check("pg_check_frozen", "'incoming_heap'"), "pg_check_frozen", sepgsqlS, 1, 0, prepared)
		}
		label(sepgsqlU) // EXECUTE is granted, but genuine entrypoint is denied.
		for _, prepared := range []bool{false, true} {
			for _, tc := range errors {
				step := sepgsqlDenied(tc.step.Query)
				step.SelectRows, step.ErrorStage = true, "execute"
				expect(step, tc.name, sepgsqlU, 0, 1, prepared)
			}
			for _, tc := range nullSteps {
				expect(tc.step, tc.name, sepgsqlU, 0, 0, prepared)
			}
		}
		label(sepgsqlS)
		freeze()
		expect(page(true, true, true), "pg_visibility", sepgsqlS, 2, 0, false)
		expect(page(true, true, true), "pg_visibility", sepgsqlS, 2, 0, true)
	})
}
