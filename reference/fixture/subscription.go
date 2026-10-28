package fixture

import (
	"context"
	"github.com/lib/pq"
	"testing"
	"time"
)

// SubscriptionReferenceCleanup runs after scenario handles close. Disassociation
// ensures disconnected declarations never attempt remote slot cleanup.
func SubscriptionReferenceCleanup(t *testing.T, names []string) func() {
	t.Helper()
	clean := func() {
		if !Started {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		for _, name := range names {
			var n int
			if err := Admin.QueryRowContext(ctx, "SELECT count(*) FROM pg_subscription WHERE subname=$1 AND subdbid=(SELECT oid FROM pg_database WHERE datname=current_database())", name).Scan(&n); err != nil {
				t.Errorf("subscription cleanup lookup: %v", err)
				continue
			}
			if n == 0 {
				continue
			}
			quoted := pq.QuoteIdentifier(name)
			for _, query := range []string{"ALTER SUBSCRIPTION " + quoted + " DISABLE", "ALTER SUBSCRIPTION " + quoted + " SET (slot_name=NONE)", "DROP SUBSCRIPTION " + quoted} {
				if _, err := Admin.ExecContext(ctx, query); err != nil {
					t.Errorf("subscription cleanup %s: %v", name, err)
					break
				}
			}
		}
	}
	t.Cleanup(clean)
	return clean
}
