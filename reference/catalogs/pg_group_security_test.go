package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferencePGGroupVisibility(t *testing.T) {
	pgGroupScenario(t, []string{"pgg_visible", "pgg_privileged", "pgg_reader", "pgg_login_member", "pgg_nologin_member"}, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		group := pgGroupCreate(t, p, b, "pgg_visible", "NOLOGIN")
		priv := pgGroupCreate(t, p, b, "pgg_privileged", "NOLOGIN SUPERUSER CREATEROLE CREATEDB REPLICATION BYPASSRLS CONNECTION LIMIT 0 VALID UNTIL '-infinity'")
		pgGroupCreate(t, p, b, "pgg_reader", "LOGIN")
		login := pgGroupCreate(t, p, b, "pgg_login_member", "LOGIN")
		nologin := pgGroupCreate(t, p, b, "pgg_nologin_member", "NOLOGIN")
		for _, q := range []string{"GRANT pgg_visible TO pgg_login_member WITH ADMIN FALSE, INHERIT FALSE, SET FALSE", "GRANT pgg_privileged TO pgg_nologin_member WITH ADMIN FALSE, INHERIT FALSE, SET FALSE"} {
			pgRolesExec(t, p, b, q)
		}
		// Never record credential setup or print its SQL on failure.
		if _, err := b.DB.ExecContext(p.Ctx, "ALTER ROLE pgg_visible PASSWORD 'group-private-credential' VALID UNTIL '-infinity'"); err != nil {
			t.Fatal("credential setup failed")
		}
		q := "SELECT * FROM pg_catalog.pg_group WHERE "+pgTableViewsNames("groname", []string{"pgg_visible", "pgg_privileged", "pgg_reader", "pgg_login_member", "pgg_nologin_member", "public"})
		want := [][]any{pgGroupRow("pgg_visible", group, login), pgGroupRow("pgg_privileged", priv, nologin), pgGroupRow("pgg_nologin_member", nologin)}
		pgGroupBoth(t, p, b, q, want)
		conn, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		defer conn.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION")
		fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Exec("SET SESSION AUTHORIZATION pgg_reader", 0))
		for _, prepared := range []bool{false, true} {
			pgGroupQuery(t, p, b, conn, q, prepared, want)
		}
		for _, name := range []string{"rolpassword", "passwd", "rolvaliduntil", "rolcanlogin"} {
			for _, prepared := range []bool{false, true} {
				s := fixture.ImplicitError("SELECT "+name+" FROM pg_catalog.pg_group", fixture.Diagnostic{Code: "42703", Message: "column \"" + name + "\" does not exist", Position: "8"})
				s.SelectRows, s.Prepared, s.ErrorStage = true, prepared, "execute"
				if prepared {
					s.ErrorStage = "prepare"
				}
				fixture.RunStep(t, p.Ctx, conn, b.Recorder, s)
			}
		}
		pgGroupQuery(t, p, b, conn, q, false, want)
		fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Exec("RESET SESSION AUTHORIZATION", 0))
	})
}

func TestReferencePGGroupFeatureSmoke(t *testing.T) {
	pgGroupScenario(t, []string{"pgg_smoke", "pgg_smoke_member", "pgg_smoke_reader"}, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		id := pgGroupCreate(t, p, b, "pgg_smoke", "NOLOGIN")
		member := pgGroupCreate(t, p, b, "pgg_smoke_member", "LOGIN")
		pgGroupCreate(t, p, b, "pgg_smoke_reader", "LOGIN")
		conn, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		defer conn.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION")
		fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Exec("SET SESSION AUTHORIZATION pgg_smoke_reader", 0))
		q := "SELECT * FROM pg_catalog.pg_group WHERE groname=$1"
		stmt, err := conn.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: conn, Statement: stmt, SQL: q}
		check := func(present bool, members ...int64) {
			var want [][]any
			if present {
				want = [][]any{pgGroupRow("pgg_smoke", id, members...)}
			}
			pgGroupBoth(t, p, b, q, want, "pgg_smoke")
			pgGroupQuery(t, p, b, held, q, false, want, "pgg_smoke")
		}
		check(true)
		pgRolesExec(t, p, b, "GRANT pgg_smoke TO pgg_smoke_member WITH ADMIN FALSE, INHERIT FALSE, SET FALSE")
		check(true, member)
		pgRolesExec(t, p, b, "ALTER ROLE pgg_smoke LOGIN")
		check(false)
		pgRolesExec(t, p, b, "ALTER ROLE pgg_smoke NOLOGIN")
		check(true, member)
		s := fixture.ImplicitError("SELECT rolpassword FROM pg_catalog.pg_group", fixture.Diagnostic{Code: "42703", Message: `column "rolpassword" does not exist`, Position: "8"})
		s.SelectRows, s.ErrorStage = true, "execute"
		fixture.RunStep(t, p.Ctx, conn, b.Recorder, s)
		pgRolesExec(t, p, b, "REVOKE pgg_smoke FROM pgg_smoke_member")
		check(true)
		fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Exec("RESET SESSION AUTHORIZATION", 0))
	})
}
