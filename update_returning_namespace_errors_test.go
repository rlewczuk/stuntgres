package stuntgres_test

import (
	"stuntgres"
	"testing"
)

func TestUpdateReturningNamespaceErrors(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	execSQL(t, db, "CREATE TABLE t(id INT)")
	execSQL(t, db, "INSERT INTO t VALUES(1)")
	_, err := db.Exec("UPDATE t SET id=2 RETURNING id AS label,label")
	typeCoreDiagnostic(t, err, stuntgres.Error{Code: "42703", Message: `column "label" does not exist`, Position: "41"})
	execSQL(t, db, "ALTER TABLE t DROP COLUMN id")
	_, err = db.Prepare("UPDATE t SET absent=1 RETURNING *")
	typeCoreDiagnostic(t, err, stuntgres.Error{Code: "42601", Message: "RETURNING must have at least one column", Position: "33"})
}
