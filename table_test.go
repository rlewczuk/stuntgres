package stuntgres_test

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"reflect"
	"stuntgres"
	"testing"
)

func tableCoreExec(t *testing.T, db *sql.DB, prepared bool, q string, count int64, args ...any) {
	t.Helper()
	var result sql.Result
	var err error
	if prepared {
		s, e := db.Prepare(q)
		if e != nil {
			t.Fatal(q, e)
		}
		defer s.Close()
		result, err = s.Exec(args...)
	} else {
		result, err = db.Exec(q, args...)
	}
	if err != nil {
		t.Fatal(q, err)
	}
	got, err := result.RowsAffected()
	if err != nil || got != count {
		t.Fatal(q, got, err)
	}
}
func tableCoreMetadata(t *testing.T, db *sql.DB, prepared bool, q string, names, types []string, nullable []bool) {
	t.Helper()
	var rows *sql.Rows
	var err error
	if prepared {
		s, e := db.Prepare(q)
		if e != nil {
			t.Fatal(e)
		}
		defer s.Close()
		rows, err = s.Query()
	} else {
		rows, err = db.Query(q)
	}
	if err != nil {
		t.Fatal(q, err)
	}
	defer rows.Close()
	got, err := rows.Columns()
	if err != nil || !reflect.DeepEqual(got, names) {
		t.Fatal(got, err, names)
	}
	metadata, err := rows.ColumnTypes()
	if err != nil || len(metadata) != len(types) {
		t.Fatal(metadata, err)
	}
	for i, n := range types {
		c := metadata[i]
		v, known := c.Nullable()
		var scan reflect.Type
		switch n {
		case "INT4", "INT8":
			scan = reflect.TypeFor[int64]()
		case "TEXT":
			scan = reflect.TypeFor[string]()
		case "BOOL":
			scan = reflect.TypeFor[bool]()
		}
		if c.DatabaseTypeName() != n || c.ScanType() != scan || !known || v != nullable[i] {
			t.Fatal(i, c, v, known)
		}
	}
	if rows.Next() || rows.Err() != nil {
		t.Fatal("expected empty shape", rows.Err())
	}
}
func TestTableCoreDirectPreparedShapeAndDiagnostics(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "prepared"}[prepared], func(t *testing.T) {
			db := openLocal(t, stuntgres.NewDriver(), t.Name())
			exec := func(q string, count int64, args ...any) { tableCoreExec(t, db, prepared, q, count, args...) }
			exec("CREATE TABLE source(i INTEGER NOT NULL,s TEXT,f BOOLEAN)", 0)
			exec("INSERT INTO source VALUES($1,$2,$3)", 1, 7, "O'Brien", true)
			exec("CREATE TABLE clone(lead BIGINT,LIKE source,tail TEXT)", 0)
			tableCoreMetadata(t, db, prepared, "SELECT * FROM clone", []string{"lead", "i", "s", "f", "tail"}, []string{"INT8", "INT4", "TEXT", "BOOL", "TEXT"}, []bool{true, false, true, true, true})
			exec("INSERT INTO clone VALUES($1,$2,$3,$4,$5)", 1, int64(9223372036854775807), 7, "O'Brien", true, "tail")
			wantTuples(t, db, "SELECT * FROM clone", [][]any{{int64(9223372036854775807), int64(7), "O'Brien", true, "tail"}})
			exec("CREATE TABLE z()", 0)
			exec("CREATE TABLE zl(LIKE z)", 0)
			for _, n := range []string{"z", "zl"} {
				tableCoreMetadata(t, db, prepared, "SELECT * FROM "+n, []string{}, []string{}, []bool{})
			}
			for _, tc := range []struct {
				q    string
				want stuntgres.Error
			}{
				{"CREATE TABLE bad (id no_such_type)", stuntgres.Error{Code: "42704", Message: `type "no_such_type" does not exist`, Position: "22"}},
				{"CREATE TABLE missingclone (LIKE missing)", stuntgres.Error{Code: "42P01", Message: `relation "missing" does not exist`, Position: "33"}},
				{"CREATE TABLE bad(LIKE source,i TEXT)", stuntgres.Error{Code: "42701", Message: `column "i" specified more than once`}},
				{"CREATE TABLE bad(ctid INT)", stuntgres.Error{Code: "42701", Message: `column name "ctid" conflicts with a system column name`}},
				{"DROP TABLE source,missing,clone", stuntgres.Error{Code: "42P01", Message: `table "missing" does not exist`}},
				{"INSERT INTO clone(i) VALUES(NULL)", stuntgres.Error{Code: "23502", Message: `null value in column "i" of relation "clone" violates not-null constraint`, Detail: "Failing row contains (null, null, null, null, null).", Schema: "public", Table: "clone", Column: "i"}},
			} {
				var err error
				if prepared {
					s, e := db.Prepare(tc.q)
					if e != nil {
						t.Fatal("utility preparation", e)
					}
					_, err = s.Exec()
					s.Close()
				} else {
					_, err = db.Exec(tc.q)
				}
				typeCoreDiagnostic(t, err, tc.want)
			}
			wantTuples(t, db, "SELECT * FROM source", [][]any{{int64(7), "O'Brien", true}})
			exec("DROP TABLE source RESTRICT", 0)
			wantTuples(t, db, "SELECT * FROM clone", [][]any{{int64(9223372036854775807), int64(7), "O'Brien", true, "tail"}})
			exec("DROP TABLE clone,public.clone,z,zl CASCADE", 0)
		})
	}
}
func TestTableCoreCursorPreparedRebindingAndTransactionBoundary(t *testing.T) {
	d := stuntgres.NewDriver()
	db := openLocal(t, d, t.Name())
	execSQL(t, db, "CREATE TABLE t(id INT)")
	execSQL(t, db, "INSERT INTO t VALUES(11),(22)")
	prepared, err := db.Prepare("SELECT id FROM t ORDER BY id")
	if err != nil {
		t.Fatal(err)
	}
	defer prepared.Close()
	raw, err := d.Open(t.Name())
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	held, err := raw.(driver.QueryerContext).QueryContext(context.Background(), "SELECT id FROM t ORDER BY id", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	execSQL(t, db, "DROP TABLE t")
	rows, err := prepared.Query()
	if rows != nil {
		rows.Close()
	}
	sqlState(t, err, "42P01")
	execSQL(t, db, "CREATE TABLE t(other TEXT)")
	execSQL(t, db, "INSERT INTO t VALUES('fresh')")
	rows, err = prepared.Query()
	if rows != nil {
		rows.Close()
	}
	sqlState(t, err, "42703")
	if !reflect.DeepEqual(held.Columns(), []string{"id"}) || held.(driver.RowsColumnTypeDatabaseTypeName).ColumnTypeDatabaseTypeName(0) != "INT4" {
		t.Fatal("retained metadata changed")
	}
	values := make([]driver.Value, 1)
	for _, n := range []int64{11, 22} {
		if err = held.Next(values); err != nil || values[0] != n {
			t.Fatal(values, err)
		}
	}
	if err = held.Next(values); err != io.EOF {
		t.Fatal(err)
	}
	wantTuples(t, db, "SELECT * FROM t", [][]any{{"fresh"}})
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	_, err = tx.Exec("DROP TABLE t")
	sqlState(t, err, "0A000")
	tx.Rollback()
	wantTuples(t, db, "SELECT * FROM t", [][]any{{"fresh"}})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = db.ExecContext(ctx, "DROP TABLE t")
	if !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	wantTuples(t, db, "SELECT * FROM t", [][]any{{"fresh"}})
}

func TestTableCoreQuotedTypesPublicDirectPrepared(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		db := openLocal(t, stuntgres.NewDriver(), t.Name())
		tableCoreExec(t, db, prepared, `CREATE TABLE raw(i "int4" NOT NULL,b "int8",s "text",f "bool")`, 0)
		tableCoreMetadata(t, db, prepared, "SELECT * FROM raw", []string{"i", "b", "s", "f"}, []string{"INT4", "INT8", "TEXT", "BOOL"}, []bool{false, true, true, true})
		for _, name := range []string{"integer", "int", "bigint", "boolean", "smallint", "real", "decimal", "INTEGER", "INT4", "SERIAL"} {
			q := `CREATE TABLE bad (id "` + name + `")`
			var err error
			if prepared {
				s, e := db.Prepare(q)
				if e != nil {
					t.Fatal("utility preparation", e)
				}
				_, err = s.Exec()
				s.Close()
			} else {
				_, err = db.Exec(q)
			}
			typeCoreDiagnostic(t, err, stuntgres.Error{Code: "42704", Message: `type "` + name + `" does not exist`, Position: "22"})
		}
		for _, tc := range []struct{ q, message string }{{`CREATE TABLE bad (id "a""b")`, `type "a"b" does not exist`}, {`CREATE TABLE bad (id "a\b")`, `type "a\b" does not exist`}} {
			var err error
			if prepared {
				s, e := db.Prepare(tc.q)
				if e != nil {
					t.Fatal("utility preparation", e)
				}
				_, err = s.Exec()
				s.Close()
			} else {
				_, err = db.Exec(tc.q)
			}
			typeCoreDiagnostic(t, err, stuntgres.Error{Code: "42704", Message: tc.message, Position: "22"})
		}
		tableCoreExec(t, db, prepared, `CREATE TABLE IF NOT EXISTS raw(id "integer")`, 0)
		tableCoreMetadata(t, db, prepared, "SELECT * FROM raw", []string{"i", "b", "s", "f"}, []string{"INT4", "INT8", "TEXT", "BOOL"}, []bool{false, true, true, true})
	}
}

func TestTableCoreQuotedDuplicateRelationDirectPrepared(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		for _, tc := range []struct{ identifier, message string }{
			{`"a""b"`, `relation "a"b" already exists`},
			{`"a\b"`, `relation "a\b" already exists`},
		} {
			t.Run(map[bool]string{false: "direct", true: "prepared"}[prepared]+"/"+tc.identifier, func(t *testing.T) {
				db := openLocal(t, stuntgres.NewDriver(), t.Name())
				tableCoreExec(t, db, prepared, "CREATE TABLE "+tc.identifier+"(id INT NOT NULL)", 0)
				tableCoreExec(t, db, prepared, "INSERT INTO "+tc.identifier+" VALUES(11)", 1)
				q := "CREATE TABLE " + tc.identifier + "(lost TEXT)"
				var err error
				if prepared {
					stmt, prepareErr := db.Prepare(q)
					if prepareErr != nil {
						t.Fatal("duplicate utility failed during preparation", prepareErr)
					}
					_, err = stmt.Exec()
					stmt.Close()
				} else {
					_, err = db.Exec(q)
				}
				typeCoreDiagnostic(t, err, stuntgres.Error{Code: "42P07", Message: tc.message})
				rows, err := db.Query("SELECT * FROM " + tc.identifier)
				if err != nil {
					t.Fatal(err)
				}
				defer rows.Close()
				names, err := rows.Columns()
				if err != nil || !reflect.DeepEqual(names, []string{"id"}) {
					t.Fatal(names, err)
				}
				columns, err := rows.ColumnTypes()
				if err != nil || len(columns) != 1 {
					t.Fatal(columns, err)
				}
				nullable, known := columns[0].Nullable()
				if columns[0].DatabaseTypeName() != "INT4" || columns[0].ScanType() != reflect.TypeFor[int64]() || !known || nullable {
					t.Fatal(columns[0], nullable, known)
				}
				if !rows.Next() {
					t.Fatal("winner row missing", rows.Err())
				}
				var id int64
				if err := rows.Scan(&id); err != nil || id != 11 {
					t.Fatal(id, err)
				}
				if rows.Next() || rows.Err() != nil {
					t.Fatal("winner rows changed", rows.Err())
				}
			})
		}
	}
}
