package stuntgres_test

import (
	"context"
	"stuntgres"
	"testing"
)

func TestViewWriteCapabilityBoundary(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	execSQL(t, db, "CREATE TABLE t(id INT)")
	execSQL(t, db, "INSERT INTO t VALUES(7)")
	execSQL(t, db, "CREATE VIEW v AS SELECT id FROM t")
	for _, query := range []string{"INSERT INTO v VALUES(8)", "UPDATE v SET id=8", "DELETE FROM v"} {
		_, err := db.ExecContext(context.Background(), query)
		typeCoreDiagnostic(t, err, stuntgres.Error{Code: "0A000", Message: "view writes are unsupported"})
	}
	rows, err := db.Query("SELECT id FROM t")
	queryCoreRows(t, rows, err, []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}}, false)
}

func TestViewInternalCHARStaysProviderBounded(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	_, err := db.Exec(`CREATE TABLE bad(x "char")`)
	typeCoreDiagnostic(t, err, stuntgres.Error{Code: "0A000", Message: "unsupported column type", Position: "20"})
}
