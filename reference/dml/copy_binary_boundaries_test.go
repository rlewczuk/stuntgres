package reference_test

import (
	"errors"
	"testing"
)

// Assert existing native storage admission before exercising the genuine PG
// receiver. These are explicit provider/DDL boundaries, not binary capability gates.
func copyBinaryTableBoundary(t *testing.T, c copyTextConnection, q, typ string) bool {
	t.Helper()
	if c.pg || typ != "money[]" {
		return false
	}
	// Its represented array is lookup-only: Usage.Valid rejects it when the
	// existing table backing composite checks members (composite.go61).
	_, err := c.conn.ExecContext(c.ctx, q)
	var state interface{ SQLState() string }
	if err == nil || err.Error() != "invalid composite member" || errors.As(err, &state) {
		t.Fatalf("native lookup-only money[] admission=%T %v", err, err)
	}
	return true
}
