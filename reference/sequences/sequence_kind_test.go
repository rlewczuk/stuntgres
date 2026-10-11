package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceSequenceOperationKindRecords(t *testing.T) {
	p := fixture.NewScenario(t)
	detail := "This operation is not supported for sequences."
	cases := []struct{ q, message, detail, hint, position string }{
		{"CREATE TABLE copied (LIKE s)", `relation "s" is invalid in LIKE clause`, detail, "", "27"},
		{"ALTER TABLE s ALTER COLUMN last_value SET NOT NULL", `ALTER action ALTER COLUMN ... SET NOT NULL cannot be performed on relation "s"`, detail, "", ""},
		{"ALTER TABLE s ALTER COLUMN last_value DROP NOT NULL", `ALTER action ALTER COLUMN ... DROP NOT NULL cannot be performed on relation "s"`, detail, "", ""},
		{"ALTER TABLE s DROP COLUMN last_value", `ALTER action DROP COLUMN cannot be performed on relation "s"`, detail, "", ""},
		{"COMMENT ON TABLE s IS 'x'", `"s" is not a table`, "", "", ""},
		{"COMMENT ON COLUMN s.last_value IS 'x'", `cannot set comment on relation "s"`, detail, "", ""},
		{"INSERT INTO s VALUES (1,0,true)", `cannot change sequence "s"`, "", "", ""},
		{"DROP TABLE s", `"s" is not a table`, "", "Use DROP SEQUENCE to remove a sequence.", ""},
	}
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			c := fixture.SequenceCoreConn(t, p.Ctx, backend.DB)
			fixture.SequenceProbeExec(t, p.Ctx, c, "CREATE SEQUENCE s")
			for _, tc := range cases {
				want := fixture.ControlsReferenceDiagnostic("42809", tc.message)
				want.Detail, want.Hint, want.Position = tc.detail, tc.hint, tc.position
				fixture.SequenceProbeError(t, p.Ctx, c, tc.q, &want)
				stmt, err := c.PrepareContext(p.Ctx, tc.q)
				if err != nil {
					t.Fatal("Prepare must succeed", tc.q, err)
				}
				_, err = stmt.ExecContext(p.Ctx)
				stmt.Close()
				fixture.AssertDiagnostic(t, err, &want)
			}
			outside := fixture.ControlsReferenceDiagnostic("25P01", "LOCK TABLE can only be used in transaction blocks")
			fixture.SequenceProbeError(t, p.Ctx, c, "LOCK TABLE s IN ACCESS EXCLUSIVE MODE", &outside)
			fixture.SequenceProbeExec(t, p.Ctx, c, "BEGIN")
			inside := fixture.ControlsReferenceDiagnostic("42809", `cannot lock relation "s"`)
			inside.Detail = detail
			fixture.SequenceProbeError(t, p.Ctx, c, "LOCK TABLE s IN ACCESS EXCLUSIVE MODE", &inside)
			fixture.SequenceProbeExec(t, p.Ctx, c, "ROLLBACK")
			fixture.SequenceProbeValue(t, p.Ctx, c, "SELECT nextval('s')", 1)
		})
	}
}
