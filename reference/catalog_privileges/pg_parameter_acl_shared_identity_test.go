package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func pgParameterACLSharedScenario(t *testing.T, suffix string, body func(*testing.T, *fixture.Pair, fixture.Backend, fixture.Backend, func(string) fixture.Backend)) {
	t.Helper()
	fixture.RoleReferenceCleanup(t, []string{"pacs_reader", "pacs_grantor", "pacs_observer"})
	var databases []string
	fixture.DatabaseReferenceCleanup(t, &databases)
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			pgParameterACLRoles(t, p, b, "pacs_reader", "pacs_grantor", "pacs_observer")
			create := func(s string) fixture.Backend {
				name := "pacs_"+suffix+"_"+s
				if b.PG { databases = append(databases, name) }
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("CREATE DATABASE "+name, 0))
				db, _, record := fixture.DatabaseScenarioPool(t, p, b.PG, name, "")
				return fixture.Backend{Name: b.Name, DB: db, Recorder: record, PG: b.PG}
			}
			peer := create("b")
			body(t, p, b, peer, create)
		})
	}
}

func TestReferencePGParameterACLSharedIdentity(t *testing.T) {
	pgParameterACLSharedScenario(t, "identity", func(t *testing.T, p *fixture.Pair, a, b fixture.Backend, create func(string) fixture.Backend) {
		const name = "pacs_identity.mixed"
		const q = "SELECT * FROM pg_parameter_acl WHERE parname=$1"
		run := func(view fixture.Backend, s fixture.Step) { fixture.RunStep(t, p.Ctx, view.DB, view.Recorder, s) }
		held := fixture.ViewHold(t, p, b, q)
		empty := pgParameterACLSelect(q, b.PG, nil, name)
		run(b, fixture.Exec("REVOKE ALL ON PARAMETER "+name+" FROM pacs_reader", 0))
		fixture.RunStep(t, p.Ctx, held, b.Recorder, empty)
		run(a, fixture.Exec(`GRANT SET ON PARAMETER "PACS_IDENTITY"."MiXeD" TO pacs_reader`, 0))
		oid := pgParameterACLOID(t, p, a, name)
		owner := pgParameterACLOwner(a)
		acl := "{"+owner+"=sA/"+owner+",pacs_reader=s/"+owner+"}"
		c := create("c")
		views := []fixture.Backend{a, b, c}
		check := func(want string, identity int64) {
			for _, view := range views { pgParameterACLRead(t, p, view, name, want, identity) }
			fixture.RunStep(t, p.Ctx, held, b.Recorder, pgParameterACLSelect(q, b.PG, [][]any{{identity, name, want}}, name))
		}
		check(acl, oid)
		observer, err := b.DB.Conn(p.Ctx)
		if err != nil { t.Fatal(err) }
		defer observer.Close()
		defer observer.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION")
		fixture.RunStep(t, p.Ctx, observer, b.Recorder, fixture.Exec("SET SESSION AUTHORIZATION pacs_observer", 0))
		fixture.RunStep(t, p.Ctx, observer, b.Recorder, pgParameterACLSelect(q, b.PG, [][]any{{oid, name, acl}}, name))
		old, err := a.DB.QueryContext(p.Ctx, q, name)
		if err != nil { t.Fatal(err) }
		defer old.Close()
		saved, err := old.ColumnTypes()
		if err != nil { t.Fatal(err) }
		run(b, fixture.Exec("GRANT ALTER SYSTEM ON PARAMETER "+name+" TO pacs_reader", 0))
		check("{"+owner+"=sA/"+owner+",pacs_reader=sA/"+owner+"}", oid)
		// Refresh the role root as well; old Rows still carry their own pair.
		run(c, fixture.Exec("CREATE ROLE pacs_transient", 0))
		run(c, fixture.Exec("DROP ROLE pacs_transient", 0))
		var oldOID int64
		var oldName, oldACL string
		if !old.Next() { t.Fatal("retained positive row", old.Err()) }
		if err := old.Scan(&oldOID, &oldName, &oldACL); err != nil || oldOID != oid || oldName != name || oldACL != acl { t.Fatal(oldOID, oldName, oldACL, err) }
		if old.Next() || old.Err() != nil { t.Fatal(old.Err()) }
		old.Close()
		pgParameterACLMetadata(t, saved, a.PG)
		run(b, fixture.Exec("REVOKE ALL ON PARAMETER "+name+" FROM pacs_reader", 0))
		for _, view := range views { run(view, empty) }
		fixture.RunStep(t, p.Ctx, held, b.Recorder, empty)
		run(c, fixture.Exec("GRANT SET ON PARAMETER "+name+" TO pacs_reader", 0))
		fresh := pgParameterACLOID(t, p, b, name)
		if fresh == oid { t.Fatal("removed parameter identity reused", oid) }
		check(acl, fresh)
		run(a, fixture.Exec("REVOKE ALL ON PARAMETER "+name+" FROM pacs_reader", 0))
		run(a, fixture.Exec("GRANT SET ON PARAMETER "+name+" TO PUBLIC", 0))
		emptyOID := pgParameterACLOID(t, p, a, name)
		run(b, fixture.Exec("REVOKE ALL ON PARAMETER "+name+" FROM "+owner, 0))
		run(c, fixture.Exec("REVOKE ALL ON PARAMETER "+name+" FROM PUBLIC", 0))
		check("{}", emptyOID)
		run(b, fixture.Exec("GRANT ALL ON PARAMETER "+name+" TO "+owner, 0))
		for _, view := range views { run(view, empty) }
	})
}
