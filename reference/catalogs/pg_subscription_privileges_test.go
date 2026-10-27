package reference_test

import (
	"context"
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func subscriptionDenied(t *testing.T, p *fixture.Pair, b fixture.Backend, e fixture.Executor, q string, prepared bool, args ...any) {
	t.Helper()
	s := fixture.ErrorDiagnostic(q, fixture.ControlsReferenceDiagnostic("42501", "permission denied for table pg_subscription"), args...)
	s.SelectRows, s.ErrorStage, s.Prepared = true, "execute", prepared
	fixture.RunStep(t,p.Ctx,e,b.Recorder,s)
}

func withSubscriptionReader(t *testing.T, body func(*fixture.Pair, fixture.Backend, *sql.Conn, *sql.Conn)) {
	t.Helper()
	fixture.RoleReferenceCleanup(t, []string{"subcat_reader", "subcat_delegate"})
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			admin, actor := fixture.LockConnections(t,p.Ctx,b.DB)
			run := func(e fixture.Executor, q string) { fixture.RunStep(t,p.Ctx,e,b.Recorder,fixture.Exec(q,0)) }
			run(admin,"CREATE ROLE subcat_reader")
			run(admin,"CREATE ROLE subcat_delegate")
			// Repair shared catalog state before borrowers and cluster roles close.
			defer func() {
				ctx,cancel := context.WithTimeout(context.Background(),10*time.Second)
				defer cancel()
				for _, q := range []string{
					"RESET ROLE",
					"REVOKE SELECT(subconninfo) ON pg_catalog.pg_subscription FROM subcat_reader,subcat_delegate",
					"REVOKE SELECT ON pg_catalog.pg_subscription FROM subcat_reader,subcat_delegate",
					"REVOKE pg_read_all_data,pg_monitor,pg_read_all_stats,pg_create_subscription,subcat_delegate FROM subcat_reader",
				} { if _,err := admin.ExecContext(ctx,q); err != nil { t.Error(err) } }
				if _,err := actor.ExecContext(ctx,"RESET ROLE"); err != nil { t.Error(err) }
			}()
			run(actor,"SET ROLE subcat_reader")
			body(p,b,admin,actor)
		})
	}
}

func subscriptionPublic(t *testing.T,p *fixture.Pair,b fixture.Backend,e fixture.Executor) {
	t.Helper()
	for _, source := range []string{"pg_catalog.pg_subscription","pg_subscription"} {
		s := subscriptionEmpty(b.PG,"SELECT "+subscriptionProjection()+" FROM "+source,true)
		fixture.RunStep(t,p.Ctx,e,b.Recorder,s)
		fixture.RunStep(t,p.Ctx,e,b.Recorder,fixture.Prepared(s))
	}
	for _, s := range []fixture.Step{
		fixture.Select("SELECT 1 FROM pg_catalog.pg_subscription",[]string{"?column?"},[]string{"INT4"},nil),
		fixture.Select("SELECT count(*) FROM pg_catalog.pg_subscription",[]string{"count"},[]string{"INT8"},[][]any{{int64(0)}}),
	} { fixture.RunStep(t,p.Ctx,e,b.Recorder,s); fixture.RunStep(t,p.Ctx,e,b.Recorder,fixture.Prepared(s)) }
}

func TestReferencePGSubscriptionPrivileges(t *testing.T) {
	withSubscriptionReader(t,func(p *fixture.Pair,b fixture.Backend,admin,actor *sql.Conn) {
		subscriptionPublic(t,p,b,actor)
		for _, q := range []string{
			"SELECT subconninfo FROM pg_catalog.pg_subscription",
			"SELECT subconninfo FROM pg_catalog.pg_subscription WHERE FALSE",
			"SELECT * FROM pg_catalog.pg_subscription WHERE FALSE",
			"SELECT subconninfo FROM pg_catalog.pg_subscription LIMIT 0",
			"SELECT subname FROM pg_catalog.pg_subscription WHERE subconninfo='x'",
			"SELECT subname FROM pg_catalog.pg_subscription ORDER BY subconninfo LIMIT 0",
			"SELECT pg_subscription FROM pg_catalog.pg_subscription WHERE FALSE",
			"SELECT subconninfo FROM pg_catalog.pg_subscription WHERE oid=$1",
		} {
			var args []any
			if q == "SELECT subconninfo FROM pg_catalog.pg_subscription WHERE oid=$1" { args = []any{int64(0)} }
			subscriptionDenied(t,p,b,actor,q,false,args...)
			subscriptionDenied(t,p,b,actor,q,true,args...)
			stmt,err := actor.PrepareContext(p.Ctx,q)
			if err != nil { t.Fatal("unauthorized preparation must succeed",err) }
			subscriptionDenied(t,p,b,fixture.HeldStatement{actor,stmt,q},q,false,args...)
			if err := stmt.Close(); err != nil { t.Fatal(err) }
		}
	})
}

func subscriptionLifecycle(t *testing.T, smoke bool) {
	withSubscriptionReader(t,func(p *fixture.Pair,b fixture.Backend,admin,actor *sql.Conn) {
		run := func(q string) { fixture.RunStep(t,p.Ctx,admin,b.Recorder,fixture.Exec(q,0)) }
		if smoke {
			fixture.RunStep(t,p.Ctx,admin,b.Recorder,subscriptionEmpty(b.PG,"SELECT * FROM pg_catalog.pg_subscription",false))
			subscriptionShape(t,p,b,admin)
		}
		subscriptionPublic(t,p,b,actor)
		queries := []string{"SELECT subconninfo FROM pg_catalog.pg_subscription WHERE FALSE","SELECT * FROM pg_catalog.pg_subscription WHERE FALSE","SELECT * FROM pg_catalog.pg_subscription WHERE oid=$1"}
		var held []fixture.HeldStatement
		for _, q := range queries {
			stmt,err := actor.PrepareContext(p.Ctx,q)
			if err != nil { t.Fatal(err) }
			defer stmt.Close()
			held = append(held,fixture.HeldStatement{actor,stmt,q})
		}
		check := func(allowed bool) {
			for i,q := range queries {
				var args []any
				if i == 2 { args = []any{int64(0)} }
				for _, e := range []fixture.Executor{actor,held[i]} {
					if !allowed { subscriptionDenied(t,p,b,e,q,false,args...); continue }
					s := subscriptionEmpty(b.PG,q,false,args...)
					if i == 0 { s = fixture.Select(q,[]string{"subconninfo"},[]string{"TEXT"},nil) }
					fixture.RunStep(t,p.Ctx,e,b.Recorder,s)
				}
				if !allowed { subscriptionDenied(t,p,b,actor,q,true,args...) } else {
					s := subscriptionEmpty(b.PG,q,false,args...)
					if i == 0 { s = fixture.Select(q,[]string{"subconninfo"},[]string{"TEXT"},nil) }
					fixture.RunStep(t,p.Ctx,actor,b.Recorder,fixture.Prepared(s))
				}
			}
			subscriptionPublic(t,p,b,actor)
		}
		check(false)
		for _, action := range []string{"GRANT","REVOKE","GRANT","REVOKE"} {
			preposition := "TO"; if action == "REVOKE" { preposition = "FROM" }
			run(action+" SELECT(subconninfo) ON pg_catalog.pg_subscription "+preposition+" subcat_reader")
			check(action == "GRANT")
		}
		if smoke { return }
		run("GRANT SELECT ON pg_catalog.pg_subscription TO subcat_reader"); check(true)
		run("REVOKE SELECT ON pg_catalog.pg_subscription FROM subcat_reader"); check(false)
		run("GRANT SELECT(subconninfo) ON pg_catalog.pg_subscription TO subcat_delegate")
		run("GRANT subcat_delegate TO subcat_reader"); check(true)
		run("REVOKE subcat_delegate FROM subcat_reader"); check(false)
	})
}

func TestReferencePGSubscriptionGrantLifecycle(t *testing.T) { subscriptionLifecycle(t,false) }
func TestReferencePGSubscriptionFeatureSmoke(t *testing.T) { subscriptionLifecycle(t,true) }

func TestReferencePGSubscriptionPredefinedRoles(t *testing.T) {
	withSubscriptionReader(t,func(p *fixture.Pair,b fixture.Backend,admin,actor *sql.Conn) {
		q := "SELECT subconninfo FROM pg_catalog.pg_subscription WHERE FALSE"
		stmt,err := actor.PrepareContext(p.Ctx,q)
		if err != nil { t.Fatal(err) }; defer stmt.Close()
		held := fixture.HeldStatement{actor,stmt,q}
		run := func(q string) { fixture.RunStep(t,p.Ctx,admin,b.Recorder,fixture.Exec(q,0)) }
		for _, role := range []string{"pg_read_all_data","pg_monitor","pg_read_all_stats","pg_create_subscription"} {
			run("GRANT "+role+" TO subcat_reader")
			for _, e := range []fixture.Executor{actor,held} {
				if role == "pg_read_all_data" { fixture.RunStep(t,p.Ctx,e,b.Recorder,fixture.Select(q,[]string{"subconninfo"},[]string{"TEXT"},nil)) } else { subscriptionDenied(t,p,b,e,q,false) }
			}
			if role == "pg_read_all_data" {
				fixture.RunStep(t,p.Ctx,actor,b.Recorder,fixture.Prepared(fixture.Select(q,[]string{"subconninfo"},[]string{"TEXT"},nil)))
			} else { subscriptionDenied(t,p,b,actor,q,true) }
			subscriptionPublic(t,p,b,actor)
			run("REVOKE "+role+" FROM subcat_reader")
			subscriptionDenied(t,p,b,held,q,false)
		}
	})
}
