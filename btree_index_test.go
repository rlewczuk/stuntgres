package stuntgres_test

import (
	"testing"

	"stuntgres"
)

func TestBtreeIndexRegression(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	execSQL(t, db, "CREATE TABLE t(n INTEGER,s TEXT)")
	execSQL(t, db, "INSERT INTO t VALUES(2,'b'),(1,'a'),(2,'b'),(NULL,NULL),(3,'c')")
	execSQL(t, db, "CREATE INDEX t_n ON t(n)")
	stmt, err := db.Prepare("CREATE INDEX t_s ON t USING btree(s)")
	if err != nil {
		t.Fatal(err)
	}
	result, err := stmt.Exec()
	stmt.Close()
	if err != nil {
		t.Fatal(err)
	}
	if count, err := result.RowsAffected(); err != nil || count != 0 {
		t.Fatal(count, err)
	}
	for _, tc := range []struct {
		query string
		rows  [][]any
		bag   bool
	}{
		{"SELECT n,s FROM t WHERE n=2", [][]any{{int64(2), "b"}, {int64(2), "b"}}, true},
		{"SELECT n,s FROM t WHERE n>=2", [][]any{{int64(2), "b"}, {int64(2), "b"}, {int64(3), "c"}}, true},
		{"SELECT n,s FROM t WHERE n IS NULL", [][]any{{nil, nil}}, true},
		{"SELECT n,s FROM t WHERE s>'a'", [][]any{{int64(2), "b"}, {int64(2), "b"}, {int64(3), "c"}}, true},
		{"SELECT n,s FROM t ORDER BY n ASC NULLS LAST", [][]any{{int64(1), "a"}, {int64(2), "b"}, {int64(2), "b"}, {int64(3), "c"}, {nil, nil}}, false},
		{"SELECT n,s FROM t ORDER BY n DESC", [][]any{{nil, nil}, {int64(3), "c"}, {int64(2), "b"}, {int64(2), "b"}, {int64(1), "a"}}, false},
	} {
		rows, err := db.Query(tc.query)
		queryCoreRows(t, rows, err, []string{"n", "s"}, []string{"INT4", "TEXT"}, tc.rows, tc.bag)
	}
	query, err := db.Prepare("SELECT n,s FROM t WHERE n=$1")
	if err != nil {
		t.Fatal(err)
	}
	defer query.Close()
	rows, err := query.Query(2)
	queryCoreRows(t, rows, err, []string{"n", "s"}, []string{"INT4", "TEXT"}, [][]any{{int64(2), "b"}, {int64(2), "b"}}, true)
	for _, tc := range []struct{ query, code, message string }{
		{"CREATE INDEX t_n ON t(n)", "42P07", `relation "t_n" already exists`},
		{"CREATE INDEX retry ON missing(n)", "42P01", `relation "missing" does not exist`},
		{"CREATE INDEX retry ON t(missing)", "42703", `column "missing" does not exist`},
	} {
		_, err := db.Exec(tc.query)
		typeCoreDiagnostic(t, err, stuntgres.Error{Code: tc.code, Message: tc.message})
		prepared, err := db.Prepare(tc.query)
		if err != nil {
			t.Fatal("semantic error at Prepare", err)
		}
		_, err = prepared.Exec()
		prepared.Close()
		typeCoreDiagnostic(t, err, stuntgres.Error{Code: tc.code, Message: tc.message})
	}
	execSQL(t, db, "CREATE INDEX retry ON t(n)")
}
