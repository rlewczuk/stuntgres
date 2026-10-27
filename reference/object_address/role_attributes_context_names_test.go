package reference_test

import (
	"fmt"
	"github.com/lib/pq"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceRoleAttributesContextualMembershipNames(t *testing.T) {
	words := []string{"password", "connection", "valid", "encrypted", "unencrypted"}
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprintf("prepared=%v", prepared), func(t *testing.T) {
			fixture.RoleReferenceCleanup(t, append([]string{"rac_names"}, words...))
			p := fixture.NewScenario(t)
			for _, b := range p.Backends() {
				t.Run(b.Name, func(t *testing.T) {
					var notices []fixture.Diagnostic
					_, c, recorder := fixture.NoticeReferencePool(t, p, b.PG, &notices)
					bootstrap := "postgres"
					if b.PG {
						bootstrap = "stuntgres"
					}
					s := &fixture.RoleMembershipScenario{T: t, P: p, Backend: b, C: c, Record: recorder, Notices: &notices, Prepared: prepared, Bootstrap: bootstrap}
					defer c.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION")
					for _, word := range words {
						s.Exec("CREATE ROLE " + pq.QuoteIdentifier(word) + " LOGIN")
					}
					// Rotation puts every contextual word both first and after commas.
					// Attributes on both sides also prove a member list does not swallow
					// subsequent PASSWORD/CONNECTION/VALID payload options.
					for _, clause := range []string{"IN ROLE", "IN GROUP", "ROLE", "USER", "ADMIN"} {
						for first := range words {
							members := append(append([]string{}, words[first:]...), words[:first]...)
							passwordOption := "PASSWORD NULL"
							if first%2 != 0 {
								passwordOption = "ENCRYPTED PASSWORD 'role-attributes-fixture'"
							}
							s.Exec("CREATE ROLE rac_names LOGIN " + clause + " " + strings.Join(members, ", ") + " " + passwordOption + " CONNECTION LIMIT -1 VALID UNTIL 'infinity'")
							for _, word := range words {
								role, member := "rac_names", word
								if strings.HasPrefix(clause, "IN ") {
									role, member = word, "rac_names"
								}
								s.Flags(role, member, bootstrap, 1, clause == "ADMIN", true, true)
								s.Actor(member)
								s.Exec("SET ROLE " + pq.QuoteIdentifier(role))
								s.Run(fixture.Select("SELECT current_user", []string{"current_user"}, []string{"NAME"}, [][]any{{role}}))
								s.Actor("")
							}
							s.Exec("DROP ROLE rac_names")
						}
					}
					s.Exec("CREATE GROUP rac_names")
					for first := range words {
						members := append(append([]string{}, words[first:]...), words[:first]...)
						s.Exec("ALTER GROUP rac_names ADD USER " + strings.Join(members, ", "))
						for _, word := range words {
							s.Flags("rac_names", word, bootstrap, 1, false, true, true)
							s.Actor(word)
							s.Exec("SET ROLE rac_names")
							s.Actor("")
						}
						s.Exec("ALTER GROUP rac_names DROP USER " + strings.Join(members, ", "))
						for _, word := range words {
							s.Flags("rac_names", word, bootstrap, 0, false, false, false)
							s.Actor(word)
							s.Fail("SET ROLE rac_names", "42501", `permission denied to set role "rac_names"`, "")
							s.Actor("")
						}
					}
					s.Exec("DROP ROLE rac_names")
				})
			}
		})
	}
}
