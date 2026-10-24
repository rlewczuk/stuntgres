package fixture

import (
	"context"
	"testing"
	"time"

	"github.com/lib/pq"
)

// AccessMethodReferenceCleanup registers database-global cleanup before scenario
// ownership. Call the returned function after NewScenario for initial cleanup.
// The public-schema reset alone cannot retire access methods.
func AccessMethodReferenceCleanup(t *testing.T, names []string) func() {
	t.Helper()
	clean := func() {
		if !Started {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		for _, name := range names {
			if _, err := Admin.ExecContext(ctx, `DROP ACCESS METHOD IF EXISTS `+pq.QuoteIdentifier(name)+` CASCADE`); err != nil {
				t.Errorf("global method cleanup %s: %v", name, err)
			}
		}
	}
	t.Cleanup(clean)
	return clean
}
