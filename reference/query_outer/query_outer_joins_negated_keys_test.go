package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceQueryOuterJoinsNegatedKeys(t *testing.T) {
	fixture.OuterJoinsBackendScenario(t, func(t *testing.T, p *fixture.Pair, conn *sql.Conn, backend fixture.Backend) {
		inputs := []struct {
			name string
			from string
			rows [][]any
		}{
			{"populated", "a FULL JOIN b", fixture.OuterJoinsOnRows("FULL")},
			{"empty_left", "empty_a AS a FULL JOIN b", [][]any{
				{nil, nil, int64(1), "x"}, {nil, nil, int64(1), "x"},
				{nil, nil, int64(3), "y"}, {nil, nil, nil, "m"},
			}},
			{"empty_right", "a FULL JOIN empty_b AS b", [][]any{
				{int64(1), "a", nil, nil}, {int64(1), "a", nil, nil},
				{int64(2), "b", nil, nil}, {nil, "n", nil, nil},
			}},
			{"both_empty", "empty_a AS a FULL JOIN empty_b AS b", nil},
		}
		for _, condition := range []string{
			"NOT (CAST(a.k AS BIGINT) <> b.k)",
			"NOT (b.k <> CAST(a.k AS BIGINT))",
			"NOT (a.k <> b.k)",
			"NOT (b.k <> a.k)",
			"NOT (CAST(a.k AS BIGINT) != b.k)",
			"NOT (b.k != CAST(a.k AS BIGINT))",
			"NOT (a.k != b.k)",
			"NOT (b.k != a.k)",
			"NOT NOT NOT (CAST(a.k AS BIGINT) <> b.k)",
			// The effective equality, not the original comparison spelling,
			// must be the common key across these OR branches.
			"(CAST(a.k AS BIGINT)=b.k AND a.l='a') OR (NOT (CAST(a.k AS BIGINT)<>b.k) AND b.r='x')",
			"(a.k=b.k AND a.l='a') OR (NOT (a.k!=b.k) AND b.r='x')",
			"(NOT (a.k<>b.k) AND a.l='a') OR (NOT (a.k!=b.k) AND b.r='x')",
			// This is effective equality OR UNKNOWN: TRUE still matches,
			// while FALSE/UNKNOWN do not match, including NULL keys.
			"NOT ((CAST(a.k AS BIGINT)<>b.k) AND NULL::boolean)",
		} {
			t.Run(condition, func(t *testing.T) {
				for _, input := range inputs {
					t.Run(input.name, func(t *testing.T) {
						query := "SELECT * FROM " + input.from + " ON " + condition
						fixture.OuterJoinsCases(t, p, conn, backend.Recorder,
							fixture.OuterJoinsSelect(query, input.rows),
							fixture.OuterJoinsSelect(query+" LIMIT 0", nil))
					})
				}
			})
		}
		// Negation must preserve UNKNOWN: neither a NULL key nor a NULL
		// residual can create a match. A genuine equality conjunct still
		// makes an otherwise impossible predicate FULL-capable.
		for _, condition := range []string{
			"NOT ((CAST(a.k AS BIGINT)<>b.k) OR NULL::boolean)",
			"NOT (CAST(a.k AS BIGINT)<>b.k) AND NULL::boolean",
			"a.k=b.k AND NOT (a.k=b.k)",
			"NOT NULL::boolean",
		} {
			t.Run(condition, func(t *testing.T) {
				for _, input := range inputs {
					t.Run(input.name, func(t *testing.T) {
						rows := input.rows
						if input.name == "populated" {
							rows = fixture.OuterJoinsUnmatchedRows("FULL")
						}
						query := "SELECT * FROM " + input.from + " ON " + condition
						fixture.OuterJoinsCases(t, p, conn, backend.Recorder,
							fixture.OuterJoinsSelect(query, rows),
							fixture.OuterJoinsSelect(query+" LIMIT 0", nil))
					})
				}
			})
		}
		for _, condition := range []string{
			"CAST(a.k AS BIGINT)<>b.k",
			"a.k!=b.k",
			"NOT (CAST(a.k AS BIGINT)=b.k)",
			"NOT (b.k=a.k)",
			"NOT NOT (CAST(a.k AS BIGINT)<>b.k)",
			// Common-OR extraction does not commute operand identity,
			// even though each branch alone contains a usable key.
			"(CAST(a.k AS BIGINT)=b.k AND a.l='a') OR (NOT (b.k<>CAST(a.k AS BIGINT)) AND b.r='x')",
			"(a.k=b.k AND a.l='a') OR (NOT (b.k!=a.k) AND b.r='x')",
			"(NOT (a.k<>b.k) AND a.l='a') OR (NOT (b.k!=a.k) AND b.r='x')",
		} {
			t.Run(condition, func(t *testing.T) {
				for _, input := range inputs {
					t.Run(input.name, func(t *testing.T) {
						for _, limit := range []string{"", " LIMIT 0"} {
							step := fixture.ImplicitError("SELECT * FROM "+input.from+" ON "+condition+limit,
								fixture.Diagnostic{Code: "0A000", Message: "FULL JOIN is only supported with merge-joinable or hash-joinable join conditions"})
							step.SelectRows, step.ErrorStage = true, "execute"
							fixture.RunStep(t, p.Ctx, conn, backend.Recorder, step)
							fixture.RunStep(t, p.Ctx, conn, backend.Recorder, fixture.Prepared(step))
						}
					})
				}
			})
		}
	})
}
