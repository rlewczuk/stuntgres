package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceRoleRevokeDiagnostics(t *testing.T) {
	fixture.RoleGrantScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), prepared bool) {
		revokeTable(run)
		run(fixture.Exec("CREATE INDEX ix ON t(a)", 0))
		run(fixture.Exec("CREATE TYPE ct AS(a INT)", 0))
		cases := []struct{ q, code, message string }{
			{"REVOKE GRANT OPTION FOR SELECT ON missing_table FROM missing_role GRANTED BY missing_grantor CASCADE", "42704", `role "missing_grantor" does not exist`},
			{"REVOKE SELECT ON missing_table FROM missing_role GRANTED BY rg_owner CASCADE", "0A000", "grantor must be current user"},
			{"REVOKE GRANT OPTION FOR SELECT ON t FROM missing_role CASCADE", "42704", `role "missing_role" does not exist`},
			{"REVOKE GRANT OPTION FOR SELECT ON missing_table FROM rg_reader CASCADE", "42P01", `relation "missing_table" does not exist`},
			{"REVOKE GRANT OPTION FOR SELECT(missing_column) ON t FROM rg_reader CASCADE", "42703", `column "missing_column" of relation "t" does not exist`},
			{"REVOKE SELECT ON ALL TABLES IN SCHEMA missing_schema FROM rg_reader CASCADE", "3F000", `schema "missing_schema" does not exist`},
			{"REVOKE GRANT OPTION FOR SELECT ON ix FROM rg_reader CASCADE", "42809", `"ix" is an index`},
			{"REVOKE GRANT OPTION FOR SELECT ON ct FROM rg_reader CASCADE", "42809", `"ct" is a composite type`},
			{"REVOKE GRANT OPTION FOR CREATE ON t FROM rg_reader CASCADE", "0LP01", "invalid privilege type CREATE for relation"},
			{"REVOKE GRANT OPTION FOR bogus ON t FROM rg_reader CASCADE", "42601", `unrecognized privilege type "bogus"`},
		}
		for _, privilege := range []string{"DELETE", "TRUNCATE", "TRIGGER", "MAINTAIN"} {
			cases = append(cases, struct{ q, code, message string }{"REVOKE GRANT OPTION FOR " + privilege + "(a) ON t FROM rg_reader CASCADE", "0LP01", "invalid privilege type " + privilege + " for column"})
		}
		for _, tc := range cases {
			s := fixture.ImplicitError(tc.q, fixture.Diagnostic{Code: tc.code, Message: tc.message})
			s.ErrorStage = "execute"
			run(s)
		}
		revokeActor(run, "rg_reader", func() {
			for _, tc := range []struct{ q, message string }{
				{"REVOKE GRANT OPTION FOR SELECT ON t FROM rg_c CASCADE", "permission denied for table t"},
				{"REVOKE GRANT OPTION FOR SELECT(a) ON t FROM rg_c CASCADE", `permission denied for column "a" of relation "t"`},
			} {
				s := fixture.ImplicitError(tc.q, fixture.Diagnostic{Code: "42501", Message: tc.message})
				s.ErrorStage = "execute"
				run(s)
			}
		})
		run(fixture.Exec("GRANT SELECT ON t TO rg_a WITH GRANT OPTION", 0))
		revokeActor(run, "rg_a", func() {
			run(fixture.Exec("GRANT SELECT ON t TO rg_reader WITH GRANT OPTION", 0))
			run(fixture.Exec("REVOKE GRANT OPTION FOR SELECT ON t FROM rg_reader GRANTED BY CURRENT_USER CASCADE", 0))
			run(fixture.Exec("REVOKE SELECT ON t FROM rg_reader GRANTED BY CURRENT_ROLE CASCADE", 0))
		})
		revokeRead(run, "rg_reader", false)
	})
}

func TestReferenceRoleRevokeNotices(t *testing.T) {
	fixture.RoleGrantScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), prepared bool) {
		revokeTable(run)
		run(fixture.Exec("CREATE TABLE u(a INT)", 0))
		run(fixture.Exec("GRANT SELECT ON t TO rg_a WITH GRANT OPTION", 0))
		run(fixture.Exec("GRANT SELECT ON t TO rg_b", 0))
		var notices []fixture.Diagnostic
		_, nc, recorder := fixture.NoticeReferencePool(t, p, b.PG, &notices)
		nrun := func(s fixture.Step) { s.Prepared = prepared; fixture.RunStep(t, p.Ctx, nc, recorder, s) }
		for _, actor := range []string{"rg_a", "rg_b"} {
			nrun(fixture.Exec("SET SESSION AUTHORIZATION "+actor, 0))
			for _, request := range []string{"SELECT,UPDATE", "ALL PRIVILEGES"} {
				q := "REVOKE GRANT OPTION FOR " + request + " ON t FROM rg_reader CASCADE"
				notices = nil
				stmt, err := nc.PrepareContext(p.Ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				revokeNotices(t, notices, nil)
				stmt.Close()
				nrun(fixture.Exec(q, 0))
				var want []fixture.Diagnostic
				if actor == "rg_b" || request != "ALL PRIVILEGES" {
					message := "not all privileges could be revoked"
					if actor == "rg_b" {
						message = "no privileges could be revoked"
					}
					want = append(want, revokeWarning("01006", message+` for "t"`))
					for _, column := range []string{"tableoid", "cmax", "xmax", "cmin", "xmin", "ctid", "a", "b"} {
						want = append(want, revokeWarning("01006", message+` for column "`+column+`" of relation "t"`))
					}
				}
				revokeNotices(t, notices, want)
			}
			// Merging all four column bits suppresses nonzero partial masks,
			// regardless of enumerated versus ALL spelling; zero still warns.
			for _, request := range []string{"SELECT(a),INSERT(a),UPDATE(a),REFERENCES(a)", "ALL(a)"} {
				notices = nil
				nrun(fixture.Exec("REVOKE GRANT OPTION FOR "+request+" ON t FROM rg_reader CASCADE", 0))
				var want []fixture.Diagnostic
				if actor == "rg_b" {
					want = []fixture.Diagnostic{revokeWarning("01006", `no privileges could be revoked for column "a" of relation "t"`)}
				}
				revokeNotices(t, notices, want)
			}
			nrun(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		}
		// Authorized missing/wrong-origin rows do not generate notices.
		notices = nil
		for range 2 {
			nrun(fixture.Exec("REVOKE GRANT OPTION FOR SELECT ON t FROM rg_reader CASCADE", 0))
		}
		revokeNotices(t, notices, nil)
		// Keep reached notices on a later error, never warn for unreached u.
		nrun(fixture.Exec("SET SESSION AUTHORIZATION rg_b", 0))
		notices = nil
		s := fixture.ImplicitError("REVOKE GRANT OPTION FOR SELECT ON t,u FROM rg_reader CASCADE", fixture.Diagnostic{Code: "42501", Message: "permission denied for table u"})
		s.ErrorStage = "execute"
		nrun(s)
		want := []fixture.Diagnostic{revokeWarning("01006", `no privileges could be revoked for "t"`)}
		for _, column := range []string{"tableoid", "cmax", "xmax", "cmin", "xmin", "ctid", "a", "b"} {
			want = append(want, revokeWarning("01006", `no privileges could be revoked for column "`+column+`" of relation "t"`))
		}
		revokeNotices(t, notices, want)
		nrun(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
	})
}

func TestReferenceRoleRevokeRestrictAtomicity(t *testing.T) {
	fixture.RoleGrantScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), prepared bool) {
		revokeTable(run)
		run(fixture.Exec("CREATE TABLE u(b INT)", 0))
		run(fixture.Exec("INSERT INTO u VALUES(9)", 1))
		run(fixture.Exec("GRANT SELECT ON t,u TO rg_a WITH GRANT OPTION", 0))
		revokeActor(run, "rg_a", func() { run(fixture.Exec("GRANT SELECT ON u TO rg_b", 0)) })
		run(revokeDependency("REVOKE GRANT OPTION FOR SELECT ON t,u FROM rg_a RESTRICT"))
		revokeRead(run, "rg_a", true)
		// Prove t's option also survived, not only its ordinary privilege.
		revokeActor(run, "rg_a", func() { run(fixture.Exec("GRANT SELECT ON t TO rg_reader", 0)) })
		revokeRead(run, "rg_reader", true)
		// Later recipient dependency must roll back the earlier recipient.
		run(fixture.Exec("GRANT SELECT ON t TO rg_reader WITH GRANT OPTION", 0))
		run(fixture.Exec("GRANT SELECT ON t TO rg_c WITH GRANT OPTION", 0))
		revokeActor(run, "rg_c", func() { run(fixture.Exec("GRANT SELECT ON t TO rg_b", 0)) })
		run(revokeDependency("REVOKE GRANT OPTION FOR SELECT ON t FROM rg_reader,rg_c RESTRICT"))
		revokeRead(run, "rg_reader", true)
		revokeActor(run, "rg_reader", func() { run(fixture.Exec("GRANT SELECT ON t TO rg_owner", 0)) })
		run(fixture.Exec("REVOKE SELECT ON t FROM rg_reader CASCADE", 0))
		run(fixture.Exec("GRANT SELECT(a) ON t TO rg_a WITH GRANT OPTION", 0))
		revokeActor(run, "rg_a", func() { run(fixture.Exec("GRANT SELECT(a) ON t TO rg_group", 0)) })
		run(revokeDependency("REVOKE GRANT OPTION FOR SELECT ON t FROM rg_a RESTRICT"))
		for _, tc := range []struct{ q, code, message string }{
			{"REVOKE GRANT OPTION FOR SELECT ON t,missing_table FROM rg_a CASCADE", "42P01", `relation "missing_table" does not exist`},
			{"REVOKE GRANT OPTION FOR SELECT(a) ON t,u FROM rg_a CASCADE", "42703", `column "a" of relation "u" does not exist`},
			{"REVOKE GRANT OPTION FOR SELECT ON t FROM rg_a,missing_role CASCADE", "42704", `role "missing_role" does not exist`},
		} {
			run(fixture.ImplicitError(tc.q, fixture.Diagnostic{Code: tc.code, Message: tc.message}))
			revokeRead(run, "rg_a", true)
			revokeRead(run, "rg_reader", true)
			revokeActor(run, "rg_a", func() { run(fixture.Exec("GRANT SELECT ON t TO rg_reader", 0)) })
		}
		q := "REVOKE GRANT OPTION FOR SELECT ON t FROM rg_a CASCADE; SELECT )"
		s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "42601", Message: `syntax error at or near ")"`, Position: fixture.Position(len(q))})
		s.ErrorStage = "execute"
		if prepared {
			s.ErrorStage = "prepare"
		}
		run(s)
		revokeRead(run, "rg_a", true)
		revokeRead(run, "rg_reader", true)
		// Duplicates merge once and successful CASCADE publishes all removals.
		run(fixture.Exec("REVOKE GRANT OPTION FOR SELECT(a,a),SELECT ON t,t FROM rg_a,rg_a CASCADE", 0))
		revokeRead(run, "rg_a", true)
		revokeRead(run, "rg_reader", false)
		revokeActor(run, "rg_group", func() { run(fixture.RoleGrantDenied("SELECT a FROM t", "t")) })
	})
}
