package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferencePGAuthMembersLifecycle(t *testing.T) {
	pgGroupScenario(t, []string{"pam_life_role", "pam_life_renamed", "pam_life_member"}, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		role := pgGroupCreate(t, p, b, "pam_life_role", "NOLOGIN")
		member := pgGroupCreate(t, p, b, "pam_life_member", "LOGIN")
		q := "SELECT * FROM pg_auth_members WHERE roleid=$1"
		stmt, err := b.DB.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: b.DB, Statement: stmt, SQL: q}
		check := func(want []authMemberRow) {
			authMembersBoth(t, p, b, b.DB, q, want, role)
			authMembersWant(t, authMembersRead(t, p, b, held, q, role), want, true)
		}
		check(nil)
		pgRolesExec(t, p, b, "GRANT pam_life_role TO pam_life_member")
		first := authMembersOne(t, p, b, b.DB, role)
		if first.member != member || first.admin || !first.inherit || !first.set {
			t.Fatal(first)
		}
		check([]authMemberRow{first})
		var retained *sql.Rows
		if !b.PG {
			retained, err = stmt.QueryContext(p.Ctx, role)
			if err != nil {
				t.Fatal(err)
			}
			defer retained.Close()
		}
		revoked := first
		pgRolesExec(t, p, b, "REVOKE INHERIT OPTION FOR pam_life_role FROM pam_life_member")
		revoked.inherit = false
		check([]authMemberRow{revoked})
		pgRolesExec(t, p, b, "REVOKE SET OPTION FOR pam_life_role FROM pam_life_member")
		revoked.set = false
		check([]authMemberRow{revoked})
		pgRolesExec(t, p, b, "GRANT pam_life_role TO pam_life_member WITH ADMIN TRUE, INHERIT FALSE, SET FALSE")
		updated := first
		updated.admin, updated.inherit, updated.set = true, false, false
		check([]authMemberRow{updated})
		pgRolesExec(t, p, b, "GRANT pam_life_role TO pam_life_member WITH ADMIN TRUE, INHERIT FALSE, SET FALSE")
		check([]authMemberRow{updated})
		pgRolesExec(t, p, b, "REVOKE ADMIN OPTION FOR pam_life_role FROM pam_life_member")
		updated.admin = false
		check([]authMemberRow{updated})
		pgRolesExec(t, p, b, "ALTER ROLE pam_life_role RENAME TO pam_life_renamed")
		roleName := "pam_life_renamed"
		check([]authMemberRow{updated})
		pgRolesBoth(t, p, b, fixture.Select("SELECT r.rolname FROM pg_auth_members a JOIN pg_roles r ON r.oid=a.roleid WHERE a.oid=$1", []string{"rolname"}, []string{"NAME"}, [][]any{{roleName}}, first.oid))
		conn, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		defer conn.ExecContext(p.Ctx, "ROLLBACK")
		exec := func(q string) {
			if _, err := conn.ExecContext(p.Ctx, q); err != nil {
				t.Fatal(err)
			}
		}
		exec("BEGIN")
		exec("SAVEPOINT pam_save")
		exec("GRANT " + roleName + " TO pam_life_member WITH SET TRUE")
		temporary := updated
		temporary.set = true
		authMembersWant(t, []authMemberRow{authMembersOne(t, p, b, conn, role)}, []authMemberRow{temporary}, true)
		check([]authMemberRow{updated})
		exec("ROLLBACK TO pam_save")
		authMembersWant(t, []authMemberRow{authMembersOne(t, p, b, conn, role)}, []authMemberRow{updated}, true)
		exec("REVOKE " + roleName + " FROM pam_life_member")
		exec("GRANT " + roleName + " TO pam_life_member")
		abandoned := authMembersOne(t, p, b, conn, role)
		if abandoned.oid == first.oid {
			t.Fatal("regrant reused identity")
		}
		exec("ROLLBACK TO pam_save")
		authMembersWant(t, []authMemberRow{authMembersOne(t, p, b, conn, role)}, []authMemberRow{updated}, true)
		exec("ROLLBACK")
		check([]authMemberRow{updated})
		pgRolesExec(t, p, b, "REVOKE "+roleName+" FROM pam_life_member")
		check(nil)
		exec("BEGIN")
		exec("GRANT " + roleName + " TO pam_life_member")
		committed := authMembersOne(t, p, b, conn, role)
		if committed.oid == first.oid || committed.oid == abandoned.oid {
			t.Fatal("reservation reused", committed)
		}
		check(nil)
		exec("COMMIT")
		check([]authMemberRow{committed})
		if retained != nil {
			authMembersWant(t, authMembersConsume(t, retained, false), []authMemberRow{first}, true)
		}
		pgRolesExec(t, p, b, "DROP ROLE "+roleName)
		check(nil)
		replacement := pgGroupCreate(t, p, b, roleName, "NOLOGIN")
		if replacement == role {
			t.Fatal("principal identity reused")
		}
		pgRolesExec(t, p, b, "GRANT "+roleName+" TO pam_life_member")
		recreated := authMembersOne(t, p, b, b.DB, replacement)
		if recreated.oid == committed.oid || recreated.oid == first.oid {
			t.Fatal("dropped membership identity reused")
		}
		check(nil)
	})
}

func TestReferencePGAuthMembersConcurrentIdentity(t *testing.T) {
	pgGroupScenario(t, []string{"pam_con_a", "pam_con_b", "pam_con_c", "pam_con_d"}, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		a := pgGroupCreate(t, p, b, "pam_con_a", "NOLOGIN")
		bb := pgGroupCreate(t, p, b, "pam_con_b", "NOLOGIN")
		c := pgGroupCreate(t, p, b, "pam_con_c", "NOLOGIN")
		d := pgGroupCreate(t, p, b, "pam_con_d", "NOLOGIN")
		left, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer left.Close()
		right, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer right.Close()
		defer left.ExecContext(p.Ctx, "ROLLBACK")
		defer right.ExecContext(p.Ctx, "ROLLBACK")
		for _, conn := range []*sql.Conn{left, right} {
			if _, err := conn.ExecContext(p.Ctx, "BEGIN"); err != nil {
				t.Fatal(err)
			}
		}
		// Both independent accepted grants precede either commit: no timing sleeps.
		if _, err := left.ExecContext(p.Ctx, "GRANT pam_con_a TO pam_con_b"); err != nil {
			t.Fatal(err)
		}
		if _, err := right.ExecContext(p.Ctx, "GRANT pam_con_c TO pam_con_d"); err != nil {
			t.Fatal(err)
		}
		l, r := authMembersOne(t, p, b, left, a), authMembersOne(t, p, b, right, c)
		if l.oid == r.oid || l.member != bb || r.member != d {
			t.Fatal("private identities", l, r)
		}
		authMembersBoth(t, p, b, b.DB, "SELECT * FROM pg_auth_members WHERE roleid=$1 OR roleid=$2", nil, a, c)
		for _, conn := range []*sql.Conn{left, right} {
			if _, err := conn.ExecContext(p.Ctx, "COMMIT"); err != nil {
				t.Fatal(err)
			}
		}
		authMembersBoth(t, p, b, b.DB, "SELECT * FROM pg_auth_members WHERE roleid=$1 OR roleid=$2", []authMemberRow{l, r}, a, c)
	})
}
