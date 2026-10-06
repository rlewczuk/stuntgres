//go:build reference

package reference_test

import (
	"database/sql"
	"testing"
)

func TestReferenceSequenceOperationKindRecords(t *testing.T) {
	p := newReferencePair(t)
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
	for _, db := range []*sql.DB{p.memory, p.postgres} {
		c, err := db.Conn(p.ctx)
		if err != nil {
			t.Fatal(err)
		}
		sequenceProbeExec(t, p.ctx, c, "CREATE SEQUENCE s")
		for _, tc := range cases {
			want := controlsReferenceDiagnostic("42809", tc.message)
			want.Detail, want.Hint, want.Position = tc.detail, tc.hint, tc.position
			sequenceProbeError(t, p.ctx, c, tc.q, &want)
			stmt, err := c.PrepareContext(p.ctx, tc.q)
			if err != nil {
				t.Fatal("Prepare must succeed", tc.q, err)
			}
			_, err = stmt.ExecContext(p.ctx)
			stmt.Close()
			referenceAssertDiagnostic(t, err, &want)
		}
		outside := controlsReferenceDiagnostic("25P01", "LOCK TABLE can only be used in transaction blocks")
		sequenceProbeError(t, p.ctx, c, "LOCK TABLE s IN ACCESS EXCLUSIVE MODE", &outside)
		sequenceProbeExec(t, p.ctx, c, "BEGIN")
		inside := controlsReferenceDiagnostic("42809", `cannot lock relation "s"`)
		inside.Detail = detail
		sequenceProbeError(t, p.ctx, c, "LOCK TABLE s IN ACCESS EXCLUSIVE MODE", &inside)
		sequenceProbeExec(t, p.ctx, c, "ROLLBACK")
		sequenceProbeValue(t, p.ctx, c, "SELECT nextval('s')", 1)
		c.Close()
	}
}
