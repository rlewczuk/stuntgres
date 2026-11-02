package enginetests

import (
	"testing"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/enginetest"
)

func TestPgStatStatementsFingerprintUtilityNullableFields(t *testing.T) {
	// REL_18_6 CreateForeignServerStmt has nullable type/version strings;
	// AlterForeignServerStmt additionally selects has_version. Empty is not NULL.
	for _, tc := range []struct {
		left, right string
		same        bool
	}{
		{"CREATE SERVER s FOREIGN DATA WRAPPER w", "CREATE SERVER s TYPE '' FOREIGN DATA WRAPPER w", false},
		{"CREATE SERVER s FOREIGN DATA WRAPPER w", "CREATE SERVER s VERSION '' FOREIGN DATA WRAPPER w", false},
		{"CREATE SERVER s FOREIGN DATA WRAPPER w", "CREATE SERVER s VERSION NULL FOREIGN DATA WRAPPER w", true},
		{"CREATE SERVER s VERSION 'one' FOREIGN DATA WRAPPER w", "CREATE SERVER s VERSION 'two' FOREIGN DATA WRAPPER w", false},
		{"ALTER SERVER s VERSION ''", "ALTER SERVER s VERSION NULL", false},
		{"ALTER SERVER s OPTIONS (ADD k 'v')", "ALTER SERVER s VERSION NULL OPTIONS (ADD k 'v')", false},
	} {
		t.Run(tc.left+" / "+tc.right, func(t *testing.T) {
			left, _ := analyze.JumbleUtility(enginetest.Statement(t, tc.left))
			right, _ := analyze.JumbleUtility(enginetest.Statement(t, tc.right))
			if (left.QueryID() == right.QueryID()) != tc.same {
				t.Fatalf("selected nullable utility fields: same=%v, want %v", left.QueryID() == right.QueryID(), tc.same)
			}
		})
	}
}
