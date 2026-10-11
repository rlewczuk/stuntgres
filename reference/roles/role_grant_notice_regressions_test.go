package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

// Notice expectations are independently supplied by each scenario; the same
// public direct/prepared command and consumer body runs on both backends.
func roleGrantNoticeExec(t *testing.T, p *fixture.Pair, conn *sql.Conn, recorder *fixture.Recorder, prepared bool, notices *[]fixture.Diagnostic, query string, want ...fixture.Diagnostic) {
	t.Helper()
	*notices = nil
	stmt, err := conn.PrepareContext(p.Ctx, query)
	if err != nil {
		t.Fatal(query, "Prepare", err)
	}
	if len(*notices) != 0 {
		t.Fatalf("%s Prepare notices %#v", query, *notices)
	}
	if err := stmt.Close(); err != nil {
		t.Fatal("close quiet description", err)
	}
	step := fixture.Exec(query, 0)
	step.Prepared = prepared
	fixture.RunStep(t, p.Ctx, conn, recorder, step)
	if len(*notices) != len(want) {
		t.Fatalf("%s notices %#v want %#v", query, *notices, want)
	}
	for i := range want {
		if (*notices)[i] != want[i] {
			t.Fatalf("%s notice %d got %#v want %#v", query, i, (*notices)[i], want[i])
		}
	}
}

func TestReferenceRoleGrantColumnNoticeOrder(t *testing.T) {
	fixture.RoleGrantScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), prepared bool) {
		run(fixture.Exec("CREATE TABLE t(a INT,gap INT,b INT,c INT)", 0))
		run(fixture.Exec("INSERT INTO t VALUES(7,0,9,11)", 1))
		run(fixture.Exec("ALTER TABLE t DROP COLUMN gap", 0))
		run(fixture.Exec("ALTER TABLE t RENAME COLUMN b TO z", 0))
		run(fixture.Exec("GRANT SELECT ON t TO rg_b", 0))
		run(fixture.Exec("GRANT SELECT ON t TO rg_a WITH GRANT OPTION", 0))
		var notices []fixture.Diagnostic
		_, nc, recorder := fixture.NoticeReferencePool(t, p, b.PG, &notices)
		nrun := func(step fixture.Step) { step.Prepared = prepared; fixture.RunStep(t, p.Ctx, nc, recorder, step) }
		requests := "SELECT(c,z,a,c),INSERT(z,a,z)"
		// Stable live attributes are 1,3,4. Neither reversed textual order c,z,a
		// nor lexical name order a,c,z is the release's diagnostic traversal.
		for _, command := range []string{"GRANT", "REVOKE"} {
			nrun(fixture.Exec("SET SESSION AUTHORIZATION rg_b", 0))
			query, code, message := "GRANT "+requests+" ON t TO rg_reader", "01007", "no privileges were granted"
			if command == "REVOKE" {
				query, code, message = "REVOKE "+requests+" ON t FROM rg_reader", "01006", "no privileges could be revoked"
			}
			want := []fixture.Diagnostic{
				{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: code, Message: message + ` for column "a" of relation "t"`},
				{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: code, Message: message + ` for column "z" of relation "t"`},
				{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: code, Message: message + ` for column "c" of relation "t"`},
			}
			roleGrantNoticeExec(t, p, nc, recorder, prepared, &notices, query, want...)
			nrun(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
			run(fixture.Exec("SET SESSION AUTHORIZATION rg_reader", 0))
			for _, query := range []string{"SELECT a FROM t", "SELECT z FROM t", "SELECT c FROM t", "INSERT INTO t(a) VALUES(100)", "UPDATE t SET z=100"} {
				run(fixture.RoleGrantDenied(query, "t"))
			}
			run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		}
		// Partial options install only SELECT. Duplicate/mixed requests are merged
		// before traversal: c is fully eligible, while a and z each warn once.
		nrun(fixture.Exec("SET SESSION AUTHORIZATION rg_a", 0))
		roleGrantNoticeExec(t, p, nc, recorder, prepared, &notices, "GRANT "+requests+" ON t TO rg_reader",
			fixture.Diagnostic{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: "01007", Message: `not all privileges were granted for column "a" of relation "t"`},
			fixture.Diagnostic{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: "01007", Message: `not all privileges were granted for column "z" of relation "t"`})
		nrun(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		run(fixture.Exec("SET SESSION AUTHORIZATION rg_reader", 0))
		run(fixture.Select("SELECT a,z,c FROM t", []string{"a", "z", "c"}, []string{"INT4", "INT4", "INT4"}, [][]any{{int64(7), int64(9), int64(11)}}))
		run(fixture.RoleGrantDenied("INSERT INTO t(a) VALUES(100)", "t"))
		run(fixture.RoleGrantDenied("UPDATE t SET z=100", "t"))
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		nrun(fixture.Exec("SET SESSION AUTHORIZATION rg_a", 0))
		roleGrantNoticeExec(t, p, nc, recorder, prepared, &notices, "REVOKE "+requests+" ON t FROM rg_reader",
			fixture.Diagnostic{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: "01006", Message: `not all privileges could be revoked for column "a" of relation "t"`},
			fixture.Diagnostic{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: "01006", Message: `not all privileges could be revoked for column "z" of relation "t"`})
		nrun(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		run(fixture.Exec("SET SESSION AUTHORIZATION rg_reader", 0))
		for _, query := range []string{"SELECT a FROM t", "SELECT z FROM t", "SELECT c FROM t"} {
			run(fixture.RoleGrantDenied(query, "t"))
		}
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		run(fixture.Select("SELECT a,z,c FROM t", []string{"a", "z", "c"}, []string{"INT4", "INT4", "INT4"}, [][]any{{int64(7), int64(9), int64(11)}}))
	})
}

func TestReferenceRoleGrantColumnFullMaskNotices(t *testing.T) {
	fixture.RoleGrantScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), prepared bool) {
		run(fixture.Exec("CREATE TABLE t(a INT,b INT)", 0))
		run(fixture.Exec("INSERT INTO t VALUES(7,9)", 1))
		run(fixture.Exec("GRANT SELECT ON t TO rg_a WITH GRANT OPTION", 0))
		run(fixture.Exec("GRANT SELECT ON t TO rg_b", 0))
		var notices []fixture.Diagnostic
		_, nc, recorder := fixture.NoticeReferencePool(t, p, b.PG, &notices)
		nrun := func(step fixture.Step) { step.Prepared = prepared; fixture.RunStep(t, p.Ctx, nc, recorder, step) }
		// Both spellings merge to SELECT|INSERT|UPDATE|REFERENCES on a, including
		// duplicates. Release column partial-warning suppression uses that mask,
		// not the presence of an ALL syntax token.
		fullMasks := []string{"SELECT(a),INSERT(a,a),UPDATE(a),REFERENCES(a),SELECT(a)", "ALL(a,a)"}
		for _, request := range fullMasks {
			nrun(fixture.Exec("SET SESSION AUTHORIZATION rg_a", 0))
			roleGrantNoticeExec(t, p, nc, recorder, prepared, &notices, "GRANT "+request+" ON t TO rg_reader")
			nrun(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
			run(fixture.Exec("SET SESSION AUTHORIZATION rg_reader", 0))
			run(fixture.Select("SELECT a FROM t", []string{"a"}, []string{"INT4"}, [][]any{{int64(7)}}))
			for _, query := range []string{"SELECT b FROM t", "SELECT * FROM t", "INSERT INTO t(a) VALUES(100)", "UPDATE t SET a=100"} {
				run(fixture.RoleGrantDenied(query, "t"))
			}
			run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
			nrun(fixture.Exec("SET SESSION AUTHORIZATION rg_a", 0))
			roleGrantNoticeExec(t, p, nc, recorder, prepared, &notices, "REVOKE "+request+" ON t FROM rg_reader")
			nrun(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
			run(fixture.Exec("SET SESSION AUTHORIZATION rg_reader", 0))
			run(fixture.RoleGrantDenied("SELECT a FROM t", "t"))
			run(fixture.RoleGrantDenied("SELECT b FROM t", "t"))
			run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		}
		// An incomplete merged mask must still produce partial-authority warnings.
		nrun(fixture.Exec("SET SESSION AUTHORIZATION rg_a", 0))
		roleGrantNoticeExec(t, p, nc, recorder, prepared, &notices, "GRANT SELECT(a,a),INSERT(a) ON t TO rg_reader",
			fixture.Diagnostic{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: "01007", Message: `not all privileges were granted for column "a" of relation "t"`})
		nrun(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		run(fixture.Exec("SET SESSION AUTHORIZATION rg_reader", 0))
		run(fixture.Select("SELECT a FROM t", []string{"a"}, []string{"INT4"}, [][]any{{int64(7)}}))
		run(fixture.RoleGrantDenied("INSERT INTO t(a) VALUES(100)", "t"))
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		nrun(fixture.Exec("SET SESSION AUTHORIZATION rg_a", 0))
		roleGrantNoticeExec(t, p, nc, recorder, prepared, &notices, "REVOKE SELECT(a,a),INSERT(a) ON t FROM rg_reader",
			fixture.Diagnostic{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: "01006", Message: `not all privileges could be revoked for column "a" of relation "t"`})
		nrun(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		// Full masks suppress partial warnings only: zero eligible options still
		// warn, for both enumerated and ALL syntax, and confer no consumer rights.
		for _, request := range fullMasks {
			nrun(fixture.Exec("SET SESSION AUTHORIZATION rg_b", 0))
			roleGrantNoticeExec(t, p, nc, recorder, prepared, &notices, "GRANT "+request+" ON t TO rg_reader",
				fixture.Diagnostic{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: "01007", Message: `no privileges were granted for column "a" of relation "t"`})
			roleGrantNoticeExec(t, p, nc, recorder, prepared, &notices, "REVOKE "+request+" ON t FROM rg_reader",
				fixture.Diagnostic{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: "01006", Message: `no privileges could be revoked for column "a" of relation "t"`})
			nrun(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
			run(fixture.Exec("SET SESSION AUTHORIZATION rg_reader", 0))
			for _, query := range []string{"SELECT a FROM t", "SELECT b FROM t", "INSERT INTO t(a) VALUES(100)", "UPDATE t SET a=100"} {
				run(fixture.RoleGrantDenied(query, "t"))
			}
			run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		}
		run(fixture.Select("SELECT a,b FROM t", []string{"a", "b"}, []string{"INT4", "INT4"}, [][]any{{int64(7), int64(9)}}))
	})
}
