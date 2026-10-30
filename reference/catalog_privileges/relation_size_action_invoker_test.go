package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceRelationSizeActionInvoker(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"rs_action_owner", "rs_action_caller"})
	databases := []string{"rs_action_target"}
	fixture.DatabaseReferenceCleanup(t, &databases)
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			conn, err := b.DB.Conn(p.Ctx)
			if err != nil { t.Fatal(err) }
			defer conn.Close()
			cleanup := func(role string) {
				for _, q := range []string{"RESET SESSION AUTHORIZATION", "DROP OWNED BY " + role + " CASCADE"} {
					if _, err := conn.ExecContext(p.Ctx, q); err != nil { t.Error("size action ownership/ACL cleanup", q, err) }
				}
			}
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, b.Recorder, s) }
			run(fixture.Exec("CREATE ROLE rs_action_owner", 0))
			defer cleanup("rs_action_owner")
			run(fixture.Exec("CREATE ROLE rs_action_caller", 0))
			defer cleanup("rs_action_caller")
			relationSizeRestoreTablespaceACL(t, p, b, []string{"rs_action_owner", "rs_action_caller"})
			for _, q := range []string{
				"GRANT USAGE ON SCHEMA public TO rs_action_owner,rs_action_caller",
				"CREATE DATABASE rs_action_target",
				"REVOKE CONNECT ON DATABASE rs_action_target FROM PUBLIC",
				"GRANT CONNECT ON DATABASE rs_action_target TO rs_action_owner",
				"GRANT CREATE ON TABLESPACE pg_global TO rs_action_owner",
				"CREATE TABLE public.rs_action_db(i INT)",
				"CREATE TABLE public.rs_action_ts(i INT)",
				"CREATE TABLE public.rs_action_audit(kind TEXT,n BIGINT)",
				"ALTER TABLE public.rs_action_db OWNER TO rs_action_owner",
				"ALTER TABLE public.rs_action_ts OWNER TO rs_action_owner",
				"ALTER TABLE public.rs_action_audit OWNER TO rs_action_owner",
				"GRANT INSERT ON public.rs_action_db,public.rs_action_ts TO rs_action_caller",
				"CREATE RULE rs_capture_db AS ON INSERT TO public.rs_action_db DO ALSO INSERT INTO public.rs_action_audit VALUES('db',pg_database_size('rs_action_target'))",
				"CREATE RULE rs_capture_ts AS ON INSERT TO public.rs_action_ts DO ALSO INSERT INTO public.rs_action_audit VALUES('ts',pg_tablespace_size('pg_global'))",
			} { run(fixture.Exec(q, 0)) }

			for _, target := range []struct{table, object, label, grant, revoke string}{
				{"rs_action_db", "rs_action_target", "database", "GRANT CONNECT ON DATABASE rs_action_target TO rs_action_caller", "REVOKE CONNECT ON DATABASE rs_action_target FROM rs_action_caller"},
				{"rs_action_ts", "pg_global", "tablespace", "GRANT CREATE ON TABLESPACE pg_global TO rs_action_caller", "REVOKE CREATE ON TABLESPACE pg_global FROM rs_action_caller"},
			} {
				q := "INSERT INTO public." + target.table + " VALUES($1::integer)"
				stmt, err := conn.PrepareContext(p.Ctx, q)
				if err != nil { t.Fatal(err) }
				defer stmt.Close()
				held := fixture.HeldStatement{Executor: conn, Statement: stmt, SQL: q}
				denied := func() {
					t.Helper()
					for _, executor := range []fixture.Executor{conn, held} {
						s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "42501", Message: "permission denied for " + target.label + " " + target.object}, int64(1))
						s.ErrorStage = "execute"
						fixture.RunStep(t, p.Ctx, executor, b.Recorder, s)
					}
				}
				run(fixture.Exec("SET SESSION AUTHORIZATION rs_action_caller", 0))
				denied() // The rule owner has rights, the function invoker does not.
				run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
				for _, table := range []string{target.table, "rs_action_audit"} {
					run(relationSizeSelect("SELECT count(*) AS v FROM public."+table, [][]any{{int64(0)}}))
				}
				run(fixture.Exec(target.grant, 0))
				run(fixture.Exec("SET SESSION AUTHORIZATION rs_action_caller", 0))
				for _, executor := range []fixture.Executor{conn, held} {
					fixture.RunStep(t, p.Ctx, executor, b.Recorder, fixture.Exec(q, 1, int64(2)))
				}
				run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
				run(fixture.Exec(target.revoke, 0))
				run(fixture.Exec("SET SESSION AUTHORIZATION rs_action_caller", 0))
				denied() // Held execution must not retain the earlier grant.
				run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
				run(relationSizeSelect("SELECT count(*) AS v FROM public."+target.table, [][]any{{int64(2)}}))
				run(fixture.Exec(target.grant, 0))
				ownerRevoke := "REVOKE CONNECT ON DATABASE rs_action_target FROM rs_action_owner"
				if target.label == "tablespace" { ownerRevoke = "REVOKE CREATE ON TABLESPACE pg_global FROM rs_action_owner" }
				run(fixture.Exec(ownerRevoke, 0))
				run(fixture.Exec("SET SESSION AUTHORIZATION rs_action_caller", 0))
				fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Exec(q, 1, int64(3)))
				run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
				run(relationSizeSelect("SELECT count(*) AS v FROM public."+target.table, [][]any{{int64(3)}}))
				kind := "db"
				if target.label == "tablespace" { kind = "ts" }
				audit := fixture.Select("SELECT kind,n>=0 AS valid FROM public.rs_action_audit", []string{"kind", "valid"}, []string{"TEXT", "BOOL"}, [][]any{{kind, true}, {kind, true}, {kind, true}})
				audit.Bag = true
				run(audit)
				// Keep each family's atomicity witness independent of earlier
				// successful action rows in the shared audit table.
				run(fixture.Exec("DELETE FROM public.rs_action_audit", 3))
			}
		})
	}
}
