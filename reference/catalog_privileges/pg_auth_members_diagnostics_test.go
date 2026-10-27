package reference_test

import (
	"context"
	"errors"
	"stuntgres/reference/fixture"
	"testing"
	"unicode/utf8"
)

func TestReferencePGAuthMembersDiagnostics(t *testing.T) {
	pgGroupScenario(t,[]string{"pam_diag_role","pam_diag_member"},func(t *testing.T,p *fixture.Pair,b fixture.Backend) {
		role := pgGroupCreate(t,p,b,"pam_diag_role","NOLOGIN")
		pgGroupCreate(t,p,b,"pam_diag_member","LOGIN")
		pgRolesExec(t,p,b,"GRANT pam_diag_role TO pam_diag_member")
		edge := authMembersOne(t,p,b,b.DB,role)
		pgRolesExec(t,p,b,"CREATE TABLE pam_diag_sentinel(a INT)")
		fixture.RunStep(t,p.Ctx,b.DB,b.Recorder,fixture.Exec("INSERT INTO pam_diag_sentinel VALUES(7)",1))
		q := "SELECT * FROM pg_catalog.pg_auth_members WHERE roleid=$1"
		stmt, err := b.DB.PrepareContext(p.Ctx,q)
		if err != nil { t.Fatal(err) }
		defer stmt.Close()
		held := fixture.HeldStatement{Executor:b.DB,Statement:stmt,SQL:q}
		check := func() {
			authMembersWant(t,authMembersRead(t,p,b,held,q,role),[]authMemberRow{edge},true)
			fixture.RunStep(t,p.Ctx,b.DB,b.Recorder,fixture.Select("SELECT a FROM pam_diag_sentinel",[]string{"a"},[]string{"INT4"},[][]any{{int64(7)}}))
		}
		trailing := "SELECT * FROM pg_catalog.pg_auth_members WHERE"
		cases := []struct{ q,code,message,position string }{
			{"SELECT missing FROM pg_catalog.pg_auth_members","42703",`column "missing" does not exist`,"8"},
			{"SELECT rolpassword FROM pg_catalog.pg_auth_members","42703",`column "rolpassword" does not exist`,"8"},
			{"/*é*/ SELECT missing FROM pg_catalog.pg_auth_members","42703",`column "missing" does not exist`,"14"},
			{"SELECT 'bad'::oid FROM pg_catalog.pg_auth_members","22P02",`invalid input syntax for type oid: "bad"`,"8"},
			{"SELECT * FROM pg_catalog.pg_auth_members_absent","42P01",`relation "pg_catalog.pg_auth_members_absent" does not exist`,"15"},
			{trailing,"42601","syntax error at end of input",fixture.Position(utf8.RuneCountInString(trailing)+1)},
		}
		for _, tc := range cases {
			for _, prepared := range []bool{false,true} {
				s := fixture.ImplicitError(tc.q,fixture.Diagnostic{Code:tc.code,Message:tc.message,Position:tc.position})
				s.SelectRows,s.Prepared,s.ErrorStage = true,prepared,"execute"
				if prepared { s.ErrorStage = "prepare" }
				fixture.RunStep(t,p.Ctx,b.DB,b.Recorder,s)
				check()
			}
		}
		for _, prepared := range []bool{false,true} {
			want := fixture.ControlsReferenceDiagnostic("22P02",`invalid input syntax for type oid: "bad"`)
			want.Context = "unnamed portal parameter $1 = '...'"
			s := fixture.ImplicitError("SELECT * FROM pg_catalog.pg_auth_members WHERE oid=$1",want,"bad")
			s.SelectRows,s.Prepared,s.ErrorStage = true,prepared,"execute"
			fixture.RunStep(t,p.Ctx,b.DB,b.Recorder,s)
			check()
		}
		authMembersBoth(t,p,b,b.DB,"SELECT * FROM pg_auth_members WHERE oid=$1",nil,nil)
		// Existing role command diagnostic; a rejected cycle changes no row IDs.
		s := fixture.RoleReferenceError("GRANT pam_diag_member TO pam_diag_role","0LP01",`role "pam_diag_member" is a member of role "pam_diag_role"`,"",false,"execute")
		fixture.RunStep(t,p.Ctx,b.DB,b.Recorder,s)
		check()
		// Fast pre-canceled pq calls have no nil-output contract.
		if !b.PG {
			all := "SELECT * FROM pg_catalog.pg_auth_members"
			before := authMembersRead(t,p,b,b.DB,all)
			ctx, cancel := context.WithCancel(p.Ctx)
			cancel()
			for _, e := range []fixture.Executor{b.DB,held} {
				rows, err := e.QueryContext(ctx,q,role)
				if rows != nil {
					rows.Close()
					t.Fatal("canceled membership query returned rows")
				}
				if !errors.Is(err,context.Canceled) {
					t.Fatalf("canceled membership query: %v",err)
				}
				var state interface{ SQLState() string }
				if errors.As(err,&state) {
					t.Fatalf("ordinary cancellation has SQLSTATE %s",state.SQLState())
				}
				authMembersWant(t,authMembersRead(t,p,b,b.DB,all),before,true)
				check()
			}
		}
	})
}
