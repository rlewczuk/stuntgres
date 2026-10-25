package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferencePGGroupMembership(t *testing.T) {
	pgGroupScenario(t, []string{"pgg_parent", "pgg_middle", "pgg_empty", "pgg_leaf", "pgg_creator", "pgg_created", "pgg_user", "pgg_user_override", "pgg_role_override", "pgg_group_default"}, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		parent := pgGroupCreate(t, p, b, "pgg_parent", "NOLOGIN")
		middle := pgGroupCreate(t, p, b, "pgg_middle", "NOLOGIN")
		empty := pgGroupCreate(t, p, b, "pgg_empty", "")
		leaf := pgGroupCreate(t, p, b, "pgg_leaf", "LOGIN")
		for _, q := range []string{"GRANT pgg_parent TO pgg_middle WITH ADMIN FALSE, INHERIT FALSE, SET FALSE", "GRANT pgg_middle TO pgg_leaf"} {
			pgRolesExec(t, p, b, q)
		}
		check := func(members ...int64) {
			pgGroupBoth(t, p, b, "SELECT * FROM pg_group WHERE "+fixture.PGTableViewsNames("groname", []string{"pgg_parent", "pgg_middle", "pgg_empty", "pgg_leaf"}), [][]any{pgGroupRow("pgg_parent", parent, members...), pgGroupRow("pgg_middle", middle, leaf), pgGroupRow("pgg_empty", empty)})
		}
		check(middle)
		for _, q := range []string{"ALTER ROLE pgg_parent SUPERUSER CREATEDB CREATEROLE REPLICATION BYPASSRLS NOINHERIT CONNECTION LIMIT 0 VALID UNTIL '-infinity'", "GRANT pgg_parent TO pgg_middle WITH ADMIN TRUE, INHERIT TRUE, SET TRUE", "REVOKE ADMIN OPTION FOR pgg_parent FROM pgg_middle", "GRANT pgg_parent TO pgg_middle WITH INHERIT FALSE, SET FALSE"} {
			pgRolesExec(t, p, b, q)
			check(middle)
		}
		pgRolesExec(t, p, b, "GRANT pgg_parent TO pgg_leaf WITH ADMIN FALSE, INHERIT FALSE, SET FALSE")
		check(middle, leaf)
		pgGroupBoth(t, p, b, "SELECT * FROM pg_group WHERE groname='public'", nil)
		for _, q := range []string{"CREATE USER pgg_user", "CREATE USER pgg_user_override NOLOGIN", "CREATE ROLE pgg_role_override LOGIN", "CREATE GROUP pgg_group_default"} {
			pgRolesExec(t, p, b, q)
		}
		var userOverride, groupDefault int64
		if err := b.DB.QueryRowContext(p.Ctx, "SELECT oid FROM pg_roles WHERE rolname='pgg_user_override'").Scan(&userOverride); err != nil {
			t.Fatal(err)
		}
		if err := b.DB.QueryRowContext(p.Ctx, "SELECT oid FROM pg_roles WHERE rolname='pgg_group_default'").Scan(&groupDefault); err != nil {
			t.Fatal(err)
		}
		pgGroupBoth(t, p, b, "SELECT * FROM pg_group WHERE "+fixture.PGTableViewsNames("groname", []string{"pgg_user", "pgg_user_override", "pgg_role_override", "pgg_group_default"}), [][]any{pgGroupRow("pgg_user_override", userOverride), pgGroupRow("pgg_group_default", groupDefault)})
		creator := pgGroupCreate(t, p, b, "pgg_creator", "LOGIN CREATEROLE")
		conn, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		defer conn.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION")
		for _, q := range []string{"SET SESSION AUTHORIZATION pgg_creator", "CREATE ROLE pgg_created NOLOGIN", "RESET SESSION AUTHORIZATION"} {
			fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Exec(q, 0))
		}
		var created int64
		if err := conn.QueryRowContext(p.Ctx, "SELECT oid FROM pg_roles WHERE rolname='pgg_created'").Scan(&created); err != nil {
			t.Fatal(err)
		}
		pgGroupBoth(t, p, b, "SELECT * FROM pg_group WHERE groname='pgg_created'", [][]any{pgGroupRow("pgg_created", created, creator)})
	})
}

func TestReferencePGGroupGrantorMultiplicity(t *testing.T) {
	pgGroupScenario(t, []string{"pgg_multi", "pgg_admin", "pgg_member"}, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		group := pgGroupCreate(t, p, b, "pgg_multi", "NOLOGIN")
		admin := pgGroupCreate(t, p, b, "pgg_admin", "LOGIN")
		member := pgGroupCreate(t, p, b, "pgg_member", "LOGIN")
		pgRolesExec(t, p, b, "GRANT pgg_multi TO pgg_admin WITH ADMIN TRUE, INHERIT FALSE, SET FALSE")
		conn, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		defer conn.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION")
		for _, q := range []string{"SET SESSION AUTHORIZATION pgg_admin", "GRANT pgg_multi TO pgg_member", "RESET SESSION AUTHORIZATION", "GRANT pgg_multi TO pgg_member WITH INHERIT FALSE, SET FALSE"} {
			fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Exec(q, 0))
		}
		pgGroupBoth(t, p, b, "SELECT * FROM pg_group WHERE groname='pgg_multi'", [][]any{pgGroupRow("pgg_multi", group, admin, member, member)})
		pgRolesExec(t, p, b, "REVOKE pgg_multi FROM pgg_member GRANTED BY pgg_admin")
		pgGroupBoth(t, p, b, "SELECT * FROM pg_group WHERE groname='pgg_multi'", [][]any{pgGroupRow("pgg_multi", group, admin, member)})
		pgRolesExec(t, p, b, "REVOKE pgg_multi FROM pgg_member")
		pgGroupBoth(t, p, b, "SELECT * FROM pg_group WHERE groname='pgg_multi'", [][]any{pgGroupRow("pgg_multi", group, admin)})
	})
}
