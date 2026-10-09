package reference_test

import (
	"database/sql"
	"stuntgres"
	"testing"
)

// Original non-Context consumer routes remain distinct from recorded companions.
func updateReturningReferenceExec(t *testing.T, db *sql.DB, query string) {
	t.Helper()
	result, err := db.Exec(query)
	if err != nil {
		t.Fatal(query, err)
	}
	if _, err := result.LastInsertId(); err == nil {
		t.Fatal("LastInsertId unexpectedly supported")
	}
}

func updateReturningReferenceDiagnostic(t *testing.T, err error, want referenceDiagnostic) {
	t.Helper()
	want.Severity, want.SeverityUnlocalized = "ERROR", "ERROR"
	referenceAssertDiagnostic(t, err, &want)
}

func updateReturningNativeDestination(t *testing.T) *sql.DB {
	t.Helper()
	connector, err := stuntgres.NewDriver().OpenConnector(t.Name())
	if err != nil {
		t.Fatal(err)
	}
	db := sql.OpenDB(connector)
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	})
	return db
}
