package stuntgres_test

import (
	"stuntgres"
	"testing"
)

func TestUpdateReturningExcludedMechanisms(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	execSQL(t, db, "CREATE TABLE t(id INT)")
	execSQL(t, db, "INSERT INTO t VALUES(1)")
	for _, q := range []string{"UPDATE t SET id=2 RETURNING (SELECT 7)", "UPDATE t SET id=2 RETURNING old", "UPDATE t SET id=2 RETURNING (old).*", "UPDATE t SET id=2 FROM t RETURNING id", "UPDATE t x SET id=2 RETURNING x.id", "INSERT INTO t VALUES(2) RETURNING id", "CREATE TRIGGER change BEFORE UPDATE ON t FOR EACH ROW EXECUTE FUNCTION adjust_update()"} {
		result, err := db.Exec(q)
		if err == nil || result != nil {
			t.Fatal("external mechanism became successful native no-op", q, result, err)
		}
	}
	rows, err := db.Query("SELECT id FROM t")
	queryCoreRows(t, rows, err, []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}}, false)
}
