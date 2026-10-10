package reference_test

import (
	"database/sql"
	"testing"
)

func TestReferenceRoleGrantDiagnostics(t *testing.T) {
	roleGrantScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn, run func(referenceStep), prepared bool) {
		run(referenceExec("CREATE TABLE t(a INT)", 0))
		run(referenceExec("CREATE INDEX ix ON t(a)", 0))
		run(referenceExec("CREATE TYPE ct AS(a INT)", 0))
		cases := []struct{ q, code, message string }{
			{"GRANT SELECT ON t TO missing_role", "42704", `role "missing_role" does not exist`},
			{"GRANT SELECT ON missing_table TO rg_reader", "42P01", `relation "missing_table" does not exist`},
			{"GRANT SELECT(missing_column) ON t TO rg_reader", "42703", `column "missing_column" of relation "t" does not exist`},
			{"GRANT ALL ON ALL TABLES IN SCHEMA missing_schema TO rg_reader", "3F000", `schema "missing_schema" does not exist`},
			{"GRANT SELECT ON ix TO rg_reader", "42809", `"ix" is an index`},
			{"GRANT SELECT ON ct TO rg_reader", "42809", `"ct" is a composite type`},
			{"GRANT CREATE ON t TO rg_reader", "0LP01", "invalid privilege type CREATE for relation"},
			{"GRANT bogus ON t TO rg_reader", "42601", `unrecognized privilege type "bogus"`},
			{"GRANT SELECT ON t TO PUBLIC WITH GRANT OPTION", "0LP01", "grant options can only be granted to roles"},
		}
		for _, privilege := range []string{"DELETE", "TRUNCATE", "TRIGGER", "MAINTAIN"} {
			cases = append(cases, struct{ q, code, message string }{"GRANT " + privilege + "(a) ON t TO rg_reader", "0LP01", "invalid privilege type " + privilege + " for column"})
		}
		for _, tc := range cases {
			s := referenceImplicitError(tc.q, referenceDiagnostic{Code: tc.code, Message: tc.message})
			s.errorStage = "execute"
			run(s)
		}
		run(referenceExec("SET SESSION AUTHORIZATION rg_reader", 0))
		for _, tc := range []struct{ q, message string }{
			{"GRANT SELECT ON t TO rg_c", "permission denied for table t"},
			{"GRANT SELECT(a) ON t TO rg_c", `permission denied for column "a" of relation "t"`},
		} {
			s := referenceImplicitError(tc.q, referenceDiagnostic{Code: "42501", Message: tc.message})
			s.errorStage = "execute"
			run(s)
		}
		run(referenceExec("RESET SESSION AUTHORIZATION", 0))
	})
}

func TestReferenceRoleGrantGrantorDiagnostics(t *testing.T) {
	roleGrantScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn, run func(referenceStep), prepared bool) {
		run(referenceExec("CREATE TABLE t(a INT)", 0))
		run(referenceExec("INSERT INTO t VALUES(7)", 1))
		for _, q := range []string{"GRANT SELECT ON missing_table TO missing_role GRANTED BY missing_grantor", "GRANT SELECT ON t TO rg_reader GRANTED BY missing_grantor"} {
			s := referenceImplicitError(q, referenceDiagnostic{Code: "42704", Message: `role "missing_grantor" does not exist`})
			s.errorStage = "execute"
			run(s)
		}
		for _, target := range []string{"t", "missing_table"} {
			s := referenceImplicitError("GRANT SELECT ON "+target+" TO rg_reader GRANTED BY rg_owner", referenceDiagnostic{Code: "0A000", Message: "grantor must be current user"})
			s.errorStage = "execute"
			run(s)
		}
		run(referenceExec("GRANT SELECT ON t TO rg_a WITH GRANT OPTION GRANTED BY CURRENT_USER", 0))
		run(referenceExec("GRANT rg_a TO rg_group WITH INHERIT TRUE", 0))
		run(referenceExec("SET SESSION AUTHORIZATION rg_group", 0))
		run(referenceImplicitError("GRANT SELECT ON t TO rg_reader GRANTED BY rg_a", referenceDiagnostic{Code: "0A000", Message: "grantor must be current user"}))
		run(referenceExec("GRANT SELECT ON t TO rg_reader GRANTED BY CURRENT_ROLE", 0))
		run(referenceExec("RESET SESSION AUTHORIZATION", 0))
		run(referenceExec("SET SESSION AUTHORIZATION rg_reader", 0))
		run(referenceSelect("SELECT a FROM t", []string{"a"}, []string{"INT4"}, [][]any{{int64(7)}}))
		run(referenceExec("RESET SESSION AUTHORIZATION", 0))
	})
}

func TestReferenceRoleGrantNotices(t *testing.T) {
	roleGrantScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn, run func(referenceStep), prepared bool) {
		run(referenceExec("CREATE TABLE t(a INT,b INT)", 0))
		run(referenceExec("INSERT INTO t VALUES(7,9)", 1))
		run(referenceExec("GRANT SELECT ON t TO rg_a WITH GRANT OPTION", 0))
		run(referenceExec("GRANT SELECT ON t TO rg_b", 0))
		var notices []referenceDiagnostic
		_, nc, recorder := noticeReferencePool(t, p, b.pg, &notices)
		nrun := func(s referenceStep) { s.prepared = prepared; referenceRunStep(t, p.ctx, nc, recorder, s) }
		for _, tc := range []struct{ actor, q, code, message string }{
			{"rg_b", "GRANT SELECT ON t TO rg_reader", "01007", `no privileges were granted for "t"`},
			{"rg_b", "GRANT SELECT(a) ON t TO rg_reader", "01007", `no privileges were granted for column "a" of relation "t"`},
			{"rg_a", "GRANT SELECT,UPDATE ON t TO rg_reader", "01007", `not all privileges were granted for "t"`},
			{"rg_a", "REVOKE SELECT,UPDATE ON t FROM rg_reader", "01006", `not all privileges could be revoked for "t"`},
			{"rg_b", "REVOKE SELECT ON t FROM rg_reader", "01006", `no privileges could be revoked for "t"`},
		} {
			nrun(referenceExec("SET SESSION AUTHORIZATION "+tc.actor, 0))
			notices = nil
			// Prepare is independently quiet; warnings belong to Execute.
			stmt, err := nc.PrepareContext(p.ctx, tc.q)
			if err != nil {
				t.Fatal(err)
			}
			if len(notices) != 0 {
				t.Fatalf("Prepare notices %#v", notices)
			}
			stmt.Close()
			nrun(referenceExec(tc.q, 0))
			want := []referenceDiagnostic{{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: tc.code, Message: tc.message}}
			if tc.code == "01006" {
				// Exact pinned18.0 notice traversal includes negative system
				// attributes before the live positive attribute numbers.
				columnMessage := "not all privileges could be revoked"
				if tc.actor == "rg_b" {
					columnMessage = "no privileges could be revoked"
				}
				for _, column := range []string{"tableoid", "cmax", "xmax", "cmin", "xmin", "ctid", "a", "b"} {
					want = append(want, referenceDiagnostic{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: "01006", Message: columnMessage + ` for column "` + column + `" of relation "t"`})
				}
			}
			if len(notices) != len(want) {
				t.Fatalf("%s notices %#v want %#v", tc.q, notices, want)
			}
			for i := range want {
				if notices[i] != want[i] {
					t.Fatalf("%s notice %d got %#v want %#v", tc.q, i, notices[i], want[i])
				}
			}
			nrun(referenceExec("RESET SESSION AUTHORIZATION", 0))
		}
		nrun(referenceExec("SET SESSION AUTHORIZATION rg_a", 0))
		notices = nil
		nrun(referenceExec("GRANT ALL PRIVILEGES ON t TO rg_reader", 0))
		if len(notices) != 0 {
			t.Fatalf("ALL partial warning %#v", notices)
		}
		nrun(referenceExec("RESET SESSION AUTHORIZATION", 0))
		run(referenceExec("SET SESSION AUTHORIZATION rg_reader", 0))
		run(referenceSelect("SELECT a FROM t", []string{"a"}, []string{"INT4"}, [][]any{{int64(7)}}))
		run(roleGrantDenied("UPDATE t SET a=8", "t"))
		run(referenceExec("RESET SESSION AUTHORIZATION", 0))
	})
}

func TestReferenceRoleGrantAtomicity(t *testing.T) {
	roleGrantScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn, run func(referenceStep), prepared bool) {
		run(referenceExec("CREATE TABLE t(a INT)", 0))
		run(referenceExec("CREATE TABLE u(b INT)", 0))
		run(referenceExec("INSERT INTO t VALUES(7)", 1))
		for _, tc := range []struct{ q, code, message string }{
			{"GRANT SELECT ON t,missing_table TO rg_reader", "42P01", `relation "missing_table" does not exist`},
			{"GRANT SELECT(a) ON t,u TO rg_reader", "42703", `column "a" of relation "u" does not exist`},
			{"GRANT SELECT ON t TO rg_reader,missing_role", "42704", `role "missing_role" does not exist`},
			{"GRANT SELECT ON t TO rg_reader,PUBLIC WITH GRANT OPTION", "0LP01", "grant options can only be granted to roles"},
		} {
			run(referenceImplicitError(tc.q, referenceDiagnostic{Code: tc.code, Message: tc.message}))
			run(referenceExec("SET SESSION AUTHORIZATION rg_reader", 0))
			run(roleGrantDenied("SELECT a FROM t", "t"))
			run(referenceExec("RESET SESSION AUTHORIZATION", 0))
		}
		q := "GRANT SELECT ON t TO rg_reader; SELECT )"
		s := referenceImplicitError(q, referenceDiagnostic{Code: "42601", Message: `syntax error at or near ")"`, Position: referencePosition(len(q))})
		s.errorStage = "execute"
		if prepared {
			s.errorStage = "prepare"
		}
		run(s)
		run(referenceExec("SET SESSION AUTHORIZATION rg_reader", 0))
		run(roleGrantDenied("SELECT a FROM t", "t"))
		run(referenceExec("RESET SESSION AUTHORIZATION", 0))
		// Duplicate reached targets and recipients remain idempotent real grants.
		run(referenceExec("GRANT SELECT(a,a) ON t,t TO rg_reader,rg_reader", 0))
		run(referenceExec("SET SESSION AUTHORIZATION rg_reader", 0))
		run(referenceSelect("SELECT a FROM t", []string{"a"}, []string{"INT4"}, [][]any{{int64(7)}}))
		run(referenceExec("RESET SESSION AUTHORIZATION", 0))
	})
}

func TestReferenceRoleGrantReachedNotices(t *testing.T) {
	roleGrantScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn, run func(referenceStep), prepared bool) {
		run(referenceExec("CREATE TABLE t(a INT)", 0))
		run(referenceExec("CREATE TABLE u(a INT)", 0))
		run(referenceExec("GRANT SELECT ON t TO rg_b", 0))
		var notices []referenceDiagnostic
		_, nc, recorder := noticeReferencePool(t, p, b.pg, &notices)
		referenceRunStep(t, p.ctx, nc, recorder, referenceExec("SET SESSION AUTHORIZATION rg_b", 0))
		notices = nil
		s := referenceImplicitError("GRANT SELECT ON t,u TO rg_reader", referenceDiagnostic{Code: "42501", Message: "permission denied for table u"})
		s.prepared, s.errorStage = prepared, "execute"
		referenceRunStep(t, p.ctx, nc, recorder, s)
		want := referenceDiagnostic{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: "01007", Message: `no privileges were granted for "t"`}
		if len(notices) != 1 || notices[0] != want {
			t.Fatal("reached warning before later denial", notices, want)
		}
		referenceRunStep(t, p.ctx, nc, recorder, referenceExec("RESET SESSION AUTHORIZATION", 0))
		run(referenceExec("GRANT SELECT ON t TO rg_a WITH GRANT OPTION", 0))
		run(referenceExec("SET SESSION AUTHORIZATION rg_a", 0))
		run(referenceImplicitError("GRANT SELECT ON t,u TO rg_reader", referenceDiagnostic{Code: "42501", Message: "permission denied for table u"}))
		run(referenceExec("RESET SESSION AUTHORIZATION", 0))
		run(referenceExec("SET SESSION AUTHORIZATION rg_reader", 0))
		run(roleGrantDenied("SELECT a FROM t", "t"))
		run(referenceExec("RESET SESSION AUTHORIZATION", 0))
	})
}

func TestReferenceRoleGrantDuplicateRevoke(t *testing.T) {
	roleGrantScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn, run func(referenceStep), prepared bool) {
		run(referenceExec("CREATE TABLE t(a INT)", 0))
		run(referenceExec("INSERT INTO t VALUES(7)", 1))
		run(referenceExec("GRANT SELECT(a,a) ON t,t TO rg_reader,rg_reader", 0))
		run(referenceExec("REVOKE SELECT(a,a) ON t,t FROM rg_reader,rg_reader", 0))
		run(referenceExec("SET SESSION AUTHORIZATION rg_reader", 0))
		run(roleGrantDenied("SELECT a FROM t", "t"))
		run(referenceExec("RESET SESSION AUTHORIZATION", 0))
	})
}
