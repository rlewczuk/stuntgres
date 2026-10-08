package stuntgres_test

import (
	"database/sql"
	"testing"

	"stuntgres"
)

func nullCountError(t *testing.T, db *sql.DB, q string, want stuntgres.Error, args ...any) {
	t.Helper()
	rows, err := db.Query(q, args...)
	if rows != nil {
		rows.Close()
		t.Fatal("failed analysis exposed cursor", q)
	}
	typeCoreDiagnostic(t, err, want)
	stmt, err := db.Prepare(q)
	if stmt != nil {
		stmt.Close()
		t.Fatal("failed analysis exposed handle", q)
	}
	typeCoreDiagnostic(t, err, want)
}
