package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"stuntgres/reference/fixture"
)

func clusterEnvironmentSettings(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, user, session string) {
	t.Helper()
	persistenceRun(t, p, b, c, fixture.Select("SELECT current_user AS actor,session_user AS authenticated,current_setting('application_name') AS app,current_setting('search_path') AS path", []string{"actor", "authenticated", "app", "path"}, []string{"NAME", "NAME", "TEXT", "TEXT"}, [][]any{{user, session, "cluster caller", "cluster_env_shadow, public, pg_catalog"}}))
}

func clusterEnvironmentKey(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, body string) {
	t.Helper()
	persistenceExec(t, p, b, c, "CREATE OR REPLACE FUNCTION public.cluster_env_key(integer) RETURNS integer LANGUAGE SQL IMMUTABLE AS $$"+body+"$$")
}

func clusterEnvironmentMetadata(clustered bool) fixture.Step {
	return fixture.Select("SELECT c.relname,i.indisclustered,i.indexprs IS NOT NULL AS has_expression,i.indisvalid,i.indisready,i.indislive,i.indnkeyatts,i.indnatts FROM pg_catalog.pg_index i JOIN pg_catalog.pg_class c ON c.oid=i.indexrelid WHERE c.relname='env_idx'", []string{"relname", "indisclustered", "has_expression", "indisvalid", "indisready", "indislive", "indnkeyatts", "indnatts"}, []string{"NAME", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL", "INT2", "INT2"}, [][]any{{"env_idx", clustered, true, true, true, true, int64(1), int64(1)}})
}

func TestReferenceClusterMaintenanceEnvironment(t *testing.T) {
	for _, kind := range []string{"owner_private", "safe_path", "settings", "restricted", "failure"} {
		for _, selected := range []bool{false, true} {
			t.Run(kind+map[bool]string{false: "/sibling", true: "/selected"}[selected], func(t *testing.T) {
				fixture.RoleReferenceCleanup(t, []string{"cluster_env_owner", "cluster_env_caller"})
				fixture.SchemaReferenceCleanup(t, "cluster_env_private")
				fixture.SchemaReferenceCleanup(t, "cluster_env_shadow")
				persistenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
					var authenticated string
					if err := c.QueryRowContext(p.Ctx, "SELECT session_user").Scan(&authenticated); err != nil {
						t.Fatal(err)
					}
					for _, q := range []string{
						"CREATE ROLE cluster_env_owner", "CREATE ROLE cluster_env_caller",
						"GRANT USAGE,CREATE ON SCHEMA public TO cluster_env_owner", "GRANT USAGE ON SCHEMA public TO cluster_env_caller",
						"CREATE SCHEMA cluster_env_private AUTHORIZATION cluster_env_owner", "CREATE SCHEMA cluster_env_shadow",
						"CREATE FUNCTION cluster_env_shadow.abs(integer) RETURNS integer LANGUAGE SQL IMMUTABLE AS $$SELECT 999$$",
						"GRANT USAGE ON SCHEMA cluster_env_shadow TO cluster_env_caller",
						"SET ROLE cluster_env_owner", "CREATE TABLE cluster_env_private.secret(token int)",
					} {
						persistenceExec(t, p, b, c, q)
					}
					persistenceRun(t, p, b, c, fixture.Exec("INSERT INTO cluster_env_private.secret VALUES(1)", 1))
					clusterCommandSeed(t, p, b, c)
					clusterEnvironmentKey(t, p, b, c, "SELECT 0; SELECT $1")
					persistenceExec(t, p, b, c, "CREATE INDEX env_idx ON t(public.cluster_env_key(k))")
					body := "SELECT pg_catalog.set_config('application_name','cluster local',false); SELECT $1"
					switch kind {
					case "owner_private":
						body = "SELECT 0; SELECT $1 FROM cluster_env_private.secret WHERE token=1"
					case "safe_path":
						body = "SELECT 0; SELECT $1 / CASE WHEN abs($1)=pg_catalog.abs($1) AND pg_catalog.current_setting('search_path')='pg_catalog, pg_temp' THEN 1 ELSE 0 END"
					case "restricted":
						body = "SELECT pg_catalog.set_config('application_name','cluster local',false); SELECT pg_catalog.set_config('role','none',false); SELECT $1"
					case "failure":
						body = "SELECT pg_catalog.set_config('application_name','cluster local',false); SELECT $1 / CASE WHEN pg_catalog.current_setting('application_name')='cluster local' THEN 0 ELSE 1 END"
					}
					clusterEnvironmentKey(t, p, b, c, body)
					for _, q := range []string{"RESET ROLE", "GRANT MAINTAIN ON t TO cluster_env_caller", "SET ROLE cluster_env_caller", "SET application_name='cluster caller'", "SET search_path=cluster_env_shadow,public,pg_catalog"} {
						persistenceExec(t, p, b, c, q)
					}
					defer c.ExecContext(p.Ctx, "RESET ROLE")
					defer c.ExecContext(p.Ctx, "RESET search_path")
					defer c.ExecContext(p.Ctx, "RESET application_name")
					clusterEnvironmentSettings(t, p, b, c, "cluster_env_caller", authenticated)
					persistenceRun(t, p, b, c, clusterEnvironmentMetadata(false))
					persistenceRun(t, p, b, c, fixture.Prepared(clusterEnvironmentMetadata(false)))
					q := "CLUSTER public.t USING a"
					if selected {
						q = "CLUSTER public.t USING env_idx"
					}
					held := clusterCommandHeld(t, p, b, c, q)
					for _, route := range []string{"direct", "fresh", "held"} {
						step := fixture.Exec(q, 0)
						if kind == "restricted" || kind == "failure" {
							// PG18.6 reports these complete failures from the second
							// command of this non-inlined SQL function.
							d := persistenceDiagnostic("42501", `cannot set parameter "role" within security-restricted operation`)
							if kind == "failure" {
								d = persistenceDiagnostic("22012", "division by zero")
							}
							d.Context = `SQL function "cluster_env_key" statement 2`
							step = fixture.ErrorDiagnostic(q, d)
							step.ErrorStage = "execute"
						}
						var executor fixture.Executor = c
						if route == "fresh" {
							step = fixture.Prepared(step)
						}
						if route == "held" {
							executor = fixture.HeldStatement{Executor: c, Statement: held, SQL: q}
						}
						persistenceRun(t, p, b, executor, step)
						clusterEnvironmentSettings(t, p, b, c, "cluster_env_caller", authenticated)
						persistenceRun(t, p, b, c, clusterEnvironmentMetadata(selected && kind != "restricted" && kind != "failure"))
					}
					persistenceExec(t, p, b, c, "RESET ROLE")
					persistenceRun(t, p, b, c, clusterCommandRows())
					flags := clusterState("t", [][]any{{"a", kind != "restricted" && kind != "failure" && !selected}, {"b", false}, {"env_idx", kind != "restricted" && kind != "failure" && selected}, {"u", false}})
					persistenceRun(t, p, b, c, flags)
					if !b.PG && kind == "owner_private" && selected {
						// Restore need not depend on the execution environment of the
						// separate CREATE INDEX utility: retain an equivalent pure key.
						clusterEnvironmentKey(t, p, b, c, "SELECT 0; SELECT $1")
						persistenceExec(t, p, b, c, "CLUSTER public.t")
						clusterRemedyNativeRoundtrip(t, p, b, c, clusterCommandRows().Rows, flags)
					}
				})
			})
		}
	}
	t.Run("cancellation_restores_environment", clusterEnvironmentCancellation)
}

func clusterEnvironmentCancellation(t *testing.T) {
	for _, held := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "held"}[held], func(t *testing.T) {
			fixture.SchemaReferenceCleanup(t, "cluster_env_shadow")
			persistenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				clusterCommandSeed(t, p, b, c)
				for _, q := range []string{"CREATE SCHEMA cluster_env_shadow", "CREATE TABLE public.cluster_env_gate(token int)"} {
					persistenceExec(t, p, b, c, q)
				}
				persistenceRun(t, p, b, c, fixture.Exec("INSERT INTO public.cluster_env_gate VALUES(1)", 1))
				clusterEnvironmentKey(t, p, b, c, "SELECT 0; SELECT $1")
				persistenceExec(t, p, b, c, "CREATE INDEX env_idx ON t(public.cluster_env_key(k))")
				clusterEnvironmentKey(t, p, b, c, "SELECT pg_catalog.set_config('application_name','cluster local',false); SELECT $1 FROM public.cluster_env_gate WHERE token=1")
				var user, authenticated string
				if err := c.QueryRowContext(p.Ctx, "SELECT current_user,session_user").Scan(&user, &authenticated); err != nil {
					t.Fatal(err)
				}
				for _, q := range []string{"SET application_name='cluster caller'", "SET search_path=cluster_env_shadow,public,pg_catalog"} {
					persistenceExec(t, p, b, c, q)
				}
				defer c.ExecContext(p.Ctx, "RESET application_name")
				defer c.ExecContext(p.Ctx, "RESET search_path")
				owner, err := b.DB.Conn(p.Ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer owner.Close()
				observer, err := b.DB.Conn(p.Ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer observer.Close()
				const q = "CLUSTER public.t USING a"
				var statement *sql.Stmt
				if held {
					statement = clusterCommandHeld(t, p, b, c, q)
				}
				workerPID, ownerPID := 0, 0
				var oid int64
				if b.PG {
					workerPID, ownerPID = fixture.LockPID(t, p.Ctx, c), fixture.LockPID(t, p.Ctx, owner)
					oid = fixture.LockOID(t, p.Ctx, "cluster_env_gate")
				}
				tx, err := owner.BeginTx(p.Ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				defer tx.Rollback()
				persistenceExec(t, p, b, tx, "LOCK TABLE public.cluster_env_gate IN ACCESS EXCLUSIVE MODE")
				ctx, cancel := context.WithCancel(p.Ctx)
				defer cancel()
				finished := make(chan struct{})
				join := fixture.LockOperation(t, ctx, func(ctx context.Context) fixture.RelationOutcome {
					defer close(finished)
					var result sql.Result
					var err error
					if held {
						result, err = statement.ExecContext(ctx)
					} else {
						result, err = c.ExecContext(ctx, q)
					}
					return fixture.RelationOutcome{Result: result, Err: err}
				})
				fixture.RelationLockPending(t, p, b, workerPID, ownerPID, oid, "AccessShareLock", finished)
				if b.PG {
					var ok bool
					if err = observer.QueryRowContext(p.Ctx, "SELECT pg_cancel_backend($1)", workerPID).Scan(&ok); err != nil || !ok {
						t.Fatal(ok, err)
					}
				} else {
					cancel()
				}
				out := join()
				if out.Result != nil || out.Rows != nil {
					t.Fatal("canceled CLUSTER exposed rows/result")
				}
				if b.PG {
					want := persistenceDiagnostic("57014", "canceling statement due to user request")
					want.Context = `SQL function "cluster_env_key" statement 2`
					fixture.AssertDiagnostic(t, out.Err, &want)
				} else if !errors.Is(out.Err, context.Canceled) {
					t.Fatal(out.Err)
				}
				if err = tx.Rollback(); err != nil {
					t.Fatal(err)
				}
				clusterEnvironmentSettings(t, p, b, c, user, authenticated)
				persistenceRun(t, p, b, c, clusterCommandRows())
				persistenceRun(t, p, b, c, clusterCommandProgressIdle())
				persistenceRun(t, p, b, c, clusterState("t", [][]any{{"a", false}, {"b", false}, {"env_idx", false}, {"u", false}}))
				persistenceRun(t, p, b, c, clusterEnvironmentMetadata(false))
			})
		})
	}
}
