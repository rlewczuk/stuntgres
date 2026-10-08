package stuntgres_test

import (
	"stuntgres"
	"testing"
)

func TestDeleteReturningAliases(t *testing.T) {
	for _, tc := range []struct {
		query        string
		names, types []string
		want         []any
	}{
		{"DELETE FROM t RETURNING *,old.*,new.*,id+10 AS bumped,new.id IS NULL AS gone,COALESCE(new.id,old.id) AS kept", []string{"id", "v", "id", "v", "id", "v", "bumped", "gone", "kept"}, []string{"INT4", "TEXT", "INT4", "TEXT", "INT4", "TEXT", "INT4", "BOOL", "INT4"}, []any{int64(2), nil, int64(2), nil, nil, nil, int64(12), true, int64(2)}},
		{"DELETE FROM t RETURNING WITH (NEW AS n,OLD AS o) o.id,n.id,COALESCE(n.v,'fallback') AS label", []string{"id", "id", "label"}, []string{"INT4", "INT4", "TEXT"}, []any{int64(2), nil, "fallback"}},
		{`DELETE FROM t RETURNING WITH (OLD AS "O") "O".id,new.id`, []string{"id", "id"}, []string{"INT4", "INT4"}, []any{int64(2), nil}},
		{"DELETE FROM t AS new RETURNING new.id,id", []string{"id", "id"}, []string{"INT4", "INT4"}, []any{int64(2), int64(2)}},
		{"DELETE FROM t AS new RETURNING WITH (NEW AS n) new.id,n.id", []string{"id", "id"}, []string{"INT4", "INT4"}, []any{int64(2), nil}},
		{"DELETE FROM t RETURNING WITH (OLD AS new) new.id", []string{"id"}, []string{"INT4"}, []any{int64(2)}},
		{"DELETE FROM t RETURNING WITH (NEW AS old) old.id", []string{"id"}, []string{"INT4"}, []any{nil}},
		{"DELETE FROM public.t RETURNING public.t.id,CAST(old.id AS BIGINT),new.id::BIGINT", []string{"id", "id", "id"}, []string{"INT4", "INT8", "INT8"}, []any{int64(2), int64(2), nil}},
		{"DELETE FROM t RETURNING CASE WHEN new.id IS NULL THEN 'ok' ELSE v END AS label,abs(old.id),old.id IS DISTINCT FROM new.id", []string{"label", "abs", "?column?"}, []string{"TEXT", "INT4", "BOOL"}, []any{"ok", int64(2), true}},
	} {
		t.Run(tc.query, func(t *testing.T) {
			db := openLocal(t, stuntgres.NewDriver(), t.Name())
			execSQL(t, db, "CREATE TABLE t(id INT NOT NULL,v TEXT)")
			execSQL(t, db, "INSERT INTO t VALUES(2,NULL),(2,NULL)")
			rows, err := db.Query(tc.query)
			queryCoreRows(t, rows, err, tc.names, tc.types, [][]any{tc.want, tc.want}, true)
			rows, err = db.Query(tc.query)
			queryCoreRows(t, rows, err, tc.names, tc.types, [][]any{}, true)
		})
	}
	t.Run("ordinary_old_new_columns", func(t *testing.T) {
		db := openLocal(t, stuntgres.NewDriver(), t.Name())
		execSQL(t, db, "CREATE TABLE old(old INT,new TEXT)")
		execSQL(t, db, "INSERT INTO old VALUES(7,'value')")
		rows, err := db.Query("DELETE FROM old RETURNING old.*,old,new,new.*")
		queryCoreRows(t, rows, err, []string{"old", "new", "old", "new", "old", "new"}, []string{"INT4", "TEXT", "INT4", "TEXT", "INT4", "TEXT"}, [][]any{{int64(7), "value", int64(7), "value", nil, nil}}, false)
	})
	t.Run("zero_columns", func(t *testing.T) {
		db := openLocal(t, stuntgres.NewDriver(), t.Name())
		execSQL(t, db, "CREATE TABLE t()")
		execSQL(t, db, "INSERT INTO t DEFAULT VALUES")
		rows, err := db.Query("DELETE FROM t RETURNING *,1 AS one")
		queryCoreRows(t, rows, err, []string{"one"}, []string{"INT4"}, [][]any{{int64(1)}}, false)
	})
}

func TestDeleteReturningPreparedMetadata(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	execSQL(t, db, "CREATE TABLE t(id INT NOT NULL,v TEXT,n NUMERIC(8,2),b BYTEA)")
	stmt, err := db.Prepare("DELETE FROM t WHERE id=$1 RETURNING old.id,new.id,COALESCE(new.id,$1) AS kept,$2 AS label,n,b")
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	execSQL(t, db, "INSERT INTO t VALUES(2,'a',12.30,'\\x0102')")
	rows, err := stmt.Query(2, "fresh")
	if err != nil {
		t.Fatal(err)
	}
	meta, err := rows.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	if nullable, known := meta[0].Nullable(); !known || nullable {
		t.Fatal("OLD id nullability", nullable, known)
	}
	if nullable, known := meta[1].Nullable(); !known || !nullable {
		t.Fatal("NEW id nullability", nullable, known)
	}
	if precision, scale, known := meta[4].DecimalSize(); !known || precision != 8 || scale != 2 {
		t.Fatal(precision, scale, known)
	}
	queryCoreRows(t, rows, nil, []string{"id", "id", "kept", "label", "n", "b"}, []string{"INT4", "INT4", "INT4", "TEXT", "NUMERIC", "BYTEA"}, [][]any{{int64(2), nil, int64(2), "fresh", "12.30", []byte{1, 2}}}, false)
	rows, err = stmt.Query(nil, nil)
	queryCoreRows(t, rows, err, []string{"id", "id", "kept", "label", "n", "b"}, []string{"INT4", "INT4", "INT4", "TEXT", "NUMERIC", "BYTEA"}, [][]any{}, false)
	execSQL(t, db, "INSERT INTO t VALUES(3,'b',NULL,NULL)")
	rows, err = stmt.Query(3, "next")
	queryCoreRows(t, rows, err, []string{"id", "id", "kept", "label", "n", "b"}, []string{"INT4", "INT4", "INT4", "TEXT", "NUMERIC", "BYTEA"}, [][]any{{int64(3), nil, int64(3), "next", nil, nil}}, false)
}
