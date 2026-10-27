package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func authMembersSeed(t *testing.T, p *fixture.Pair, b fixture.Backend) []authMemberRow {
	t.Helper()
	got := authMembersRead(t, p, b, b.DB, "SELECT * FROM pg_auth_members WHERE member=3373")
	want := []authMemberRow{{0,3374,3373,10,false,true,true},{0,3375,3373,10,false,true,true},{0,3377,3373,10,false,true,true}}
	authMembersWant(t, got, want, false)
	return got
}

func TestReferencePGAuthMembersEdges(t *testing.T) {
	names := []string{"pam_edge_a","pam_edge_b","pam_edge_c","pam_edge_admin","pam_edge_new","pam_edge_creator","pam_edge_created","pam_edge_none"}
	pgGroupScenario(t, names, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		authMembersSeed(t,p,b)
		authMembersWant(t,authMembersRead(t,p,b,b.DB,"SELECT * FROM pg_auth_members"),[]authMemberRow{{0,3374,3373,10,false,true,true},{0,3375,3373,10,false,true,true},{0,3377,3373,10,false,true,true}},false)
		a := pgGroupCreate(t,p,b,"pam_edge_a","NOLOGIN")
		bb := pgGroupCreate(t,p,b,"pam_edge_b","LOGIN NOINHERIT")
		c := pgGroupCreate(t,p,b,"pam_edge_c","NOLOGIN")
		admin := pgGroupCreate(t,p,b,"pam_edge_admin","LOGIN")
		pgGroupCreate(t,p,b,"pam_edge_none","LOGIN SUPERUSER")
		var bootstrap int64
		if err := b.DB.QueryRowContext(p.Ctx,"SELECT oid FROM pg_roles WHERE rolname=current_user").Scan(&bootstrap); err != nil { t.Fatal(err) }
		pgRolesExec(t,p,b,"GRANT pam_edge_a TO pam_edge_b")
		pgRolesExec(t,p,b,"GRANT pam_edge_b TO pam_edge_c WITH ADMIN FALSE, INHERIT FALSE, SET FALSE")
		pgRolesExec(t,p,b,"GRANT pam_edge_a TO pam_edge_admin WITH ADMIN TRUE, INHERIT FALSE, SET FALSE")
		pgRolesExec(t,p,b,"GRANT pam_edge_a TO pam_edge_b WITH ADMIN TRUE, INHERIT TRUE, SET TRUE GRANTED BY pam_edge_admin")
		q := "SELECT * FROM pg_auth_members WHERE roleid=$1 OR roleid=$2"
		want := []authMemberRow{{0,a,bb,bootstrap,false,false,true},{0,bb,c,bootstrap,false,false,false},{0,a,admin,bootstrap,true,false,false},{0,a,bb,admin,true,true,true}}
		got := authMembersRead(t,p,b,b.DB,q,a,bb)
		authMembersWant(t,got,want,false)
		authMembersBoth(t,p,b,b.DB,q,got,a,bb)
		pgRolesExec(t,p,b,"REVOKE pam_edge_a FROM pam_edge_admin CASCADE")
		var survivors []authMemberRow
		for _, edge := range got {
			if edge.member != admin && edge.grantor != admin { survivors = append(survivors,edge) }
		}
		authMembersBoth(t,p,b,b.DB,q,survivors,a,bb)
		pgRolesExec(t,p,b,"CREATE ROLE pam_edge_new IN ROLE pam_edge_a ROLE pam_edge_c ADMIN pam_edge_admin")
		var n int64
		if err := b.DB.QueryRowContext(p.Ctx,"SELECT oid FROM pg_roles WHERE rolname='pam_edge_new'").Scan(&n); err != nil { t.Fatal(err) }
		initial := authMembersRead(t,p,b,b.DB,"SELECT * FROM pg_auth_members WHERE roleid=$1 OR member=$1",n)
		authMembersWant(t,initial,[]authMemberRow{{0,a,n,bootstrap,false,true,true},{0,n,c,bootstrap,false,true,true},{0,n,admin,bootstrap,true,true,true}},false)
		creator := pgGroupCreate(t,p,b,"pam_edge_creator","LOGIN CREATEROLE")
		conn, err := b.DB.Conn(p.Ctx)
		if err != nil { t.Fatal(err) }
		defer conn.Close()
		defer conn.ExecContext(p.Ctx,"RESET SESSION AUTHORIZATION")
		if _, err := conn.ExecContext(p.Ctx,"SET SESSION AUTHORIZATION pam_edge_creator"); err != nil { t.Fatal(err) }
		if _, err := conn.ExecContext(p.Ctx,"CREATE ROLE pam_edge_created"); err != nil { t.Fatal(err) }
		var created int64
		if err := conn.QueryRowContext(p.Ctx,"SELECT oid FROM pg_roles WHERE rolname='pam_edge_created'").Scan(&created); err != nil { t.Fatal(err) }
		edge := authMembersOne(t,p,b,conn,created)
		authMembersWant(t,[]authMemberRow{edge},[]authMemberRow{{0,created,creator,10,true,false,false}},false)
	})
}

func TestReferencePGAuthMembersVisibility(t *testing.T) {
	pgGroupScenario(t,[]string{"pam_vis_role","pam_vis_member","pam_vis_reader"},func(t *testing.T,p *fixture.Pair,b fixture.Backend) {
		seed := authMembersSeed(t,p,b)
		role := pgGroupCreate(t,p,b,"pam_vis_role","NOLOGIN")
		member := pgGroupCreate(t,p,b,"pam_vis_member","NOLOGIN")
		pgGroupCreate(t,p,b,"pam_vis_reader","LOGIN")
		pgRolesExec(t,p,b,"GRANT pam_vis_role TO pam_vis_member WITH ADMIN FALSE, INHERIT FALSE, SET FALSE")
		edge := authMembersOne(t,p,b,b.DB,role)
		if edge.member != member || edge.admin || edge.inherit || edge.set { t.Fatal(edge) }
		want := append(seed,edge)
		q := "SELECT * FROM pg_catalog.pg_auth_members WHERE member=3373 OR roleid=$1"
		conn, err := b.DB.Conn(p.Ctx)
		if err != nil { t.Fatal(err) }
		defer conn.Close()
		stmt, err := conn.PrepareContext(p.Ctx,q)
		if err != nil { t.Fatal(err) }
		defer stmt.Close()
		defer conn.ExecContext(p.Ctx,"RESET SESSION AUTHORIZATION")
		if _, err := conn.ExecContext(p.Ctx,"SET SESSION AUTHORIZATION pam_vis_reader"); err != nil { t.Fatal(err) }
		authMembersBoth(t,p,b,conn,q,want,role)
		held := fixture.HeldStatement{Executor:conn,Statement:stmt,SQL:q}
		authMembersWant(t,authMembersRead(t,p,b,held,q,role),want,true)
		fixture.RunStep(t,p.Ctx,conn,b.Recorder,fixture.Select("SELECT r.rolname,m.rolname,a.admin_option FROM pg_auth_members a JOIN pg_roles r ON r.oid=a.roleid JOIN pg_roles m ON m.oid=a.member WHERE r.rolname='pam_vis_role'",[]string{"rolname","rolname","admin_option"},[]string{"NAME","NAME","BOOL"},[][]any{{"pam_vis_role","pam_vis_member",false}}))
		for _, prepared := range []bool{false,true} { fixture.RunStep(t,p.Ctx,conn,b.Recorder,pgAuthIDDenied("SELECT rolpassword FROM pg_authid",prepared)) }
	})
}
