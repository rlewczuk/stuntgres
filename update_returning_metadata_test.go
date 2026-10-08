package stuntgres_test

import (
	"reflect"
	"stuntgres"
	"testing"
)

func TestUpdateReturningPreparedMetadata(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	execSQL(t, db, "CREATE TABLE t(id INT NOT NULL,n NUMERIC(8,2),v VARCHAR(7),ts TIMESTAMP(3),b BYTEA)")
	execSQL(t, db, "INSERT INTO t VALUES(1,12.30,'value','2024-01-02 03:04:05.123','\\x0102')")
	held, err := db.Prepare("UPDATE t SET b=$1 WHERE id=$2 RETURNING old.id,new.id,old.n,new.n,old.v,new.v,old.ts,new.ts,old.b,new.b")
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	for _, id := range []int{99, 1, 1} {
		input := []byte{3, 4}
		rows, err := held.Query(input, id)
		if err != nil {
			t.Fatal(err)
		}
		input[0] = 99
		names, err := rows.Columns()
		if err != nil || !reflect.DeepEqual(names, []string{"id", "id", "n", "n", "v", "v", "ts", "ts", "b", "b"}) {
			t.Fatal(names, err)
		}
		columns, err := rows.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		for i, typ := range []string{"INT4", "INT4", "NUMERIC", "NUMERIC", "VARCHAR", "VARCHAR", "TIMESTAMP", "TIMESTAMP", "BYTEA", "BYTEA"} {
			if columns[i].DatabaseTypeName() != typ {
				t.Fatal(i, columns[i])
			}
		}
		for _, i := range []int{0, 1} {
			if nullable, known := columns[i].Nullable(); !known || nullable {
				t.Fatal("real version lost NOT NULL", i, nullable, known)
			}
		}
		for _, i := range []int{2, 3} {
			if precision, scale, known := columns[i].DecimalSize(); !known || precision != 8 || scale != 2 {
				t.Fatal(i, precision, scale, known)
			}
		}
		for _, i := range []int{4, 5} {
			if length, known := columns[i].Length(); !known || length != 7 {
				t.Fatal(i, length, known)
			}
		}
		if id == 99 {
			if rows.Next() {
				t.Fatal("no-match row")
			}
		} else {
			if !rows.Next() {
				t.Fatal(rows.Err())
			}
			dst := make([]any, 10)
			pointers := make([]any, 10)
			for i := range dst {
				pointers[i] = &dst[i]
			}
			if err = rows.Scan(pointers...); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(dst[9], []byte{3, 4}) {
				t.Fatal(dst)
			}
			dst[9].([]byte)[0] = 88
			if rows.Next() {
				t.Fatal("extra row")
			}
		}
		if err = rows.Err(); err != nil {
			t.Fatal(err)
		}
		rows.Close()
	}
	rows, err := held.Query(nil, 1)
	if err != nil {
		t.Fatal(err)
	}
	if !rows.Next() {
		t.Fatal(rows.Err())
	}
	dst := make([]any, 10)
	pointers := make([]any, 10)
	for i := range dst {
		pointers[i] = &dst[i]
	}
	if err = rows.Scan(pointers...); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(dst[8], []byte{3, 4}) || dst[9] != nil {
		t.Fatal(dst)
	}
	rows.Close()
	rows, err = db.Query("SELECT b FROM t")
	queryCoreRows(t, rows, err, []string{"b"}, []string{"BYTEA"}, [][]any{{nil}}, false)
}

func TestUpdateReturningPreflightEmpty(t *testing.T) {
	for _, populate := range []bool{false, true} {
		db := openLocal(t, stuntgres.NewDriver(), t.Name())
		execSQL(t, db, "CREATE TABLE t(id INT)")
		if populate {
			execSQL(t, db, "INSERT INTO t VALUES(1)")
		}
		held, err := db.Prepare("UPDATE t SET id=2 WHERE FALSE RETURNING CAST($1::TEXT AS INTEGER)")
		if err != nil {
			t.Fatal(err)
		}
		rows, err := held.Query("bad")
		if rows != nil {
			rows.Close()
			t.Fatal("preflight failure cursor")
		}
		typeCoreDiagnostic(t, err, stuntgres.Error{Code: "22P02", Message: `invalid input syntax for type integer: "bad"`})
		rows, err = held.Query("7")
		queryCoreRows(t, rows, err, []string{"int4"}, []string{"INT4"}, [][]any{}, false)
		held.Close()
		db.Close()
	}
}
