package reference_test

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"stuntgres/reference/fixture"
)

func withShseclabelReader(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn, *sql.Conn)) {
	t.Helper()
	fixture.RoleReferenceCleanup(t, []string{"shseclabel_reader"})
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			admin, actor := fixture.LockConnections(t, p.Ctx, b.DB)
			fixture.RunStep(t, p.Ctx, admin, b.Recorder, fixture.Exec("CREATE ROLE shseclabel_reader", 0))
			// System ACLs survive public-schema reset. Register repair before mutation;
			// statements close in body before these borrowers and role cleanup.
			defer func() {
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				for _, q := range []string{"GRANT SELECT ON pg_catalog.pg_shseclabel TO PUBLIC", "REVOKE SELECT ON pg_catalog.pg_shseclabel FROM shseclabel_reader"} {
					if _, err := admin.ExecContext(ctx, q); err != nil { t.Error(err) }
				}
				if _, err := actor.ExecContext(ctx, "RESET ROLE"); err != nil { t.Error(err) }
			}()
			fixture.RunStep(t, p.Ctx, actor, b.Recorder, fixture.Exec("SET ROLE shseclabel_reader", 0))
			body(t, p, b, admin, actor)
		})
	}
}

func shseclabelDenied(t *testing.T, p *fixture.Pair, b fixture.Backend, e fixture.Executor, q string, prepared bool) {
	t.Helper()
	s := fixture.ErrorDiagnostic(q, fixture.ControlsReferenceDiagnostic("42501", "permission denied for table pg_shseclabel"))
	s.SelectRows, s.Prepared, s.ErrorStage = true, prepared, "execute"
	fixture.RunStep(t, p.Ctx, e, b.Recorder, s)
}

func shseclabelPrivilegeLifecycle(t *testing.T, smoke bool) {
	withShseclabelReader(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, admin, actor *sql.Conn) {
		run := func(q string) { fixture.RunStep(t, p.Ctx, admin, b.Recorder, fixture.Exec(q, 0)) }
		if smoke {
			shseclabelShape(t, p, b, admin)
            shseclabelProviderSmoke(t,p,b,admin,actor)
			fixture.RunStep(t, p.Ctx, admin, b.Recorder, shseclabelEmpty(b.PG, "SELECT * FROM pg_catalog.pg_shseclabel"))
		}
		steps := []fixture.Step{shseclabelEmpty(b.PG, "SELECT * FROM pg_catalog.pg_shseclabel")}
		if !smoke {
			steps = append(steps,
				fixture.Select("SELECT provider FROM pg_catalog.pg_shseclabel", []string{"provider"}, []string{"TEXT"}, nil),
				shseclabelEmpty(b.PG, "SELECT * FROM pg_catalog.pg_shseclabel WHERE FALSE"),
				shseclabelEmpty(b.PG, "SELECT * FROM pg_catalog.pg_shseclabel LIMIT 0"),
				fixture.Select("SELECT 1 FROM pg_catalog.pg_shseclabel", []string{"?column?"}, []string{"INT4"}, nil))
		}
		steps = append(steps, shseclabelCount())
		held := make([]fixture.HeldStatement, len(steps))
		for i, s := range steps {
			stmt, err := actor.PrepareContext(p.Ctx, s.Query)
			if err != nil { t.Fatal(err) }
			defer stmt.Close()
			held[i] = fixture.HeldStatement{Executor: actor, Statement: stmt, SQL: s.Query}
		}
		check := func(allowed, public, reader bool) {
			t.Helper()
			shseclabelStoredACL(t,p,b,admin,public,reader)
			rights := fixture.Select("SELECT has_table_privilege('shseclabel_reader','pg_catalog.pg_shseclabel','SELECT') AS can_read", []string{"can_read"}, []string{"BOOL"}, [][]any{{allowed}})
			fixture.RunStep(t, p.Ctx, admin, b.Recorder, rights)
			fixture.RunStep(t, p.Ctx, admin, b.Recorder, fixture.Prepared(rights))
			for i, s := range steps {
				for _, e := range []fixture.Executor{actor, held[i]} {
					if allowed { fixture.RunStep(t, p.Ctx, e, b.Recorder, s) } else { shseclabelDenied(t, p, b, e, s.Query, false) }
				}
				if allowed { fixture.RunStep(t, p.Ctx, actor, b.Recorder, fixture.Prepared(s)) } else { shseclabelDenied(t, p, b, actor, s.Query, true) }
			}
			// Privileged owner/superuser remains admitted independently of reader ACL.
			fixture.RunStep(t, p.Ctx, admin, b.Recorder, shseclabelEmpty(b.PG, "SELECT * FROM pg_catalog.pg_shseclabel"))
		}
		check(true, true, false)
		run("REVOKE SELECT ON pg_catalog.pg_shseclabel FROM PUBLIC")
		check(false, false, false)
		run("GRANT SELECT ON pg_catalog.pg_shseclabel TO shseclabel_reader")
		check(true, false, true)
		run("REVOKE SELECT ON pg_catalog.pg_shseclabel FROM shseclabel_reader")
		check(false, false, false)
		run("GRANT SELECT ON pg_catalog.pg_shseclabel TO PUBLIC")
		check(true, true, false)
	})
}

func TestReferencePGShseclabelPrivileges(t *testing.T) { shseclabelPrivilegeLifecycle(t, false) }
func TestReferencePGShseclabelFeatureSmoke(t *testing.T) { shseclabelPrivilegeLifecycle(t, true) }

func shseclabelStoredACL(t *testing.T,p *fixture.Pair,b fixture.Backend,e fixture.Executor,public,reader bool) {
	t.Helper()
	var want [][]any
	for _,privilege:=range []string{"DELETE","INSERT","MAINTAIN","REFERENCES","SELECT","TRIGGER","TRUNCATE","UPDATE"} {
		want=append(want,[]any{true,true,false,privilege,false})
	}
	if public {want=append(want,[]any{true,false,true,"SELECT",false})}
	if reader {want=append(want,[]any{true,false,false,"SELECT",false})}
	s:=fixture.Select("SELECT a.grantor=c.relowner AS owner_grantor,a.grantee=c.relowner AS owner_grantee,a.grantee=0 AS public_grantee,a.privilege_type,a.is_grantable FROM pg_catalog.pg_class c CROSS JOIN LATERAL aclexplode(c.relacl) a WHERE c.oid=3592",[]string{"owner_grantor","owner_grantee","public_grantee","privilege_type","is_grantable"},[]string{"BOOL","BOOL","BOOL","TEXT","BOOL"},want)
	s.Bag=true
	fixture.RunStep(t,p.Ctx,e,b.Recorder,s)
	fixture.RunStep(t,p.Ctx,e,b.Recorder,fixture.Prepared(s))
}
