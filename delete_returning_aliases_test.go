package stuntgres_test

import (
	"database/sql"
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
		{"DELETE FROM t AS d WHERE d.v IS NULL RETURNING id", []string{"id"}, []string{"INT4"}, []any{int64(2)}},
		{"DELETE FROM t RETURNING CAST(COALESCE(new.id,old.id) AS BIGINT) AS kept,int8(old.id) AS wide,new.id::BIGINT AS gone", []string{"kept", "wide", "gone"}, []string{"INT8", "INT8", "INT8"}, []any{int64(2), int64(2), nil}},
		{"DELETE FROM t RETURNING WITH (OLD AS o,NEW AS n) CAST(o.id AS BIGINT) AS predecessor,CAST(n.id AS BIGINT) AS replacement", []string{"predecessor", "replacement"}, []string{"INT8", "INT8"}, []any{int64(2), nil}},
		{"DELETE FROM t RETURNING NULL::regclass AS predecessor,NULL::regclass AS replacement", []string{"predecessor", "replacement"}, []string{"REGCLASS", "REGCLASS"}, []any{nil, nil}},
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
			stmt, err := db.Prepare(tc.query)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			execSQL(t, db, "INSERT INTO t VALUES(2,NULL),(2,NULL)")
			rows, err = stmt.Query()
			queryCoreRows(t, rows, err, tc.names, tc.types, [][]any{tc.want, tc.want}, true)
			rows, err = stmt.Query()
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

func TestDeleteReturningCommentSnapshot(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		db := openLocal(t, stuntgres.NewDriver(), t.Name())
		execSQL(t, db, "CREATE TABLE t(id INT)")
		execSQL(t, db, "INSERT INTO t VALUES(1)")
		execSQL(t, db, "COMMENT ON TABLE t IS 'committed table'")
		execSQL(t, db, "COMMENT ON COLUMN t.id IS 'committed column'")
		tx, err := db.Begin()
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		query := "DELETE FROM t RETURNING id,col_description('t'::regclass,1) AS column_comment,obj_description('t'::regclass,'pg_class') AS table_comment"
		if prepared {
			stmt, err := tx.Prepare(query)
			if err != nil {
				t.Fatal(err)
			}
			rows, err := stmt.Query()
			queryCoreRows(t, rows, err, []string{"id", "column_comment", "table_comment"}, []string{"INT4", "TEXT", "TEXT"}, [][]any{{int64(1), "committed column", "committed table"}}, false)
			stmt.Close()
		} else {
			rows, err := tx.Query(query)
			queryCoreRows(t, rows, err, []string{"id", "column_comment", "table_comment"}, []string{"INT4", "TEXT", "TEXT"}, [][]any{{int64(1), "committed column", "committed table"}}, false)
		}
		if err = tx.Rollback(); err != nil {
			t.Fatal(err)
		}
		rows, err := db.Query("SELECT id,col_description('t'::regclass,1) AS column_comment,obj_description('t'::regclass,'pg_class') AS table_comment FROM t")
		queryCoreRows(t, rows, err, []string{"id", "column_comment", "table_comment"}, []string{"INT4", "TEXT", "TEXT"}, [][]any{{int64(1), "committed column", "committed table"}}, false)
		db.Close()
	}
}

func TestDeleteReturningCrossKindAliases(t *testing.T) {
	for _, tc := range []struct {
		options, output string
		want            []any
		nullable        []bool
	}{
		{"OLD AS new", "new.id AS alias_id,new.v AS alias_v", []any{int64(2), "payload", int64(2), "payload"}, []bool{false, false, false, false}},
		{"NEW AS old", "old.id AS alias_id,old.v AS alias_v", []any{int64(2), "payload", nil, nil}, []bool{false, false, true, true}},
		{"OLD AS new,NEW AS n", "new.id AS old_id,new.v AS old_v,n.id AS new_id,n.v AS new_v", []any{int64(2), "payload", int64(2), "payload", nil, nil}, []bool{false, false, false, false, true, true}},
		{"NEW AS old,OLD AS o", "o.id AS old_id,o.v AS old_v,old.id AS new_id,old.v AS new_v", []any{int64(2), "payload", int64(2), "payload", nil, nil}, []bool{false, false, false, false, true, true}},
		{"OLD AS new,NEW AS old", "new.id AS old_id,new.v AS old_v,old.id AS new_id,old.v AS new_v", []any{int64(2), "payload", int64(2), "payload", nil, nil}, []bool{false, false, false, false, true, true}},
		{"NEW AS old,OLD AS new", "new.id AS old_id,new.v AS old_v,old.id AS new_id,old.v AS new_v", []any{int64(2), "payload", int64(2), "payload", nil, nil}, []bool{false, false, false, false, true, true}},
	} {
		for _, prepared := range []bool{false, true} {
			db := openLocal(t, stuntgres.NewDriver(), t.Name())
			execSQL(t, db, "CREATE TABLE t(id INT NOT NULL,v TEXT NOT NULL)")
			execSQL(t, db, "INSERT INTO t VALUES(1,'survivor'),(2,'payload'),(2,'payload')")
			query := "DELETE FROM t WHERE id=2 RETURNING WITH (" + tc.options + ") id AS target_id,v AS target_v," + tc.output
			names := []string{"target_id", "target_v", "alias_id", "alias_v"}
			types := []string{"INT4", "TEXT", "INT4", "TEXT"}
			if len(tc.want) == 6 {
				names = []string{"target_id", "target_v", "old_id", "old_v", "new_id", "new_v"}
				types = []string{"INT4", "TEXT", "INT4", "TEXT", "INT4", "TEXT"}
			}
			var held *sql.Stmt
			if prepared {
				var err error
				held, err = db.Prepare(query)
				if err != nil {
					t.Fatal(tc.options, err)
				}
			}
			for step, want := range [][][]any{{tc.want, tc.want}, {}, {tc.want, tc.want}} {
				if step == 2 {
					execSQL(t, db, "INSERT INTO t VALUES(2,'payload'),(2,'payload')")
				}
				var rows *sql.Rows
				var err error
				if held != nil {
					rows, err = held.Query()
				} else {
					rows, err = db.Query(query)
				}
				if err != nil {
					t.Fatal(tc.options, prepared, err)
				}
				meta, err := rows.ColumnTypes()
				if err != nil || len(meta) != len(tc.nullable) {
					t.Fatal(meta, err)
				}
				for i, wantNullable := range tc.nullable {
					if n, k := meta[i].Nullable(); !k || n != wantNullable {
						t.Fatal(tc.options, i, n, k, wantNullable)
					}
				}
				queryCoreRows(t, rows, nil, names, types, want, true)
			}
			if held != nil {
				held.Close()
			}
			rows, err := db.Query("SELECT id,v FROM t")
			queryCoreRows(t, rows, err, []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "survivor"}}, false)
			db.Close()
		}
	}
	for _, tc := range []struct {
		query string
		want  stuntgres.Error
	}{
		{"DELETE FROM t RETURNING WITH (OLD AS new) old.id", stuntgres.Error{Code: "42P01", Message: `missing FROM-clause entry for table "old"`, Position: "43"}},
		{"DELETE FROM t RETURNING WITH (NEW AS old) new.id", stuntgres.Error{Code: "42P01", Message: `missing FROM-clause entry for table "new"`, Position: "43"}},
		{"DELETE FROM t RETURNING WITH (OLD AS new, NEW AS new) id", stuntgres.Error{Code: "42712", Message: `table name "new" specified more than once`, Position: "43"}},
	} {
		db := openLocal(t, stuntgres.NewDriver(), t.Name())
		execSQL(t, db, "CREATE TABLE t(id INT NOT NULL,v TEXT NOT NULL)")
		execSQL(t, db, "INSERT INTO t VALUES(2,'payload'),(2,'payload')")
		rows, err := db.Query(tc.query)
		if rows != nil {
			rows.Close()
		}
		typeCoreDiagnostic(t, err, tc.want)
		held, err := db.Prepare(tc.query)
		if held != nil {
			held.Close()
		}
		typeCoreDiagnostic(t, err, tc.want)
		rows, err = db.Query("SELECT id,v FROM t")
		queryCoreRows(t, rows, err, []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(2), "payload"}, {int64(2), "payload"}}, true)
		db.Close()
	}
}

func TestDeleteReturningCurrentSchemasMetadata(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	execSQL(t, db, "CREATE TABLE t(id INT)")
	held, err := db.Prepare("DELETE FROM t RETURNING current_schemas(NULL) AS schemas")
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	for _, prepared := range []bool{false, true} {
		execSQL(t, db, "INSERT INTO t VALUES(1)")
		for _, want := range [][][]any{{{nil}}, {}} {
			var rows *sql.Rows
			if prepared {
				rows, err = held.Query()
			} else {
				rows, err = db.Query("DELETE FROM t RETURNING current_schemas(NULL) AS schemas")
			}
			if err != nil {
				t.Fatal(err)
			}
			meta, err := rows.ColumnTypes()
			if err != nil || len(meta) != 1 {
				t.Fatal(meta, err)
			}
			if n, k := meta[0].Nullable(); !k || !n {
				t.Fatal(n, k)
			}
			queryCoreRows(t, rows, nil, []string{"schemas"}, []string{"_NAME"}, want, false)
		}
	}
}
