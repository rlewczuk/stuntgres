package stuntgres_test

import (
	"stuntgres"
	"testing"
)

func TestUpdateCoreFloatIntegerAssignments(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	execSQL(t, db, "CREATE TABLE t(id INT,f DOUBLE PRECISION,n SMALLINT,i INT,b BIGINT)")
	execSQL(t, db, "INSERT INTO t VALUES(1,2.5,1,1,1),(2,-2.5,1,1,1),(3,3.5,1,1,1),(4,-3.5,1,1,1)")
	stmt, err := db.Prepare("UPDATE t SET n=f,i=f,b=f")
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	result, err := stmt.Exec()
	if err != nil {
		t.Fatal(err)
	}
	if n, e := result.RowsAffected(); e != nil || n != 4 {
		t.Fatal(n, e)
	}
	wantTuples(t, db, "SELECT id,n,i,b FROM t ORDER BY id", [][]any{{int64(1), int64(2), int64(2), int64(2)}, {int64(2), int64(-2), int64(-2), int64(-2)}, {int64(3), int64(4), int64(4), int64(4)}, {int64(4), int64(-4), int64(-4), int64(-4)}})
	execSQL(t, db, "UPDATE t SET n=1")
	for _, input := range []string{"32767.5", "NaN", "Infinity", "-Infinity"} {
		execSQL(t, db, "UPDATE t SET f='"+input+"' WHERE id=4")
		_, err := db.Exec("UPDATE t SET n=f")
		typeCoreDiagnostic(t, err, stuntgres.Error{Code: "22003", Message: "smallint out of range"})
		wantTuples(t, db, "SELECT n FROM t ORDER BY id", [][]any{{int64(1)}, {int64(1)}, {int64(1)}, {int64(1)}})
	}
	// INSERT's existing NUMERIC literals continue rounding ties away from zero;
	// the new float source edge must not change their conversion provider.
	execSQL(t, db, "CREATE TABLE inserted(n INT)")
	execSQL(t, db, "INSERT INTO inserted VALUES(2.5),(-2.5)")
	wantTuples(t, db, "SELECT n FROM inserted ORDER BY n", [][]any{{int64(-3)}, {int64(3)}})
}
