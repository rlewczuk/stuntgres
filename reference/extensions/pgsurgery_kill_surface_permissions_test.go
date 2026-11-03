package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePgsurgeryKillPermissions(t *testing.T) {
	t.Run("owner_grants_held_revoke_membership", func(t *testing.T) {
		fixture.RoleReferenceCleanup(t, []string{"ks_owner", "ks_reader", "ks_member"})
		cleanAM := fixture.AccessMethodReferenceCleanup(t, []string{"ks_owner_nonheap"})
		ksCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			if b.PG {
				cleanAM()
			}
			defer c.ExecContext(p.Ctx, "DROP ACCESS METHOD IF EXISTS ks_owner_nonheap CASCADE")
			for _, q := range []string{
				"CREATE ROLE ks_owner", "CREATE ROLE ks_reader", "CREATE ROLE ks_member",
				"GRANT USAGE ON SCHEMA ks_part TO ks_owner,ks_reader,ks_member",
				"ALTER TABLE ks_part.heap OWNER TO ks_owner", "GRANT SELECT,DELETE ON ks_part.heap TO ks_reader",
				"CREATE TABLE ks_part.inputs(n regclass)", "GRANT SELECT ON ks_part.inputs TO ks_owner,ks_reader,ks_member",
				"CREATE VIEW ks_part.v AS SELECT k FROM ks_part.heap",
				"CREATE ACCESS METHOD ks_owner_nonheap TYPE TABLE HANDLER heap_tableam_handler",
				"CREATE TABLE ks_part.nonheap(k integer)",
				"ALTER TABLE ks_part.nonheap SET ACCESS METHOD ks_owner_nonheap",
			} {
				freezeRun(t, p, b, c, fixture.Exec(q, 0))
			}
			freezeRun(t, p, b, c, fixture.Exec("INSERT INTO ks_part.inputs VALUES(NULL)", 1))
			a := freezeOne(t, p, c, "INSERT INTO ks_part.heap VALUES(1,'protected') RETURNING k,v,ctid,xmin,xmax")
			q := "SELECT ks_part.heap_force_kill(COALESCE($1::regclass,i.n),$2::tid[]) AS killed FROM ks_part.inputs i"
			st, err := c.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer st.Close()
			held := fixture.HeldStatement{Executor: c, Statement: st, SQL: q}
			defer c.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION")
			denied := func(target any, tids, message string) {
				t.Helper()
				s := ksError(q, "42501", message, target, tids)
				s.ErrorStage = "execute"
				freezeBoth(t, p, b, c, s)
				fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
			}
			for _, role := range []string{"ks_owner", "ks_reader"} {
				freezeRun(t, p, b, c, fixture.Exec("SET SESSION AUTHORIZATION "+role, 0))
				denied(nil, "{NULL}", "permission denied for function heap_force_kill")
				denied("ks_part.heap", freezeTIDs(a.tid), "permission denied for function heap_force_kill")
				freezeRun(t, p, b, c, fixture.Exec("RESET SESSION AUTHORIZATION", 0))
			}
			freezeRun(t, p, b, c, fixture.Exec("GRANT EXECUTE ON FUNCTION ks_part.heap_force_kill(regclass,tid[]) TO ks_reader", 0))
			freezeRun(t, p, b, c, fixture.Exec("SET SESSION AUTHORIZATION ks_reader", 0))
			denied("ks_part.heap", "{}", "must be owner of table heap")
			denied("ks_part.heap", freezeTIDs(a.tid), "must be owner of table heap")
			freezeBoth(t, p, b, c, fixture.Select(q, []string{"killed"}, []string{"VOID"}, [][]any{{nil}}, nil, "{NULL}"))
			// Kind admission precedes ownership, even for an empty selection.
			s := ksError(q, "42809", "cannot operate on relation \"v\"", "ks_part.v", "{}")
			s.Diagnostic.Detail = "This operation is not supported for views."
			freezeBoth(t, p, b, c, s)
			freezeBoth(t, p, b, c, ksError(q, "0A000", "only heap AM is supported", "ks_part.nonheap", "{}"))
			freezeBoth(t, p, b, c, ksError("SELECT ks_part.heap_force_freeze($1::regclass,$2::tid[]) AS killed", "42501", "permission denied for function heap_force_freeze", "ks_part.heap", "{}"))
			freezeRun(t, p, b, c, fixture.Exec("RESET SESSION AUTHORIZATION", 0))
			ksRows(t, p, b, c, a)
			freezeRun(t, p, b, c, fixture.Exec("GRANT EXECUTE ON FUNCTION ks_part.heap_force_kill(regclass,tid[]) TO ks_owner,ks_member", 0))
			freezeRun(t, p, b, c, fixture.Exec("SET SESSION AUTHORIZATION ks_owner", 0))
			freezeBoth(t, p, b, c, ksVoid(q, "ks_part.heap", "{}"))
			fixture.RunStep(t, p.Ctx, held, b.Recorder, ksVoid(q, "ks_part.heap", "{}"))
			freezeRun(t, p, b, c, fixture.Exec("RESET SESSION AUTHORIZATION", 0))
			freezeRun(t, p, b, c, fixture.Exec("REVOKE EXECUTE ON FUNCTION ks_part.heap_force_kill(regclass,tid[]) FROM ks_owner", 0))
			freezeRun(t, p, b, c, fixture.Exec("SET SESSION AUTHORIZATION ks_owner", 0))
			denied(nil, "{}", "permission denied for function heap_force_kill")
			denied("ks_part.heap", "{}", "permission denied for function heap_force_kill")
			freezeRun(t, p, b, c, fixture.Exec("RESET SESSION AUTHORIZATION", 0))
			freezeRun(t, p, b, c, fixture.Exec("GRANT ks_owner TO ks_member", 0))
			freezeRun(t, p, b, c, fixture.Exec("SET SESSION AUTHORIZATION ks_member", 0))
			freezeBoth(t, p, b, c, ksVoid(q, "ks_part.heap", "{}"))
			fixture.RunStep(t, p.Ctx, held, b.Recorder, ksVoid(q, "ks_part.heap", "{}"))
			freezeRun(t, p, b, c, fixture.Exec("RESET SESSION AUTHORIZATION", 0))
			freezeRun(t, p, b, c, fixture.Exec("REVOKE ks_owner FROM ks_member", 0))
			freezeRun(t, p, b, c, fixture.Exec("SET SESSION AUTHORIZATION ks_member", 0))
			denied("ks_part.heap", "{}", "must be owner of table heap")
			freezeRun(t, p, b, c, fixture.Exec("RESET SESSION AUTHORIZATION", 0))
			ksRows(t, p, b, c, a)
			// With only its own grant, the actual owner may kill a real version.
			freezeRun(t, p, b, c, fixture.Exec("GRANT EXECUTE ON FUNCTION ks_part.heap_force_kill(regclass,tid[]) TO ks_owner", 0))
			freezeRun(t, p, b, c, fixture.Exec("SET SESSION AUTHORIZATION ks_owner", 0))
			fixture.RunStep(t, p.Ctx, held, b.Recorder, ksVoid(q, "ks_part.heap", freezeTIDs(a.tid)))
			freezeRun(t, p, b, c, fixture.Exec("RESET SESSION AUTHORIZATION", 0))
			ksRows(t, p, b, c)
		})
	})
	t.Run("authority_before_children_schema_install", func(t *testing.T) {
		fixture.RoleReferenceCleanup(t, []string{"ks_actor"})
		ksCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			for _, q := range []string{
				"CREATE ROLE ks_actor", "GRANT USAGE ON SCHEMA ks_part TO ks_actor",
				"CREATE TABLE ks_part.divisor(d integer)", "GRANT SELECT ON ks_part.divisor TO ks_actor",
			} {
				freezeRun(t, p, b, c, fixture.Exec(q, 0))
			}
			freezeRun(t, p, b, c, fixture.Exec("INSERT INTO ks_part.divisor VALUES(0)", 1))
			a := freezeOne(t, p, c, "INSERT INTO ks_part.heap VALUES(1,'protected') RETURNING k,v,ctid,xmin,xmax")
			q := "SELECT ks_part.heap_force_kill('ks_part.heap'::regclass,ARRAY[('(0,'||(1/d)::text||')')::tid]) AS killed FROM ks_part.divisor"
			st, err := c.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer st.Close()
			held := fixture.HeldStatement{Executor: c, Statement: st, SQL: q}
			defer c.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION")
			freezeRun(t, p, b, c, fixture.Exec("SET SESSION AUTHORIZATION ks_actor", 0))
			s := ksError(q, "42501", "permission denied for function heap_force_kill")
			freezeBoth(t, p, b, c, s)
			fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
			freezeRun(t, p, b, c, fixture.Exec("RESET SESSION AUTHORIZATION", 0))
			freezeRun(t, p, b, c, fixture.Exec("GRANT EXECUTE ON FUNCTION ks_part.heap_force_kill(regclass,tid[]) TO ks_actor", 0))
			freezeRun(t, p, b, c, fixture.Exec("SET SESSION AUTHORIZATION ks_actor", 0))
			s = ksError(q, "22012", "division by zero")
			freezeBoth(t, p, b, c, s)
			fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
			freezeRun(t, p, b, c, fixture.Exec("RESET SESSION AUTHORIZATION", 0))
			ksRows(t, p, b, c, a)
			freezeRun(t, p, b, c, fixture.Exec("REVOKE USAGE ON SCHEMA ks_part FROM ks_actor", 0))
			freezeRun(t, p, b, c, fixture.Exec("SET SESSION AUTHORIZATION ks_actor", 0))
			s = ksError(killSQL, "42501", "permission denied for schema ks_part", nil, nil)
			s.Diagnostic.Position = "8"
			ksAnalysisError(t, p, b, c, s)
			freezeRun(t, p, b, c, fixture.Exec("RESET SESSION AUTHORIZATION", 0))
			freezeRun(t, p, b, c, fixture.Exec("DROP EXTENSION pg_surgery", 0))
			freezeRun(t, p, b, c, fixture.Exec("GRANT CREATE ON SCHEMA ks_part TO ks_actor", 0))
			freezeRun(t, p, b, c, fixture.Exec("SET SESSION AUTHORIZATION ks_actor", 0))
			s = ksError("CREATE EXTENSION pg_surgery SCHEMA ks_part", "42501", "permission denied to create extension \"pg_surgery\"")
			s.SelectRows = false
			s.Diagnostic.Hint = "Must be superuser to create this extension."
			freezeBoth(t, p, b, c, s)
			freezeRun(t, p, b, c, fixture.Exec("RESET SESSION AUTHORIZATION", 0))
			ksNoInstalledMembers(t, p, b, c)
			ksRows(t, p, b, c, a)
		})
	})
}
