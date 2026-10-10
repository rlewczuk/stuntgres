package reference_test

import (
	"fmt"
	"strings"
	"testing"
)

func TestReferenceRoleMembershipDependencies(t *testing.T) {
	for index, order := range []string{"endpoint", "grantor,endpoint", "endpoint,grantor", "grantor,member", "member,grantor"} {
		t.Run(fmt.Sprintf("order=%d", index), func(t *testing.T) {
			prefix := fmt.Sprintf("rm_%d_", index)
			endpoint, grantor, member := prefix+"endpoint", prefix+"grantor", prefix+"member"
			membershipCases(t, []string{member, grantor, endpoint}, func(s *membershipScenario) {
				t := s.t
				s.exec("GRANT " + endpoint + " TO " + grantor + " WITH ADMIN OPTION")
				s.exec("GRANT " + endpoint + " TO " + member + " GRANTED BY " + grantor)
				memberships := func(count int) {
					s.flags(endpoint, grantor, s.bootstrap, count, true, true, true)
					s.flags(endpoint, member, grantor, count, false, true, true)
					if s.backend.pg {
						var total int
						err := s.c.QueryRowContext(s.p.ctx, "SELECT count(*) FROM pg_auth_members a JOIN pg_roles r ON r.oid=a.roleid WHERE r.rolname=$1", endpoint).Scan(&total)
						if err != nil || total != 2*count {
							t.Fatal("complete membership count", total, 2*count, err)
						}
					}
				}
				memberships(1)
				s.fail("DROP ROLE "+grantor, "2BP01", fmt.Sprintf("role \"%s\" cannot be dropped because some objects depend on it", grantor), "privileges for membership of role "+member+" in role "+endpoint)
				memberships(1)
				s.fail("DROP ROLE "+endpoint+",missing_membership_role", "42704", `role "missing_membership_role" does not exist`, "")
				memberships(1)
				// Later owner dependency must roll back earlier endpoint cleanup too.
				if s.backend.pg {
					s.exec("GRANT USAGE, CREATE ON SCHEMA public TO PUBLIC")
				}
				s.actor(member)
				s.exec("CREATE TABLE membership_dependency(id INT)")
				s.actor("")
				s.fail("DROP ROLE "+endpoint+","+member, "2BP01", fmt.Sprintf("role \"%s\" cannot be dropped because some objects depend on it", member), "owner of table membership_dependency")
				memberships(1)
				s.exec("DROP TABLE membership_dependency")
				// Shared SQL delegation witness proves the graph survived both failures.
				s.actor(grantor)
				s.exec("GRANT " + endpoint + " TO " + member)
				s.actor("")
				targets := strings.NewReplacer("endpoint", endpoint, "grantor", grantor, "member", member).Replace(order)
				s.exec("DROP ROLE " + targets)
				memberships(0)
				// Surviving endpoints can be dropped: no attributed grants remain.
				for _, remaining := range []struct{ label, name string }{{"member", member}, {"grantor", grantor}, {"endpoint", endpoint}} {
					if !strings.Contains(order, remaining.label) {
						s.exec("DROP ROLE " + remaining.name)
					}
				}
			})
		})
	}
}
