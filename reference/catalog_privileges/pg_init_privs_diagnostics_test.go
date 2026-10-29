package reference_test

import (
	"context"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferencePGInitPrivsDiagnostics(t *testing.T) {
	ipScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		for _, tc := range []struct{ q, code, message, position string }{
			{"SELECT oid FROM pg_catalog.pg_init_privs", "42703", `column "oid" does not exist`, "8"},
			{"SELECT /*é*/ oid FROM pg_catalog.pg_init_privs", "42703", `column "oid" does not exist`, "14"},
			{"SELECT * FROM pg_catalog.pg_init_privss", "42P01", `relation "pg_catalog.pg_init_privss" does not exist`, "15"},
			{"SELECT 'x'::oid FROM pg_init_privs WHERE FALSE", "22P02", `invalid input syntax for type oid: "x"`, "8"},
			{"SELECT '4294967296'::oid FROM pg_init_privs WHERE FALSE", "22003", `value "4294967296" is out of range for type oid`, "8"},
		} {
			for _, prepared := range []bool{false, true} {
				for _, plain := range []bool{false, true} {
					s := fixture.ImplicitError(tc.q, fixture.Diagnostic{Code: tc.code, Message: tc.message, Position: tc.position})
					s.SelectRows = true
					s.Prepared = prepared
					s.NonContext = plain
					s.ErrorStage = "execute"
					if prepared {
						s.ErrorStage = "prepare"
					}
					fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
				}
			}
		}
		q := "SELECT * FROM pg_init_privs WHERE objoid=$1::oid"
		held := fixture.ViewHold(t, p, b, q)
		for _, tc := range []struct{ value, code, message string }{{"x", "22P02", `invalid input syntax for type oid: "x"`}, {"4294967296", "22003", `value "4294967296" is out of range for type oid`}} {
			s := fixture.ImplicitError(q, fixture.Diagnostic{Code: tc.code, Message: tc.message, Context: "unnamed portal parameter $1 = '...'"}, tc.value)
			s.SelectRows = true
			s.ErrorStage = "execute"
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Prepared(s))
			fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
		}
		ctx, cancel := context.WithCancel(p.Ctx)
		cancel()
		rows, err := b.DB.QueryContext(ctx, q, int64(0))
		if rows != nil {
			rows.Close()
			t.Fatal("canceled query published rows")
		}
		pgDBRoleSettingCanceled(t, err)
		stmt, err := b.DB.PrepareContext(ctx, q)
		if stmt != nil {
			stmt.Close()
			t.Fatal("canceled prepare published statement")
		}
		pgDBRoleSettingCanceled(t, err)
	})
}

func TestReferencePGInitPrivsAuthorization(t *testing.T) {
	ipScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		oid, owner, reader := ipMemberSetup(t, p, b)
		ipExec(t, p, b, b.DB, "ALTER EXTENSION pageinspect ADD TABLE ip_member", "CREATE SCHEMA ip_hidden", "ALTER TABLE ip_member SET SCHEMA ip_hidden")
		conn, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		defer func() {
			for _, q := range []string{"RESET ROLE", "REVOKE SELECT(objoid) ON pg_catalog.pg_init_privs FROM PUBLIC", "GRANT SELECT ON pg_catalog.pg_init_privs TO PUBLIC"} {
				if _, err := conn.ExecContext(p.Ctx, q); err != nil {
					t.Error(err)
				}
			}
		}()
		const q = "SELECT * FROM pg_catalog.pg_init_privs WHERE FALSE"
		stmt, err := conn.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: conn, Statement: stmt, SQL: q}
		ipExec(t, p, b, conn, "SET ROLE ip_other")
		ipTableACL(t, p, b, conn, oid, owner, reader, "SELECT")
		denied := fixture.ImplicitError("SELECT * FROM ip_hidden.ip_member", fixture.Diagnostic{Code: "42501", Message: "permission denied for schema ip_hidden", Position: "15"})
		denied.SelectRows = true
		fixture.RunStep(t, p.Ctx, conn, b.Recorder, denied)
		unauth := fixture.ImplicitError("ALTER EXTENSION pageinspect ADD TABLE ip_hidden.ip_member", fixture.Diagnostic{Code: "42501", Message: "must be owner of extension pageinspect"})
		fixture.RunStep(t, p.Ctx, conn, b.Recorder, unauth)
		ipTableACL(t, p, b, conn, oid, owner, reader, "SELECT")
		ipExec(t, p, b, conn, "RESET ROLE", "REVOKE SELECT ON pg_catalog.pg_init_privs FROM PUBLIC", "SET ROLE ip_other")
		deny := func(query string) fixture.Step {
			s := fixture.ImplicitError(query, fixture.Diagnostic{Code: "42501", Message: "permission denied for table pg_init_privs"})
			s.SelectRows = true
			s.ErrorStage = "execute"
			return s
		}
		fixture.RunStep(t, p.Ctx, conn, b.Recorder, deny(q))
		fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Prepared(deny(q)))
		fixture.RunStep(t, p.Ctx, held, b.Recorder, deny(q))
		fixture.RunStep(t, p.Ctx, conn, b.Recorder, deny("SELECT * FROM pg_init_privs"))
		ipExec(t, p, b, conn, "RESET ROLE", "GRANT SELECT(objoid) ON pg_catalog.pg_init_privs TO PUBLIC", "SET ROLE ip_other")
		fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Select("SELECT objoid FROM pg_init_privs WHERE FALSE", []string{"objoid"}, []string{"OID"}, nil))
		fixture.RunStep(t, p.Ctx, conn, b.Recorder, deny(q))
		fixture.RunStep(t, p.Ctx, conn, b.Recorder, deny("SELECT initprivs FROM pg_init_privs WHERE FALSE"))
		ipExec(t, p, b, conn, "RESET ROLE", "GRANT SELECT ON pg_catalog.pg_init_privs TO PUBLIC", "SET ROLE ip_other")
		fixture.RunStep(t, p.Ctx, held, b.Recorder, ipShapeStep(q, b.PG))
		ipACL(t, p, b, conn, 1259, 3394, 0, "i", ipGrant{owner, owner, ipTableRights, false}, ipGrant{0, owner, []string{"SELECT"}, false})
	})
}
