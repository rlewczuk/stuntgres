package stuntgres_test

import (
	"stuntgres"
	"testing"
)

func TestQueryOrderMetadataAndPreparedFreshness(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	execSQL(t, db, "CREATE TABLE t(id INT NOT NULL,a INT,b INT)")
	execSQL(t, db, "INSERT INTO t VALUES(1,2,1),(2,1,5),(3,NULL,2),(4,2,0)")
	rows, err := db.Query("SELECT * FROM t ORDER BY 3,1")
	queryCoreRows(t, rows, err, []string{"id", "a", "b"}, []string{"INT4", "INT4", "INT4"}, [][]any{{int64(4), int64(2), int64(0)}, {int64(1), int64(2), int64(1)}, {int64(3), nil, int64(2)}, {int64(2), int64(1), int64(5)}}, false)
	rows, err = db.Query(`SELECT id AS "Mixed",a+b AS k FROM t ORDER BY k,"Mixed"`)
	if err != nil {
		t.Fatal(err)
	}
	metadata, err := rows.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	if nullable, known := metadata[0].Nullable(); nullable || !known {
		t.Fatal("alias lost physical NOT NULL")
	}
	if _, known := metadata[1].Nullable(); known {
		t.Fatal("computed key invented nullability")
	}
	rows.Close()
	execSQL(t, db, "CREATE SEQUENCE s START WITH 1")
	stmt, err := db.Prepare("SELECT nextval('s') AS n FROM t WHERE id<4 ORDER BY n DESC")
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	for _, want := range [][][]any{{{int64(3)}, {int64(2)}, {int64(1)}}, {{int64(6)}, {int64(5)}, {int64(4)}}} {
		rows, err := stmt.Query()
		queryCoreRows(t, rows, err, []string{"n"}, []string{"INT8"}, want, false)
	}
	var next int64
	if err := db.QueryRow("SELECT nextval('s')").Scan(&next); err != nil || next != 7 {
		t.Fatal(next, err)
	}
	execSQL(t, db, "CREATE TABLE d(n BIGINT)")
	execSQL(t, db, "INSERT INTO d SELECT nextval('s') AS n FROM t WHERE id<4 ORDER BY n DESC")
	rows, err = db.Query("SELECT n FROM d ORDER BY n DESC")
	queryCoreRows(t, rows, err, []string{"n"}, []string{"INT8"}, [][]any{{int64(10)}, {int64(9)}, {int64(8)}}, false)
	if err := db.QueryRow("SELECT nextval('s')").Scan(&next); err != nil || next != 11 {
		t.Fatal(next, err)
	}
}
