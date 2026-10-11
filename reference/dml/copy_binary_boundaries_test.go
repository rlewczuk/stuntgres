package reference_test

import (
	"errors"
	"strconv"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

// A release oracle's valid domain constraints do not add native DDL authority.
// Execute and assert the genuine positioned native setup rejection, never skip
// the case or pretend that receiver/publication code handled an absent domain.
func copyBinaryDomainBoundary(t *testing.T, c copyTextConnection, q string) bool {
	t.Helper()
	if c.pg || !strings.HasPrefix(q, "CREATE DOMAIN ") {
		return false
	}
	at := -1
	for _, token := range []string{"CHECK", "NOT NULL", "DEFAULT"} {
		if n := strings.Index(q, token); n >= 0 && (at < 0 || n < at) {
			at = n
		}
	}
	if at < 0 {
		return false
	}
	_, err := c.conn.ExecContext(c.ctx, q)
	fixture.AssertDiagnostic(t, err, &fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "0A000", Message: "domain constraints and defaults are unsupported", Position: strconv.Itoa(at + 1)})
	return true
}

// Assert existing native storage admission before exercising the genuine PG
// receiver. These are explicit provider/DDL boundaries, not binary capability gates.
func copyBinaryTableBoundary(t *testing.T, c copyTextConnection, q, typ string) bool {
	t.Helper()
	if c.pg {
		return false
	}
	message := ""
	switch typ {
	case `"char"`, "xid":
		message = "unsupported column type"
	case "copy_mood[]":
		message = "unsupported array element type"
	case "money[]":
		// Its represented array is lookup-only: Usage.Valid rejects it when the
		// existing table backing composite checks members (composite.go61).
		_, err := c.conn.ExecContext(c.ctx, q)
		var state interface{ SQLState() string }
		if err == nil || err.Error() != "invalid composite member" || errors.As(err, &state) {
			t.Fatalf("native lookup-only money[] admission=%T %v", err, err)
		}
		return true
	default:
		return false
	}
	_, err := c.conn.ExecContext(c.ctx, q)
	position := strings.Index(q, "(v ") + len("(v ") + 1
	fixture.AssertDiagnostic(t, err, &fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "0A000", Message: message, Position: strconv.Itoa(position)})
	return true
}
