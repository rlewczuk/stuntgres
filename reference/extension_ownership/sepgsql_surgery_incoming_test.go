package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

// REL_18_6 heap_surgery.c owns array, relation-kind, method and SQL-owner
// checks inside the entered scalar. It is physical surgery, not executor UPDATE.
func TestReferenceSepgsqlSimulationSurgeryIncoming(t *testing.T) {
	setup := func(run sepgsqlRun) {
		run(fixture.Exec("CREATE EXTENSION pg_surgery", 0))
		sepgsqlTable(run)
		run(fixture.Exec("CREATE VIEW sepgsql_surgery_view AS SELECT id FROM sepgsql_items", 0))
		run(fixture.Exec("CREATE TABLE sepgsql_surgery_nulls(r regclass,a tid[])", 0))
		run(fixture.Exec("INSERT INTO sepgsql_surgery_nulls VALUES(NULL,'{NULL}'),(0,NULL)", 2))
	}
	call := func(method string) string {
		return "SELECT " + method + "($1::text::regclass,$2::tid[]) AS repaired"
	}
	void := func(query string, args ...any) fixture.Step {
		return fixture.Select(query, []string{"repaired"}, []string{"VOID"}, [][]any{{""}}, args...)
	}
	strict := func(run sepgsqlRun, method string, prepared bool) {
		step := fixture.Select("SELECT "+method+"(r,a) AS repaired FROM sepgsql_surgery_nulls", []string{"repaired"}, []string{"VOID"}, [][]any{{nil}, {nil}})
		step.Prepared = prepared
		run(step)
	}
	diagnostics := func(run sepgsqlRun, method string, prepared bool) {
		for _, invalid := range []struct {
			target, tids, code, message, detail string
		}{
			{"sepgsql_items", "{NULL}", "22004", "array must not contain nulls", ""},
			{"sepgsql_items", "{{\"(0,1)\"},{\"(0,2)\"}}", "22000", "argument must be empty or one-dimensional array", ""},
			{"sepgsql_surgery_view", "{}", "42809", "cannot operate on relation \"sepgsql_surgery_view\"", "This operation is not supported for views."},
		} {
			step := fixture.ImplicitError(call(method), fixture.Diagnostic{Code: invalid.code, Message: invalid.message, Detail: invalid.detail}, invalid.target, invalid.tids)
			step.Prepared = prepared
			run(step)
		}
		// A numeric regclass datum reaches the surgery kernel. Text "0" is a
		// relation name and fails earlier in PostgreSQL's regclass input.
		step := fixture.ImplicitError("SELECT "+method+"(0::regclass,$1::tid[]) AS repaired", fixture.Diagnostic{Code: "XX000", Message: "could not open relation with OID 0"}, "{}")
		step.Prepared = prepared
		run(step)
	}
	effects := func(t *testing.T, p *fixture.Pair, c *sql.Conn, run sepgsqlRun, prepared bool) {
		t.Helper()
		// Capture each backend's real physical coordinates, never assume a TID.
		var killed, frozen string
		if err := c.QueryRowContext(p.Ctx, "SELECT ctid::text FROM sepgsql_items WHERE id=1").Scan(&killed); err != nil {
			t.Fatal(err)
		}
		if err := c.QueryRowContext(p.Ctx, "SELECT ctid::text FROM sepgsql_items WHERE id=2").Scan(&frozen); err != nil {
			t.Fatal(err)
		}
		freeze := void(call("heap_force_freeze"), "sepgsql_items", "{\""+frozen+"\"}")
		freeze.Prepared = prepared
		run(freeze)
		run(sepgsqlBool("SELECT xmin::text='2' AS frozen FROM sepgsql_items WHERE id=2", "frozen", true))
		kill := void(call("heap_force_kill"), "sepgsql_items", "{\""+killed+"\"}")
		kill.Prepared = prepared
		run(kill)
		run(fixture.Select("SELECT id,v FROM sepgsql_items ORDER BY id", []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(2), "two"}}))
	}
	for _, prepared := range []bool{false, true} {
		name := "direct"
		if prepared {
			name = "prepared"
		}
		t.Run(name, func(t *testing.T) {
			sepgsqlCase(t, func(t *testing.T, p *fixture.Pair, backend fixture.Backend, c *sql.Conn, run sepgsqlRun) {
				setup(run)
				for _, method := range []string{"heap_force_kill", "heap_force_freeze"} {
					strict(run, method, prepared)
					diagnostics(run, method, prepared)
				}
				effects(t, p, c, run, prepared)
			}, func(config *stuntgres.SepgsqlSimulationConfig) {
				config.Policy.Rules = append(config.Policy.Rules, stuntgres.SepgsqlRule{Source: sepgsqlR, Target: sepgsqlU, Class: "db_procedure", Allow: []string{"execute"}})
				config.Policy.TrustedTransitions = append(config.Policy.TrustedTransitions, stuntgres.SepgsqlTrustedTransition{Source: sepgsqlR, Procedure: sepgsqlU, Result: sepgsqlT})
			}, func(t *testing.T, p *fixture.Pair, backend fixture.Backend, c *sql.Conn, run sepgsqlRun) {
				setup(run)
				for _, method := range []string{"heap_force_kill", "heap_force_freeze"} {
					run(fixture.Exec("SECURITY LABEL ON FUNCTION "+method+"(regclass,tid[]) IS '"+sepgsqlS+"'", 0))
				}
				run(fixture.Exec("CREATE FUNCTION sepgsql_surgery_target() RETURNS regclass LANGUAGE sql VOLATILE AS $$SELECT CASE WHEN sepgsql_getcon()='"+sepgsqlR+"' THEN 'sepgsql_items'::regclass ELSE 0::regclass END$$", 0))
				run(sepgsqlSet(sepgsqlR))
				for _, method := range []string{"heap_force_kill", "heap_force_freeze"} {
					step := void("SELECT " + method + "(sepgsql_surgery_target(),'{}'::tid[]) AS repaired")
					step.Prepared = prepared
					run(step)
					strict(run, method, prepared)
					diagnostics(run, method, prepared)
					run(sepgsqlText("SELECT sepgsql_getcon() AS context", "context", sepgsqlR))
				}
				// Neither R nor T has table UPDATE/DELETE grants in this policy.
				// SQL ownership still holds; the real physical kernels must run.
				effects(t, p, c, run, prepared)
				run(sepgsqlText("SELECT sepgsql_getcon() AS context", "context", sepgsqlR))
				for _, method := range []string{"heap_force_kill", "heap_force_freeze"} {
					held, err := c.PrepareContext(p.Ctx, call(method))
					if err != nil {
						t.Fatal(err)
					}
					run(sepgsqlBool("SELECT sepgsql_setcon(NULL) AS changed", "changed", true))
					run(fixture.Exec("SECURITY LABEL ON FUNCTION "+method+"(regclass,tid[]) IS '"+sepgsqlU+"'", 0))
					run(sepgsqlSet(sepgsqlR))
					strict(run, method, prepared)
					// A denied actual entry must precede errors inside the kernel,
					// and a retained statement must consult current labels.
					step := fixture.ImplicitError(call(method), fixture.Diagnostic{Code: "42501", Message: "SELinux: security policy violation"}, "sepgsql_items", "{NULL}")
					fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: held, SQL: call(method)}, p.MemoryRecord, step)
					if err := held.Close(); err != nil {
						t.Fatal(err)
					}
					step.Prepared = prepared
					run(step)
					run(sepgsqlText("SELECT sepgsql_getcon() AS context", "context", sepgsqlR))
					run(sepgsqlCount("SELECT count(*) FROM sepgsql_items", 1))
				}
			})
		})
	}
}
