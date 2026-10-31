package reference_test

import (
	"context"
	"database/sql"
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func TestReferenceSessionSignalAuthorization(t *testing.T) {
	roles := []string{"ss_owner", "ss_actor", "ss_middle", "ss_leaf", "ss_super"}
	fixture.RoleReferenceCleanup(t, roles)
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			admin, e := b.DB.Conn(p.Ctx)
			if e != nil {
				t.Fatal(e)
			}
			defer admin.Close()
			run := func(c *sql.Conn, s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
			for _, r := range roles {
				run(admin, fixture.Exec("CREATE ROLE "+r+" LOGIN PASSWORD 'role-attributes-fixture'", 0))
			}
			run(admin, fixture.Exec("GRANT ss_leaf TO ss_actor", 0))
			run(admin, fixture.Exec("GRANT ss_leaf TO ss_owner", 0))
			run(admin, fixture.Exec("ALTER ROLE ss_super SUPERUSER", 0))
			run(admin, fixture.Exec("GRANT ss_super TO ss_actor", 0))
			ownerPool := fixture.RoleAttributesPool(t, p, b, "ss_owner")
			actorPool := fixture.RoleAttributesPool(t, p, b, "ss_actor")
			owner, e := ownerPool.Conn(p.Ctx)
			if e != nil {
				t.Fatal(e)
			}
			defer owner.Close()
			actor, e := actorPool.Conn(p.Ctx)
			if e != nil {
				t.Fatal(e)
			}
			defer actor.Close()
			pid := fixture.LockPID(t, p.Ctx, owner)
			run(owner, fixture.Select("SELECT pg_cancel_backend(pg_backend_pid()) WHERE FALSE", []string{"pg_cancel_backend"}, []string{"BOOL"}, nil))
			borrowTarget := func(terminating bool) *sql.Conn {
				t.Helper()
				pool := ownerPool
				if terminating {
					// A zero-timeout signal acknowledges delivery, not socket
					// observation. Never return a poisoned idle socket to ownerPool.
					pool = fixture.RoleAttributesPool(t, p, b, "ss_owner")
					pool.SetMaxIdleConns(0)
				}
				target, err := pool.Conn(p.Ctx)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { target.Close() })
				return target
			}
			for _, fn := range []string{"pg_cancel_backend", "pg_terminate_backend"} {
				message, ordinary, super := "permission denied to cancel query", "Only roles with privileges of the role whose query is being canceled or with privileges of the \"pg_signal_backend\" role may cancel this query.", "Only roles with the SUPERUSER attribute may cancel queries of roles with the SUPERUSER attribute."
				if fn == "pg_terminate_backend" {
					message = "permission denied to terminate process"
					ordinary = "Only roles with privileges of the role whose process is being terminated or with privileges of the \"pg_signal_backend\" role may terminate this process."
					super = "Only roles with the SUPERUSER attribute may terminate processes of roles with the SUPERUSER attribute."
				}
				q := "SELECT " + fn + "($1) AS v"
				stmt, e := actor.PrepareContext(p.Ctx, q)
				if e != nil {
					t.Fatal(e)
				}
				t.Cleanup(func() { stmt.Close() })
				deny := func(target int, detail string) {
					t.Helper()
					s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "42501", Message: message, Detail: detail}, target)
					s.SelectRows = true
					s.ErrorStage = "execute"
					fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: actor, Statement: stmt, SQL: q}, b.Recorder, s)
				}
				deny(pid, ordinary)
				// Membership in a superuser role is not the caller's own SUPERUSER bit.
				deny(fixture.LockPID(t, p.Ctx, admin), super)
				// The target's assumed identity cannot transfer authenticated ownership.
				run(owner, fixture.Exec("SET ROLE ss_leaf", 0))
				deny(pid, ordinary)
				run(owner, fixture.Exec("RESET ROLE", 0))
				same := borrowTarget(fn == "pg_terminate_backend")
				samePID := fixture.LockPID(t, p.Ctx, same)
				run(owner, fixture.Select(q, []string{"v"}, []string{"BOOL"}, [][]any{{true}}, samePID))
				same.Close()
				for _, capability := range []string{"pg_signal_autovacuum_worker", "pg_signal_backend"} {
					run(admin, fixture.Exec("GRANT "+capability+" TO ss_actor", 0))
					if capability == "pg_signal_autovacuum_worker" {
						deny(pid, ordinary)
					}
					superPID := fixture.LockPID(t, p.Ctx, admin)
					deny(superPID, super)
					if capability == "pg_signal_backend" {
						run(actor, fixture.Exec("SET ROLE ss_leaf", 0))
						deny(pid, ordinary)
						run(actor, fixture.Exec("RESET ROLE", 0))
						disposable := borrowTarget(fn == "pg_terminate_backend")
						disposablePID := fixture.LockPID(t, p.Ctx, disposable)
						fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: actor, Statement: stmt, SQL: q}, b.Recorder, fixture.Select(q, []string{"v"}, []string{"BOOL"}, [][]any{{true}}, disposablePID))
						disposable.Close()
					}
					run(admin, fixture.Exec("REVOKE "+capability+" FROM ss_actor", 0))
				}
				run(admin, fixture.Exec("GRANT ss_owner TO ss_actor WITH INHERIT FALSE", 0))
				deny(pid, ordinary)
				run(admin, fixture.Exec("REVOKE ss_owner FROM ss_actor", 0))
				run(admin, fixture.Exec("GRANT ss_owner TO ss_middle", 0))
				run(admin, fixture.Exec("GRANT ss_middle TO ss_actor", 0))
				// Disposable authenticated target for termination; inherited cancellation keeps owner.
				target := owner
				targetPID := pid
				if fn == "pg_terminate_backend" {
					target = borrowTarget(true)
					targetPID = fixture.LockPID(t, p.Ctx, target)
				}
				fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: actor, Statement: stmt, SQL: q}, b.Recorder, fixture.Select(q, []string{"v"}, []string{"BOOL"}, [][]any{{true}}, targetPID))
				if target != owner {
					target.Close()
				}
				run(admin, fixture.Exec("REVOKE ss_middle FROM ss_actor", 0))
				run(admin, fixture.Exec("REVOKE ss_owner FROM ss_middle", 0))
				deny(pid, ordinary)
				run(actor, fixture.Exec("SET ROLE ss_leaf", 0))
				deny(pid, ordinary)
				run(actor, fixture.Exec("RESET ROLE", 0))
				stmt.Close()
				run(owner, fixture.Select("SELECT 42", []string{"?column?"}, []string{"INT4"}, [][]any{{int64(42)}}))
			}
			// Authenticated superuser ownership survives both identity-switch routes.
			super, e := b.DB.Conn(p.Ctx)
			if e != nil {
				t.Fatal(e)
			}
			defer super.Close()
			superPID := fixture.LockPID(t, p.Ctx, super)
			for _, identity := range []string{"SET ROLE ss_owner", "SET SESSION AUTHORIZATION ss_owner"} {
				run(super, fixture.Exec(identity, 0))
				for _, fn := range []string{"pg_cancel_backend", "pg_terminate_backend"} {
					message, detail := "permission denied to cancel query", "Only roles with the SUPERUSER attribute may cancel queries of roles with the SUPERUSER attribute."
					if fn == "pg_terminate_backend" {
						message = "permission denied to terminate process"
						detail = "Only roles with the SUPERUSER attribute may terminate processes of roles with the SUPERUSER attribute."
					}
					s := fixture.ImplicitError("SELECT "+fn+"($1)", fixture.Diagnostic{Code: "42501", Message: message, Detail: detail}, superPID)
					s.SelectRows = true
					run(actor, s)
				}
				run(super, fixture.Exec("RESET SESSION AUTHORIZATION", 0))
				run(super, fixture.Exec("RESET ROLE", 0))
			}
			// Mutating global builtin ACLs must be restored even if an assertion fails.
			defer func() {
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				for _, id := range []string{"pg_cancel_backend(integer)", "pg_terminate_backend(integer,bigint)"} {
					if _, e := admin.ExecContext(ctx, "GRANT EXECUTE ON FUNCTION "+id+" TO PUBLIC"); e != nil {
						t.Error(e)
					}
				}
			}()
			for _, id := range []string{"pg_cancel_backend(integer)", "pg_terminate_backend(integer,bigint)"} {
				fn := id[:len("pg_cancel_backend")]
				if id[3] == 't' {
					fn = "pg_terminate_backend"
				}
				q := fmt.Sprintf("SELECT %s(0) AS v", fn)
				stmt, e := actor.PrepareContext(p.Ctx, q)
				if e != nil {
					t.Fatal(e)
				}
				t.Cleanup(func() { stmt.Close() })
				held := fixture.HeldStatement{Executor: actor, Statement: stmt, SQL: q}
				run(admin, fixture.Exec("REVOKE EXECUTE ON FUNCTION "+id+" FROM PUBLIC", 0))
				s := signalError(q, "42501", "permission denied for function "+fn)
				run(actor, s)
				fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
				run(admin, fixture.Exec("GRANT EXECUTE ON FUNCTION "+id+" TO PUBLIC", 0))
				fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Select(q, []string{"v"}, []string{"BOOL"}, [][]any{{false}}))
				stmt.Close()
				for _, mode := range []string{"force_custom_plan", "force_generic_plan"} {
					run(actor, fixture.Exec("SET plan_cache_mode="+mode, 0))
					q := "SELECT " + fn + "($1) AS v"
					stmt, e := actor.PrepareContext(p.Ctx, q)
					if e != nil {
						t.Fatal(e)
					}
					t.Cleanup(func() { stmt.Close() })
					held := fixture.HeldStatement{Executor: actor, Statement: stmt, SQL: q}
					run(admin, fixture.Exec("REVOKE EXECUTE ON FUNCTION "+id+" FROM PUBLIC", 0))
					s := signalNull(q, nil)
					if mode == "force_generic_plan" {
						s = signalError(q, "42501", "permission denied for function "+fn)
						s.Args = []any{nil}
					}
					fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
					run(admin, fixture.Exec("GRANT EXECUTE ON FUNCTION "+id+" TO PUBLIC", 0))
					fixture.RunStep(t, p.Ctx, held, b.Recorder, signalNull(q, nil))
					stmt.Close()
				}
				run(actor, fixture.Exec("RESET plan_cache_mode", 0))
			}
		})
	}
}
