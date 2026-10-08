package stuntgres_test

import (
	"stuntgres"
	"testing"
)

func TestUpdateReturningAliases(t *testing.T) {
	for _, tc := range []struct {
		options, output string
		want            []any
	}{
		{"", "*,old.*,new.*,t.*,public.t.*", []any{int64(1), int64(11), int64(1), int64(10), int64(1), int64(11), int64(1), int64(11), int64(1), int64(11)}},
		{`WITH (NEW AS n,OLD AS "O")`, `"O".a,n.a`, []any{int64(10), int64(11)}},
		{"WITH (OLD AS new)", "new.a,a", []any{int64(10), int64(11)}},
		{"WITH (NEW AS old)", "old.a,a", []any{int64(11), int64(11)}},
		{"WITH (OLD AS new,NEW AS old)", "new.a,old.a", []any{int64(10), int64(11)}},
		{"WITH (OLD AS o)", "o.a,new.a", []any{int64(10), int64(11)}},
		{"WITH (NEW AS n)", "old.a,n.a", []any{int64(10), int64(11)}},
	} {
		db := openLocal(t, stuntgres.NewDriver(), t.Name())
		execSQL(t, db, "CREATE TABLE t(id INT NOT NULL,a INT)")
		execSQL(t, db, "INSERT INTO t VALUES(1,10)")
		names := []string{"a", "a"}
		types := []string{"INT4", "INT4"}
		if tc.options == "" {
			names = []string{"id", "a", "id", "a", "id", "a", "id", "a", "id", "a"}
			types = []string{"INT4", "INT4", "INT4", "INT4", "INT4", "INT4", "INT4", "INT4", "INT4", "INT4"}
		}
		rows, err := db.Query("UPDATE t SET a=a+1 RETURNING " + tc.options + " " + tc.output)
		queryCoreRows(t, rows, err, names, types, [][]any{tc.want}, false)
		db.Close()
	}
	for _, name := range []string{"old", "new"} {
		db := openLocal(t, stuntgres.NewDriver(), t.Name())
		execSQL(t, db, "CREATE TABLE "+name+"(a INT,old INT,new INT)")
		execSQL(t, db, "INSERT INTO "+name+" VALUES(10,20,30)")
		rows, err := db.Query("UPDATE " + name + " SET a=a+1 RETURNING WITH (OLD AS o) " + name + ".a,o.a,old,new")
		queryCoreRows(t, rows, err, []string{"a", "a", "old", "new"}, []string{"INT4", "INT4", "INT4", "INT4"}, [][]any{{int64(11), int64(10), int64(20), int64(30)}}, false)
		db.Close()
	}
}

func TestUpdateReturningExpressions(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	execSQL(t, db, "CREATE TABLE t(a INT,v TEXT)")
	execSQL(t, db, "INSERT INTO t VALUES(-10,NULL)")
	rows, err := db.Query("UPDATE t SET a=11 RETURNING CAST(old.a AS BIGINT),new.a::BIGINT,abs(old.a),CASE WHEN old.a<0 THEN new.a ELSE old.a END,COALESCE(v,'fallback'),NULLIF(new.a,11),old.a IS DISTINCT FROM new.a,'literal',$1 AS \"Label\"", "parameter")
	queryCoreRows(t, rows, err, []string{"a", "a", "abs", "a", "coalesce", "nullif", "?column?", "?column?", "Label"}, []string{"INT8", "INT8", "INT4", "INT4", "TEXT", "INT4", "BOOL", "TEXT", "TEXT"}, [][]any{{int64(-10), int64(11), int64(10), int64(11), "fallback", nil, true, "literal", "parameter"}}, false)
}

func TestUpdateReturningParameterOrder(t *testing.T) {
	for _, q := range []string{"UPDATE t SET id=$1 WHERE id=$1 RETURNING $1 AS typed,$1::int4 AS repeated", "UPDATE t SET id=2 RETURNING $1::int4 AS typed,$1 AS repeated"} {
		db := openLocal(t, stuntgres.NewDriver(), t.Name())
		execSQL(t, db, "CREATE TABLE t(id INT)")
		execSQL(t, db, "INSERT INTO t VALUES(2),(2)")
		held, err := db.Prepare(q)
		if err != nil {
			t.Fatal(err)
		}
		for range 2 {
			rows, err := held.Query(2)
			queryCoreRows(t, rows, err, []string{"typed", "repeated"}, []string{"INT4", "INT4"}, [][]any{{int64(2), int64(2)}, {int64(2), int64(2)}}, true)
		}
		held.Close()
		db.Close()
	}
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	execSQL(t, db, "CREATE TABLE t(id INT,v TEXT)")
	execSQL(t, db, "INSERT INTO t VALUES(2,'2')")
	q := "UPDATE t SET id=$1 WHERE v=$1 RETURNING $1"
	_, err := db.Prepare(q)
	typeCoreDiagnostic(t, err, stuntgres.Error{Code: "42804", Message: `column "id" is of type integer but expression is of type text`, Hint: "You will need to rewrite or cast the expression.", Position: "17"})
	rows, err := db.Query("UPDATE t SET v=$1 WHERE id=$1 RETURNING old.v,new.v,$1", 2)
	queryCoreRows(t, rows, err, []string{"v", "v", "?column?"}, []string{"TEXT", "TEXT", "INT4"}, [][]any{{"2", "2", int64(2)}}, false)
}
