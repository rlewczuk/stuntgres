package reference_test

import (
	"context"
	"errors"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
	"unicode/utf8"
)

func TestReferencePGAuthMembersControls(t *testing.T) {
	pgGroupScenario(t,[]string{"pam_control_role","pam_control_member"},func(t *testing.T,p *fixture.Pair,b fixture.Backend) {
		role := pgGroupCreate(t,p,b,"pam_control_role","NOLOGIN")
		pgGroupCreate(t,p,b,"pam_control_member","LOGIN")
		pgRolesExec(t,p,b,"GRANT pam_control_role TO pam_control_member WITH ADMIN FALSE, INHERIT FALSE, SET FALSE")
		edge := authMembersOne(t,p,b,b.DB,role)
		seed := authMembersSeed(t,p,b)
		q := "SELECT * FROM pg_auth_members WHERE roleid=$1 OR member=3373"
		want := append(seed,edge)
		stmt, err := b.DB.PrepareContext(p.Ctx,q)
		if err != nil { t.Fatal(err) }
		defer stmt.Close()
		held := fixture.HeldStatement{Executor:b.DB,Statement:stmt,SQL:q}
		check := func() { authMembersWant(t,authMembersRead(t,p,b,held,q,role),want,true) }
		check()
		pgRolesExec(t,p,b,"CREATE TABLE pam_control_sentinel(a INT)")
		fixture.RunStep(t,p.Ctx,b.DB,b.Recorder,fixture.Exec("INSERT INTO pam_control_sentinel VALUES(7)",1))
		if b.PG { return } // Reset/Dump/Restore are public native controls, not SQL.
		old, err := b.DB.QueryContext(p.Ctx,q,role)
		if err != nil { t.Fatal(err) }
		defer old.Close()
		dump := fixture.CommentInquiryDump(t,p)
		for _, forbidden := range []string{"CREATE ROLE","GRANT ","INSERT INTO \"pg_catalog\".\"pg_auth_members\""} {
			if strings.Contains(dump,forbidden) { t.Fatal("shared membership serialized",forbidden) }
		}
		prefix := dump+"INSERT INTO pam_control_sentinel VALUES("
		err = stuntgres.Restore(p.Ctx,p.Native,strings.NewReader(prefix+"'bad');"))
		diagnostic := fixture.ControlsReferenceDiagnostic("22P02",`invalid input syntax for type integer: "bad"`)
		diagnostic.Position = fixture.Position(utf8.RuneCountInString(prefix)+1)
		fixture.AssertDiagnostic(t,err,&diagnostic)
		check()
		fixture.RunStep(t,p.Ctx,b.DB,b.Recorder,fixture.Select("SELECT a FROM pam_control_sentinel",[]string{"a"},[]string{"INT4"},[][]any{{int64(7)}}))
		if fixture.CommentInquiryDump(t,p) != dump { t.Fatal("failed Restore changed catalog") }
		canceled, cancel := context.WithCancel(p.Ctx)
		cancel()
		if err := stuntgres.Restore(canceled,p.Native,strings.NewReader(dump)); !errors.Is(err,context.Canceled) { t.Fatal(err) }
		check()
		if err := stuntgres.Reset(p.Ctx,p.Native); err != nil { t.Fatal(err) }
		check()
		if err := stuntgres.Restore(p.Ctx,p.Native,strings.NewReader(dump)); err != nil { t.Fatal(err) }
		check()
		fixture.RunStep(t,p.Ctx,b.DB,b.Recorder,fixture.Select("SELECT a FROM pam_control_sentinel",[]string{"a"},[]string{"INT4"},[][]any{{int64(7)}}))
		authMembersWant(t,authMembersConsume(t,old,false),want,true)
		pgRolesExec(t,p,b,"REVOKE pam_control_role FROM pam_control_member")
		pgRolesExec(t,p,b,"GRANT pam_control_role TO pam_control_member")
		if next := authMembersOne(t,p,b,b.DB,role); next.oid == edge.oid { t.Fatal("Restore rewound membership allocator") }
	})
}
