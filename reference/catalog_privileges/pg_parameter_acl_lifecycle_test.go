package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func pgParameterACLLifecycle(t *testing.T, p *fixture.Pair, b fixture.Backend, suffix string) {
	t.Helper()
	name := "pacl18_" + suffix + ".mixed"
	recipient, observer := "pacl18_"+suffix+"_reader", "pacl18_"+suffix+"_observer"
	pgParameterACLRoles(t, p, b, recipient, observer)
	q := "SELECT * FROM pg_parameter_acl WHERE parname=$1"
	held := fixture.ViewHold(t, p, b, q)
	run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
	empty := pgParameterACLSelect(q, b.PG, nil, name)
	fixture.RunStep(t, p.Ctx, held, b.Recorder, empty)
	run(empty)
	run(fixture.Exec(`GRANT SET ON PARAMETER "PACL18_`+suffix+`"."MiXeD" TO `+recipient, 0))
	oid := pgParameterACLOID(t, p, b, name)
	owner := pgParameterACLOwner(b)
	acl := "{" + owner + "=sA/" + owner + "," + recipient + "=s/" + owner + "}"
	pgParameterACLRead(t, p, b, name, acl, oid)
	fixture.RunStep(t, p.Ctx, held, b.Recorder, pgParameterACLSelect(q, b.PG, [][]any{{oid, name, acl}}, name))
	conn, err := b.DB.Conn(p.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	defer func() {
		if _, err := conn.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION"); err != nil {
			t.Error(err)
		}
	}()
	observe := func(want string) {
		t.Helper()
		fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Exec("SET SESSION AUTHORIZATION "+observer, 0))
		s := pgParameterACLSelect(q, b.PG, [][]any{{oid, name, want}}, name)
		fixture.RunStep(t, p.Ctx, conn, b.Recorder, s)
		fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Prepared(s))
		stmt, err := conn.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: conn, Statement: stmt, SQL: q}, b.Recorder, s)
		if err := stmt.Close(); err != nil {
			t.Fatal(err)
		}
		fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Exec("RESET SESSION AUTHORIZATION", 0))
	}
	observe(acl)
	// Retain actual output across owner publication, without requiring pq to be eager.
	old, err := b.DB.QueryContext(p.Ctx, q, name)
	if err != nil {
		t.Fatal(err)
	}
	defer old.Close()
	saved, err := old.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	for _, command := range []string{
		"GRANT SET ON PARAMETER " + name + " TO " + recipient,
		"GRANT ALTER SYSTEM ON PARAMETER " + name + " TO " + recipient,
	} {
		run(fixture.Exec(command, 0))
		if actual := pgParameterACLOID(t, p, b, name); actual != oid {
			t.Fatal("stable parameter identity", oid, actual)
		}
	}
	var oldOID int64
	var oldName, oldACL string
	if !old.Next() {
		t.Fatal("retained positive row", old.Err())
	}
	if err := old.Scan(&oldOID, &oldName, &oldACL); err != nil || oldOID != oid || oldName != name || oldACL != acl {
		t.Fatal("retained values", oldOID, oldName, oldACL, err)
	}
	if old.Next() || old.Err() != nil {
		t.Fatal("retained EOF", old.Err())
	}
	if err := old.Close(); err != nil {
		t.Fatal(err)
	}
	pgParameterACLMetadata(t, saved, b.PG)
	acl = "{" + owner + "=sA/" + owner + "," + recipient + "=sA/" + owner + "}"
	fixture.RunStep(t, p.Ctx, held, b.Recorder, pgParameterACLSelect(q, b.PG, [][]any{{oid, name, acl}}, name))
	observe(acl)
	missing := fixture.ImplicitError("GRANT SET ON PARAMETER "+name+" TO pacl18_nonexistent_recipient", fixture.Diagnostic{Code: "42704", Message: `role "pacl18_nonexistent_recipient" does not exist`})
	missing.ErrorStage = "execute"
	run(missing)
	pgParameterACLRead(t, p, b, name, acl, oid)
	run(fixture.Exec("GRANT ALL ON PARAMETER "+name+" TO "+recipient+" WITH GRANT OPTION", 0))
	if actual := pgParameterACLOID(t, p, b, name); actual != oid {
		t.Fatal("option update identity", oid, actual)
	}
	optionACL := "{" + owner + "=sA/" + owner + "," + recipient + "=s*A*/" + owner + "}"
	fixture.RunStep(t, p.Ctx, held, b.Recorder, pgParameterACLSelect(q, b.PG, [][]any{{oid, name, optionACL}}, name))
	for range 2 {
		run(fixture.Exec("REVOKE ALL ON PARAMETER "+name+" FROM "+recipient, 0))
		run(empty)
		fixture.RunStep(t, p.Ctx, held, b.Recorder, empty)
	}
	run(fixture.Exec("GRANT SET ON PARAMETER "+name+" TO "+recipient, 0))
	fresh := pgParameterACLOID(t, p, b, name)
	if fresh == oid {
		t.Fatal("regrant reused removed object identity", oid)
	}
	pgParameterACLRead(t, p, b, name, "{"+owner+"=sA/"+owner+","+recipient+"=s/"+owner+"}", fresh)
	run(fixture.Exec("REVOKE ALL ON PARAMETER "+name+" FROM "+recipient, 0))
	fixture.RunStep(t, p.Ctx, held, b.Recorder, empty)
	run(empty)
}

func TestReferencePGParameterACLIdentityAndLifecycle(t *testing.T) {
	pgParameterACLScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		pgParameterACLLifecycle(t, p, b, "identity")
	})
}

func TestReferencePGParameterACLFeatureSmoke(t *testing.T) {
	pgParameterACLScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		pgParameterACLLifecycle(t, p, b, "smoke")
	})
}
