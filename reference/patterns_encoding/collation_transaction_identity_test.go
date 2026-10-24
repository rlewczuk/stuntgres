package reference_test

import (
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceCollationTransactionAlterIdentity(t *testing.T) {
	for _, action := range []string{"rename_commit", "move_commit", "holder_rollback"} {
		for _, held := range []bool{false, true} {
			for _, route := range []string{"sql", "go"} {
				t.Run(fmt.Sprintf("%s/held=%v/%s", action, held, route), func(t *testing.T) {
					fixture.SchemaReferenceCleanup(t, "identity_first", "identity_second", "identity_moved")
					fixture.CollationScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
						fixture.DefinitionAlterIdentity(t, p, b, "COLLATION", action, held, route)
					})
				})
			}
		}
	}
}
