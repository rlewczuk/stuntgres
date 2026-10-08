package stuntgres_test

import (
	"stuntgres"
	"testing"
)

func TestUpdateCoreBindOccurrenceResolution(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	execSQL(t, db, "CREATE TABLE t(a INT,b INT,f BOOL)")
	execSQL(t, db, "INSERT INTO t VALUES(1,2,TRUE)")
	stmt, err := db.Prepare("UPDATE t SET a=$1,b=$1")
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	_, err = db.Exec("UPDATE t SET a=$1,b=$1", "bad")
	typeCoreDiagnostic(t, err, stuntgres.Error{Code: "22P02", Message: `invalid input syntax for type integer: "bad"`,Context:"unnamed portal parameter $1 = '...'"})
	_, err = stmt.Exec("bad")
	typeCoreDiagnostic(t, err, stuntgres.Error{Code: "22P02", Message: `invalid input syntax for type integer: "bad"`,Context:"unnamed portal parameter $1 = '...'"})
	wantTuples(t, db, "SELECT a,b,f FROM t", [][]any{{int64(1), int64(2), true}})
	for _, q := range []string{"UPDATE t SET a=$2", "UPDATE /*é*/ t SET a=$1,f=$1"} {
		want := stuntgres.Error{Code: "42P18", Message: "could not determine data type of parameter $1"}
		if q != "UPDATE t SET a=$2" {
			want = stuntgres.Error{Code: "42P08", Message: "inconsistent types deduced for parameter $1", Detail: "integer versus boolean", Position: "27"}
		}
		_, err = db.Exec(q)
		typeCoreDiagnostic(t, err, want)
		prepared, err := db.Prepare(q)
		if prepared != nil {
			prepared.Close()
			t.Fatal("invalid parameter signature prepared")
		}
		typeCoreDiagnostic(t, err, want)
	}
	execSQL(t, db, `CREATE SCHEMA "é space"`)
	execSQL(t, db, `CREATE TABLE "é space"."T""x"(id INT,"A" INT,b INT)`)
	execSQL(t, db, `INSERT INTO "é space"."T""x" VALUES(1,10,20),(2,30,40)`)
	q := `UPDATE "é space"."T""x" SET "A"=$1,b="A" WHERE id=$2`
	if _, err = db.Exec(q, 7, 1); err != nil {
		t.Fatal(err)
	}
	quoted, err := db.Prepare(q)
	if err != nil {
		t.Fatal(err)
	}
	defer quoted.Close()
	for _, args := range [][]any{{9, 1}, {5, 2}} {
		result, err := quoted.Exec(args...)
		if err != nil {
			t.Fatal(err)
		}
		if n, err := result.RowsAffected(); err != nil || n != 1 {
			t.Fatal(n, err)
		}
	}
	wantTuples(t, db, `SELECT id,"A",b FROM "é space"."T""x" ORDER BY id`, [][]any{{int64(1), int64(9), int64(7)}, {int64(2), int64(5), int64(30)}})
}
