package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

// These witnesses consume real authority instead of unsupported role/ACL inquiry
// catalogs. The same SQL checks pending, restored and committed role roots.
func batchUtilityPrivilegeEffects(t *testing.T) {
	for _, membership := range []bool{true, false} {
		for _, revoke := range []bool{false, true} {
			name := "object_grant"
			if membership {
				name = "membership_grant"
			}
			if revoke {
				if membership {
					name = "membership_revoke"
				} else {
					name = "object_revoke"
				}
			}
			t.Run(name, func(t *testing.T) {
				fixture.RoleReferenceCleanup(t, []string{"batch_role", "batch_member"})
				batchScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, a, peer *sql.Conn) {
					batchExec(t, p, b, a, "CREATE ROLE batch_role", 0)
					batchExec(t, p, b, a, "CREATE ROLE batch_member", 0)
					grant := "GRANT batch_role TO batch_member"
					change := grant
					consumer := "SET SESSION AUTHORIZATION batch_member; SET ROLE batch_role; SELECT current_user AS v"
					denied := batchDiag("42501", `permission denied to set role "batch_role"`)
					result := batchSet{[]string{"v"}, []string{"NAME"}, [][]any{{"batch_role"}}, false}
					if !membership {
						batchExec(t, p, b, a, "CREATE TABLE u(i INT)", 0)
						batchExec(t, p, b, a, "INSERT INTO u VALUES(3)", 1)
						if b.PG {
							batchExec(t, p, b, a, "GRANT USAGE ON SCHEMA public TO batch_member", 0)
						}
						grant = "GRANT SELECT ON u TO batch_member"
						change = grant
						consumer = "SET SESSION AUTHORIZATION batch_member; SELECT i AS v FROM u"
						denied = batchDiag("42501", "permission denied for table u")
						result = batchSet{[]string{"v"}, []string{"INT4"}, [][]any{{int64(3)}}, false}
					}
					if revoke {
						batchExec(t, p, b, a, grant, 0)
						if membership {
							change = "REVOKE batch_role FROM batch_member"
						} else {
							change = "REVOKE SELECT ON u FROM batch_member"
						}
					}
					observe := func(x *sql.Conn, allowed bool) {
						batchExec(t, p, b, x, "SET SESSION AUTHORIZATION batch_member", 0)
						defer batchExec(t, p, b, x, "RESET SESSION AUTHORIZATION; RESET ROLE", 0)
						if membership {
							_, err := x.ExecContext(p.Ctx, "SET ROLE batch_role")
							if allowed {
								if err != nil {
									t.Fatal("committed membership consumer", err)
								}
								fixture.RunStep(t, p.Ctx, x, b.Recorder, fixture.Select("SELECT current_user AS v", result.columns, result.types, result.rows))
							} else {
								fixture.AssertDiagnostic(t, err, &denied)
							}
						} else {
							rows, err := x.QueryContext(p.Ctx, "SELECT i AS v FROM u")
							if allowed {
								if err != nil {
									t.Fatal("committed object privilege consumer", err)
								}
								batchSets(t, rows, result)
							} else {
								if rows != nil {
									rows.Close()
								}
								fixture.AssertDiagnostic(t, err, &denied)
							}
						}
					}
					observe(a, revoke)
					observe(peer, revoke)
					q := change + "; " + consumer + "; SELECT missing"
					if revoke {
						batchError(t, p, a, q, denied)
					} else {
						w := batchDiag("42703", `column "missing" does not exist`)
						w.Position = batchPosition(q, "missing")
						r := batchQuery(t, p, a, q)
						batchReadSet(t, r, result)
						fixture.AssertDiagnostic(t, r.Err(), &w)
						r.Close()
					}
					observe(a, revoke)
					observe(peer, revoke)
					batchExec(t, p, b, a, change+"; BEGIN", 0)
					observe(peer, revoke)
					batchExec(t, p, b, a, "COMMIT", 0)
					observe(a, !revoke)
					observe(peer, !revoke)
				})
			})
		}
	}
}
