package fixture

import (
	"database/sql"
	"testing"
)

func DeparseDefinitionID(t *testing.T, conn *sql.Conn, query string, args ...any) int64 {
	t.Helper()
	var id int64
	if err := conn.QueryRowContext(t.Context(), query, args...).Scan(&id); err != nil || id <= 0 || id > 4294967295 {
		t.Fatal(query, id, err)
	}
	return id
}
