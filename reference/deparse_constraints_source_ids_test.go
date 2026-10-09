package reference_test

import (
	"database/sql"
	"testing"
)

// Original public consumer routes run in fresh phases; the Context companions remain intact.
func deparseSourceDefinitionID(t *testing.T, db *sql.DB, query string, args ...any) int64 {
	t.Helper()
	var id int64
	if err := db.QueryRow(query, args...).Scan(&id); err != nil || id <= 0 || id > 4294967295 {
		t.Fatal(id, err)
	}
	return id
}
