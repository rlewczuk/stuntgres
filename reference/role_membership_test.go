//go:build reference

package reference_test

import (
	"fmt"
	"strings"
	"testing"
)

// SQL membership mutation is oracle-fixture authority only. Native membership
// behavior is independently exercised through the real internal seam in U4.
func TestReferenceRoleMembershipDependencies(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		for index, order := range []string{"endpoint", "grantor,endpoint", "endpoint,grantor", "grantor,member", "member,grantor"} {
			t.Run(fmt.Sprintf("prepared=%v/order=%d", prepared, index), func(t *testing.T) {
				prefix := fmt.Sprintf("rm_%v_%d_", prepared, index)
				endpoint, grantor, member := prefix+"endpoint", prefix+"grantor", prefix+"member"
				roleReferenceCleanup(t, []string{member, grantor, endpoint})
				p := newReferencePair(t)
				exec := func(query string) error {
					if !prepared {
						_, err := referenceAdmin.ExecContext(p.ctx, query)
						return err
					}
					stmt, err := referenceAdmin.PrepareContext(p.ctx, query)
					if err != nil {
						t.Fatal("unexpected protocol Prepare failure", query, err)
					}
					defer stmt.Close()
					_, err = stmt.ExecContext(p.ctx)
					return err
				}
				for _, name := range []string{endpoint, grantor, member} {
					if err := exec("CREATE ROLE " + name); err != nil {
						t.Fatal(err)
					}
				}
				for _, query := range []string{
					"GRANT " + endpoint + " TO " + grantor + " WITH ADMIN OPTION",
					"GRANT " + endpoint + " TO " + member + " GRANTED BY " + grantor,
				} {
					if _, err := referenceAdmin.ExecContext(p.ctx, query); err != nil {
						t.Fatal(err)
					}
				}
				memberships := func(want int) {
					rows, err := referenceAdmin.QueryContext(p.ctx, `SELECT m.rolname,g.rolname,a.admin_option,a.inherit_option,a.set_option
						FROM pg_auth_members a JOIN pg_roles r ON r.oid=a.roleid
						JOIN pg_roles m ON m.oid=a.member JOIN pg_roles g ON g.oid=a.grantor
						WHERE r.rolname=$1 ORDER BY m.rolname`, endpoint)
					if err != nil {
						t.Fatal(err)
					}
					defer rows.Close()
					count := 0
					for rows.Next() {
						var m, g string
						var admin, inherit, set bool
						if err := rows.Scan(&m, &g, &admin, &inherit, &set); err != nil {
							t.Fatal(err)
						}
						if !inherit || !set {
							t.Fatal("membership default flags", inherit, set)
						}
						if m == grantor {
							if g != "stuntgres" || !admin {
								t.Fatal("upstream membership", m, g, admin)
							}
						} else if m == member {
							if g != grantor || admin {
								t.Fatal("grantor membership", m, g, admin)
							}
						} else {
							t.Fatal("unexpected member", m)
						}
						count++
					}
					if rows.Err() != nil || count != want {
						t.Fatal("membership count", count, want, rows.Err())
					}
				}
				memberships(2)
				err := exec("DROP ROLE " + grantor)
				want := referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "2BP01",
					Message: fmt.Sprintf("role \"%s\" cannot be dropped because some objects depend on it", grantor),
					Detail:  "privileges for membership of role " + member + " in role " + endpoint}
				referenceAssertDiagnostic(t, err, &want)
				memberships(2)
				err = exec("DROP ROLE " + endpoint + ",missing_membership_role")
				missing := referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42704", Message: `role "missing_membership_role" does not exist`}
				referenceAssertDiagnostic(t, err, &missing)
				memberships(2)
				targets := strings.ReplaceAll(order, "endpoint", endpoint)
				targets = strings.ReplaceAll(targets, "grantor", grantor)
				targets = strings.ReplaceAll(targets, "member", member)
				if err := exec("DROP ROLE " + targets); err != nil {
					t.Fatal(err)
				}
				memberships(0)
			})
		}
	}
}
