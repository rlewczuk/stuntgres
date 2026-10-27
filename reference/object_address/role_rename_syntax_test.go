package reference_test

import (
	"fmt"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceRoleRenameSyntaxAtomicity(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		for _, tc := range []struct{ sql, token string }{
			{"ALTER ROLE rr_syntax RENAME TO rr_syntax_new TRAILING", "TRAILING"},
			{"ALTER USER rr_syntax RENAME rr_syntax_new", "rr_syntax_new"},
		} {
			t.Run(fmt.Sprintf("prepared=%v/%s", prepared, tc.token), func(t *testing.T) {
				fixture.RoleReferenceCleanup(t, []string{"rr_syntax", "rr_syntax_new"})
				p := fixture.NewScenario(t)
				for _, b := range p.Backends() {
					fixture.RoleAttributesRun(t, p, b, b.DB, false, fixture.Exec("CREATE ROLE rr_syntax", 0))
					d := fixture.ControlsReferenceDiagnostic("42601", `syntax error at or near "`+tc.token+`"`)
					d.Position = fixture.Position(strings.Index(tc.sql, tc.token) + 1)
					s := fixture.ErrorDiagnostic(tc.sql, d)
					s.ErrorStage = "execute"
					if prepared {
						s.ErrorStage = "prepare"
					}
					fixture.RoleAttributesRun(t, p, b, b.DB, prepared, s)
					fixture.RoleAttributesRun(t, p, b, b.DB, false, fixture.RoleTransactionExists("rr_syntax", true))
					fixture.RoleAttributesRun(t, p, b, b.DB, false, fixture.RoleTransactionExists("rr_syntax_new", false))
				}
			})
		}
	}
}
