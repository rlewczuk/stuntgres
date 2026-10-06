//go:build reference

package reference_test

import (
	"database/sql"
	"testing"
)

func TestReferenceSequenceSignatures(t *testing.T) {
	p := newReferencePair(t)
	for _, db := range []*sql.DB{p.memory, p.postgres} {
		c, err := db.Conn(p.ctx)
		if err != nil {
			t.Fatal(err)
		}
		sequenceProbeExec(t, p.ctx, c, "CREATE SEQUENCE s")
		for _, tc := range []struct{ q, signature string }{{"SELECT nextval()", "nextval()"}, {"SELECT lastval(1)", "lastval(integer)"}, {"SELECT setval('s')", "setval(unknown)"}, {"SELECT nextval(true)", "nextval(boolean)"}, {"SELECT setval('s',true)", "setval(unknown, boolean)"}} {
			want := controlsReferenceDiagnostic("42883", "function "+tc.signature+" does not exist")
			want.Hint = "No function matches the given name and argument types. You might need to add explicit type casts."
			want.Position = "8"
			sequenceProbeError(t, p.ctx, c, tc.q, &want)
			stmt, err := c.PrepareContext(p.ctx, tc.q)
			if stmt != nil {
				stmt.Close()
			}
			referenceAssertDiagnostic(t, err, &want)
		}
		sequenceProbeValue(t, p.ctx, c, "SELECT nextval('s')", 1)
		c.Close()
	}
}
