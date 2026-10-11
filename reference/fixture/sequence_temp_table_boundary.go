package fixture

import (
	"strings"
	"testing"
)

func TempReferenceNamespace(t *testing.T, s *TempReferenceSession, peer bool) string {
	t.Helper()
	c := s.C
	if peer {
		c = s.Peer
	}
	var name string
	if err := c.QueryRowContext(s.P.Ctx, "SELECT current_schema").Scan(&name); err != nil || !strings.HasPrefix(name, "pg_temp_") {
		t.Fatal(name, err)
	}
	return name
}
