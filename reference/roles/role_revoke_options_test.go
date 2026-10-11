package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

// These helpers describe public observations, not ACL implementation details.
func revokeTable(run func(fixture.Step)) {
	run(fixture.Exec("CREATE TABLE t(a INT,b INT)", 0))
	run(fixture.Exec("INSERT INTO t VALUES(7,9)", 1))
}

func revokeActor(run func(fixture.Step), role string, body func()) {
	run(fixture.Exec("SET SESSION AUTHORIZATION "+role, 0))
	body()
	run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
}

func revokeRead(run func(fixture.Step), role string, allowed bool) {
	revokeActor(run, role, func() {
		if allowed {
			run(fixture.Select("SELECT a,b FROM t", []string{"a", "b"}, []string{"INT4", "INT4"}, [][]any{{int64(7), int64(9)}}))
		} else {
			run(fixture.RoleGrantDenied("SELECT a,b FROM t", "t"))
		}
	})
}

func revokeDependency(q string) fixture.Step {
	s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "2BP01", Message: "dependent privileges exist", Hint: "Use CASCADE to revoke them too."})
	s.ErrorStage = "execute"
	return s
}

func revokeNotices(t *testing.T, got, want []fixture.Diagnostic) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("notices got %#v want %#v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("notice %d got %#v want %#v", i, got[i], want[i])
		}
	}
}

func revokeWarning(code, message string) fixture.Diagnostic {
	return fixture.Diagnostic{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: code, Message: message}
}

func TestReferenceRoleRevokeOptionOnly(t *testing.T) {
	fixture.RoleGrantScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), prepared bool) {
		revokeTable(run)
		run(fixture.Exec("GRANT SELECT ON t TO rg_a WITH GRANT OPTION", 0))
		run(fixture.Exec("REVOKE GRANT OPTION FOR SELECT ON t FROM rg_a", 0))
		revokeRead(run, "rg_a", true)
		var notices []fixture.Diagnostic
		_, nc, recorder := fixture.NoticeReferencePool(t, p, b.PG, &notices)
		nrun := func(s fixture.Step) { s.Prepared = prepared; fixture.RunStep(t, p.Ctx, nc, recorder, s) }
		nrun(fixture.Exec("SET SESSION AUTHORIZATION rg_a", 0))
		notices = nil
		stmt, err := nc.PrepareContext(p.Ctx, "GRANT SELECT ON t TO rg_reader")
		if err != nil {
			t.Fatal(err)
		}
		revokeNotices(t, notices, nil)
		stmt.Close()
		nrun(fixture.Exec("GRANT SELECT ON t TO rg_reader", 0))
		revokeNotices(t, notices, []fixture.Diagnostic{revokeWarning("01007", `no privileges were granted for "t"`)})
		nrun(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		revokeRead(run, "rg_reader", false)
		// Plain removal clears use as well; granting again restores delegation.
		run(fixture.Exec("REVOKE SELECT ON t FROM rg_a", 0))
		revokeRead(run, "rg_a", false)
		run(fixture.Exec("GRANT SELECT ON t TO rg_a WITH GRANT OPTION", 0))
		revokeActor(run, "rg_a", func() { run(fixture.Exec("GRANT SELECT ON t TO rg_reader", 0)) })
		revokeRead(run, "rg_reader", true)
	})
}

func TestReferenceRoleRevokePublicOwner(t *testing.T) {
	fixture.RoleGrantScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), prepared bool) {
		revokeActor(run, "rg_owner", func() {
			revokeTable(run)
			run(fixture.Exec("GRANT SELECT ON t TO PUBLIC", 0))
		})
		var notices []fixture.Diagnostic
		_, nc, recorder := fixture.NoticeReferencePool(t, p, b.PG, &notices)
		nrun := func(s fixture.Step) { s.Prepared = prepared; fixture.RunStep(t, p.Ctx, nc, recorder, s) }
		nrun(fixture.Exec("SET SESSION AUTHORIZATION rg_owner", 0))
		for _, q := range []string{
			"REVOKE GRANT OPTION FOR SELECT ON t FROM PUBLIC CASCADE",
			"REVOKE GRANT OPTION FOR ALL ON t FROM PUBLIC RESTRICT",
			"REVOKE GRANT OPTION FOR SELECT ON t FROM CURRENT_USER CASCADE",
			"REVOKE GRANT OPTION FOR SELECT ON t FROM CURRENT_USER CASCADE",
		} {
			notices = nil
			nrun(fixture.Exec(q, 0))
			revokeNotices(t, notices, nil)
		}
		nrun(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		revokeRead(run, "rg_reader", true)
		revokeActor(run, "rg_owner", func() {
			run(fixture.Exec("REVOKE SELECT ON t FROM PUBLIC", 0))
			run(fixture.Exec("GRANT SELECT ON t TO rg_a WITH GRANT OPTION", 0))
		})
		revokeRead(run, "rg_owner", true)
		revokeRead(run, "rg_reader", false)
		revokeActor(run, "rg_a", func() { run(fixture.Exec("GRANT SELECT ON t TO rg_reader", 0)) })
		revokeRead(run, "rg_reader", true)
		// Owner use can be removed, but the implicit ability to grant cannot.
		revokeActor(run, "rg_owner", func() {
			run(fixture.Exec("REVOKE SELECT ON t FROM CURRENT_USER", 0))
			run(fixture.RoleGrantDenied("SELECT a FROM t", "t"))
			run(fixture.Exec("REVOKE GRANT OPTION FOR SELECT ON t FROM CURRENT_USER", 0))
			run(fixture.Exec("GRANT SELECT ON t TO CURRENT_USER", 0))
		})
		revokeRead(run, "rg_owner", true)
	})
}

func TestReferenceRoleRevokeTableLike(t *testing.T) {
	fixture.RoleGrantScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), prepared bool) {
		revokeTable(run)
		run(fixture.Exec("CREATE VIEW v AS SELECT a FROM t", 0))
		run(fixture.Exec("CREATE MATERIALIZED VIEW m AS SELECT 7 AS a", 1))
		run(fixture.Exec("GRANT SELECT ON ALL TABLES IN SCHEMA public TO rg_a WITH GRANT OPTION", 0))
		revokeActor(run, "rg_a", func() { run(fixture.Exec("GRANT SELECT ON t,v,m TO rg_reader", 0)) })
		run(revokeDependency("REVOKE GRANT OPTION FOR SELECT ON ALL TABLES IN SCHEMA public FROM rg_a RESTRICT"))
		run(fixture.Exec("REVOKE GRANT OPTION FOR SELECT ON ALL TABLES IN SCHEMA public FROM rg_a CASCADE", 0))
		for _, relation := range []struct{ name, kind string }{{"t", "table"}, {"v", "view"}, {"m", "materialized view"}} {
			revokeActor(run, "rg_a", func() {
				run(fixture.Select("SELECT a FROM "+relation.name, []string{"a"}, []string{"INT4"}, [][]any{{int64(7)}}))
			})
			revokeActor(run, "rg_reader", func() {
				run(fixture.ImplicitError("SELECT a FROM "+relation.name, fixture.Diagnostic{Code: "42501", Message: "permission denied for " + relation.kind + " " + relation.name}))
			})
		}
		// Every selected relation bit is admitted without inventing consumers.
		run(fixture.Exec("CREATE INDEX ix ON t(a)", 0))
		run(fixture.Exec("GRANT SELECT,INSERT,UPDATE,DELETE,TRUNCATE,REFERENCES,TRIGGER,MAINTAIN ON t TO rg_b WITH GRANT OPTION", 0))
		run(fixture.Exec("REVOKE GRANT OPTION FOR SELECT,INSERT,UPDATE,DELETE,TRUNCATE,REFERENCES,TRIGGER,MAINTAIN ON TABLE t FROM rg_b CASCADE", 0))
		revokeActor(run, "rg_b", func() {
			run(fixture.Exec("UPDATE t SET b=9", 1))
			run(fixture.Exec("INSERT INTO t VALUES(8,10)", 1))
			run(fixture.Exec("DELETE FROM t WHERE a=8", 1))
			run(fixture.Exec("REINDEX INDEX ix", 0))
			run(fixture.Exec("BEGIN", 0))
			run(fixture.Exec("LOCK TABLE t IN ACCESS EXCLUSIVE MODE NOWAIT", 0))
			run(fixture.Exec("ROLLBACK", 0))
		})
		run(fixture.Exec("REVOKE ALL PRIVILEGES ON t FROM rg_b CASCADE", 0))
		revokeActor(run, "rg_b", func() {
			run(fixture.RoleGrantDenied("UPDATE t SET b=9", "t"))
			run(fixture.RoleGrantDenied("INSERT INTO t VALUES(8,10)", "t"))
			run(fixture.RoleGrantDenied("DELETE FROM t WHERE a=8", "t"))
			run(fixture.ImplicitError("REINDEX INDEX ix", fixture.Diagnostic{Code: "42501", Message: "permission denied for index ix"}))
			run(fixture.Exec("BEGIN", 0))
			run(fixture.RoleGrantDenied("LOCK TABLE t IN ACCESS EXCLUSIVE MODE NOWAIT", "t"))
			run(fixture.Exec("ROLLBACK", 0))
		})
	})
}
