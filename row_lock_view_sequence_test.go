package stuntgres_test

import (
	"stuntgres"
	"testing"
)

func TestRowLockViewSequenceDiagnostics(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		for _, clause := range rowLockPublicClauses {
			t.Run(clause+map[bool]string{false: "/direct", true: "/prepared"}[prepared], func(t *testing.T) {
				_, a, b, ctx := rowLockPublicViewFixture(t)
				transactionAffected(t, ctx, a, "CREATE SEQUENCE s", false, 0)
				transactionAffected(t, ctx, a, "CREATE VIEW sequence_view AS SELECT last_value FROM s", false, 0)
				rows, closeQuery, err := transactionQuery(ctx, b, "SELECT last_value FROM sequence_view WHERE FALSE "+clause, prepared)
				if closeQuery != nil {
					closeQuery()
				}
				if rows != nil {
					rows.Close()
					t.Fatal("sequence error cursor")
				}
				typeCoreDiagnostic(t, err, stuntgres.Error{Code: "42809", Message: `cannot lock rows in sequence "s"`})
				transactionSelect(t, ctx, b, "SELECT last_value FROM sequence_view", prepared, 1)
			})
		}
	}
}
