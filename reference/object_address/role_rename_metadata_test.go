package reference_test

import (
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceRoleRenameMetadata(t *testing.T) {
	for _, route := range []string{"direct", "fresh", "held"} {
		t.Run(route, func(t *testing.T) {
			fixture.RoleReferenceCleanup(t, []string{"rr_metadata", "rr_metadata_new"})
			p := fixture.NewScenario(t)
			for _, b := range p.Backends() {
				fixture.RoleAttributesRun(t, p, b, b.DB, false, fixture.Exec("CREATE ROLE rr_metadata", 0))
				s := fixture.Select("ALTER ROLE rr_metadata RENAME TO rr_metadata_new", []string{}, []string{}, [][]any{})
				switch route {
				case "direct":
					fixture.RoleAttributesRun(t, p, b, b.DB, false, s)
				case "fresh":
					fixture.RoleAttributesRun(t, p, b, b.DB, true, s)
				case "held":
					fixture.RoleTransactionStep(t, p, b, b.DB, true, s)
				}
				fixture.RoleAttributesRun(t, p, b, b.DB, false, fixture.RoleTransactionExists("rr_metadata_new", true))
			}
		})
	}
}

func TestReferenceRoleRenameReadOnly(t *testing.T) {
	for _, held := range []bool{false, true} {
		t.Run(fmt.Sprintf("held=%v", held), func(t *testing.T) {
			fixture.RoleReferenceCleanup(t, []string{"rr_readonly", "rr_readonly_new"})
			p := fixture.NewScenario(t)
			for _, b := range p.Backends() {
				c, _ := fixture.RoleTransactionConn(t, p, b)
				run := func(s fixture.Step) { fixture.RoleAttributesRun(t, p, b, c, false, s) }
				run(fixture.Exec("CREATE ROLE rr_readonly", 0))
				run(fixture.Exec("BEGIN READ ONLY", 0))
				denied := fixture.RoleReferenceError("ALTER ROLE rr_readonly RENAME TO rr_readonly_new", "25006", "cannot execute ALTER ROLE in a read-only transaction", "", false, "execute")
				if held {
					fixture.RoleTransactionStep(t, p, b, c, true, denied)
				} else {
					run(denied)
				}
				run(fixture.Exec("ROLLBACK", 0))
				run(fixture.RoleTransactionExists("rr_readonly", true))
				run(fixture.RoleTransactionExists("rr_readonly_new", false))
			}
		})
	}
}
