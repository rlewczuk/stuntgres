package reference_test

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"stuntgres/reference/fixture"
)

func withSubscriptionRelReader(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn, *sql.Conn)) {
	t.Helper()
	fixture.RoleReferenceCleanup(t, []string{"subrel_reader"})
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			admin, actor := fixture.LockConnections(t, p.Ctx, b.DB)
			fixture.RunStep(t, p.Ctx, admin, b.Recorder, fixture.Exec("CREATE ROLE subrel_reader", 0))
			// System ACLs survive public-schema reset. Register repair before mutation;
			// statements close in body before these borrowers and role cleanup.
			defer func() {
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				for _, q := range []string{"GRANT SELECT ON pg_catalog.pg_subscription_rel TO PUBLIC", "REVOKE SELECT ON pg_catalog.pg_subscription_rel FROM subrel_reader"} {
					if _, err := admin.ExecContext(ctx, q); err != nil { t.Error(err) }
				}
				if _, err := actor.ExecContext(ctx, "RESET ROLE"); err != nil { t.Error(err) }
			}()
			fixture.RunStep(t, p.Ctx, actor, b.Recorder, fixture.Exec("SET ROLE subrel_reader", 0))
			body(t, p, b, admin, actor)
		})
	}
}

func subscriptionRelDenied(t *testing.T, p *fixture.Pair, b fixture.Backend, e fixture.Executor, q string, prepared bool) {
	t.Helper()
	s := fixture.ErrorDiagnostic(q, fixture.ControlsReferenceDiagnostic("42501", "permission denied for table pg_subscription_rel"))
	s.SelectRows, s.Prepared, s.ErrorStage = true, prepared, "execute"
	fixture.RunStep(t, p.Ctx, e, b.Recorder, s)
}

func subscriptionRelPrivilegeLifecycle(t *testing.T, smoke bool) {
	withSubscriptionRelReader(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, admin, actor *sql.Conn) {
		run := func(q string) { fixture.RunStep(t, p.Ctx, admin, b.Recorder, fixture.Exec(q, 0)) }
		if smoke {
			subscriptionRelShape(t, p, b, admin)
			fixture.RunStep(t, p.Ctx, admin, b.Recorder, subscriptionRelEmpty(b.PG, "SELECT * FROM pg_catalog.pg_subscription_rel"))
		}
		steps := []fixture.Step{subscriptionRelEmpty(b.PG, "SELECT * FROM pg_catalog.pg_subscription_rel")}
		if !smoke {
			steps = append(steps,
				fixture.Select("SELECT srsubstate FROM pg_catalog.pg_subscription_rel", []string{"srsubstate"}, []string{"CHAR"}, nil),
				subscriptionRelEmpty(b.PG, "SELECT * FROM pg_catalog.pg_subscription_rel WHERE FALSE"),
				subscriptionRelEmpty(b.PG, "SELECT * FROM pg_catalog.pg_subscription_rel LIMIT 0"),
				fixture.Select("SELECT 1 FROM pg_catalog.pg_subscription_rel", []string{"?column?"}, []string{"INT4"}, nil))
		}
		steps = append(steps, subscriptionRelCount())
		held := make([]fixture.HeldStatement, len(steps))
		for i, s := range steps {
			stmt, err := actor.PrepareContext(p.Ctx, s.Query)
			if err != nil { t.Fatal(err) }
			defer stmt.Close()
			held[i] = fixture.HeldStatement{Executor: actor, Statement: stmt, SQL: s.Query}
		}
		check := func(allowed, public, reader bool) {
			t.Helper()
			subscriptionRelStoredACL(t, p, b, admin, public, reader)
			rights := fixture.Select("SELECT has_table_privilege('subrel_reader','pg_catalog.pg_subscription_rel','SELECT') AS can_read", []string{"can_read"}, []string{"BOOL"}, [][]any{{allowed}})
			fixture.RunStep(t, p.Ctx, admin, b.Recorder, rights)
			fixture.RunStep(t, p.Ctx, admin, b.Recorder, fixture.Prepared(rights))
			for i, s := range steps {
				for _, e := range []fixture.Executor{actor, held[i]} {
					if allowed { fixture.RunStep(t, p.Ctx, e, b.Recorder, s) } else { subscriptionRelDenied(t, p, b, e, s.Query, false) }
				}
				if allowed { fixture.RunStep(t, p.Ctx, actor, b.Recorder, fixture.Prepared(s)) } else { subscriptionRelDenied(t, p, b, actor, s.Query, true) }
			}
			// Privileged owner/superuser remains admitted independently of reader ACL.
			fixture.RunStep(t, p.Ctx, admin, b.Recorder, subscriptionRelEmpty(b.PG, "SELECT * FROM pg_catalog.pg_subscription_rel"))
		}
		check(true, true, false)
		run("REVOKE SELECT ON pg_catalog.pg_subscription_rel FROM PUBLIC")
		check(false, false, false)
		run("GRANT SELECT ON pg_catalog.pg_subscription_rel TO subrel_reader")
		check(true, false, true)
		run("REVOKE SELECT ON pg_catalog.pg_subscription_rel FROM subrel_reader")
		check(false, false, false)
		run("GRANT SELECT ON pg_catalog.pg_subscription_rel TO PUBLIC")
		check(true, true, false)
	})
}

func TestReferencePGSubscriptionRelPrivileges(t *testing.T) { subscriptionRelPrivilegeLifecycle(t, false) }
func TestReferencePGSubscriptionRelFeatureSmoke(t *testing.T) { subscriptionRelPrivilegeLifecycle(t, true) }
