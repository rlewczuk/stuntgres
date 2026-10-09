package reference_test

import (
	"context"
	"database/sql"
	"strings"
	"testing"
)

// The fixture resets public, not shared role metadata. Preserve the real
// server's original description rather than presuming it was SQL NULL.
func pgDescriptionPreserveSharedRole(t *testing.T, p *referencePair, backend referenceBackend) {
	t.Helper()
	if !backend.pg {
		return
	}
	oid := pgDescriptionSharedRoleID(t, p, backend)
	var original sql.NullString
	if err := backend.db.QueryRowContext(p.ctx, "SELECT shobj_description($1,'pg_authid')", oid).Scan(&original); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		literal := "NULL"
		if original.Valid {
			literal = "E'" + strings.NewReplacer("\\", "\\\\", "'", "''", "\n", "\\n", "\r", "\\r").Replace(original.String) + "'"
		}
		if _, err := backend.db.ExecContext(context.Background(), "COMMENT ON ROLE postgres IS "+literal); err != nil {
			t.Error("restore shared role comment", err)
		}
	})
}
