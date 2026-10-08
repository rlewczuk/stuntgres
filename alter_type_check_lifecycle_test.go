package stuntgres_test

import (
	"bytes"
	"fmt"
	"strings"
	"stuntgres"
	"testing"
)

func TestAlterTypeCheckPrepared(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	execSQL(t, db, "CREATE TABLE t(c INT CONSTRAINT ck CHECK(c>0))")
	execSQL(t, db, "INSERT INTO t VALUES(1)")
	conn, err := db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	insert, err := conn.PrepareContext(t.Context(), "INSERT INTO t VALUES($1)")
	if err != nil {
		t.Fatal(err)
	}
	defer insert.Close()
	selectOld, err := conn.PrepareContext(t.Context(), "SELECT c FROM t")
	if err != nil {
		t.Fatal(err)
	}
	defer selectOld.Close()
	eager, err := db.Query("SELECT c FROM t")
	if err != nil {
		t.Fatal(err)
	}
	defer eager.Close()
	if _, err = conn.ExecContext(t.Context(), "ALTER TABLE t ALTER c TYPE BIGINT"); err != nil {
		t.Fatal(err)
	}
	if _, err = insert.ExecContext(t.Context(), int64(2)); err != nil {
		t.Fatal(err)
	}
	_, err = insert.ExecContext(t.Context(), int64(-1))
	notNullDiagnostic(t, err, stuntgres.Error{Code: "23514", Message: `new row for relation "t" violates check constraint "ck"`, Detail: "Failing row contains (-1).", Schema: "public", Table: "t", Constraint: "ck"})
	rows, err := selectOld.QueryContext(t.Context())
	if rows != nil {
		rows.Close()
	}
	notNullDiagnostic(t, err, stuntgres.Error{Code: "0A000", Message: "cached plan must not change result type"})
	utility, err := conn.PrepareContext(t.Context(), "ALTER TABLE t ALTER c TYPE BIGINT USING -c")
	if err != nil {
		t.Fatal(err)
	}
	_, err = utility.ExecContext(t.Context())
	utility.Close()
	notNullDiagnostic(t, err, stuntgres.Error{Code: "23514", Message: `check constraint "ck" of relation "t" is violated by some row`, Schema: "public", Table: "t", Constraint: "ck"})
	metadata, err := eager.ColumnTypes()
	if err != nil || metadata[0].DatabaseTypeName() != "INT4" {
		t.Fatal(metadata, err)
	}
	if !eager.Next() {
		t.Fatal(eager.Err())
	}
	var old int64
	if err = eager.Scan(&old); err != nil || old != 1 {
		t.Fatal(old, err)
	}
	if eager.Next() || eager.Err() != nil {
		t.Fatal(eager.Err())
	}
	eager.Close()
	alterTypePublicRows(t, db, "SELECT c FROM t ORDER BY c", []string{"c"}, []string{"INT8"}, [][]any{{int64(1)}, {int64(2)}})
}

func TestAlterTypeCheckTransactions(t *testing.T) {
	for _, commit := range []bool{false, true} {
		t.Run(fmt.Sprint(commit), func(t *testing.T) {
			db := openLocal(t, stuntgres.NewDriver(), t.Name())
			execSQL(t, db, "CREATE TABLE t(id INT,c INT CONSTRAINT ck CHECK(c>0))")
			execSQL(t, db, "INSERT INTO t VALUES(1,1),(2,2),(3,3)")
			tx, err := db.Begin()
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			exec := func(q string) {
				t.Helper()
				if _, err := tx.Exec(q); err != nil {
					t.Fatal(q, err)
				}
			}
			exec("UPDATE t SET c=7 WHERE id=1")
			exec("DELETE FROM t WHERE id=2")
			exec("INSERT INTO t VALUES(4,4)")
			exec("SAVEPOINT before_type")
			action := "ALTER TABLE t ALTER c TYPE BIGINT USING CASE WHEN c=1 OR c=2 THEN 1/(c-c) ELSE c+1 END"
			exec(action)
			want := [][]any{{int64(1), int64(8)}, {int64(3), int64(4)}, {int64(4), int64(5)}}
			alterTypePublicRows(t, tx, "SELECT * FROM t ORDER BY id", []string{"id", "c"}, []string{"INT4", "INT8"}, want)
			var private bytes.Buffer
			if err = stuntgres.Dump(t.Context(), db, &private); err != nil || strings.Contains(private.String(), "BIGINT") {
				t.Fatal("private CHECK/TYPE leaked to Dump", err, private.String())
			}
			exec("SAVEPOINT after_type")
			_, err = tx.Exec("ALTER TABLE t ALTER c TYPE BIGINT USING -c")
			notNullDiagnostic(t, err, stuntgres.Error{Code: "23514", Message: `check constraint "ck" of relation "t" is violated by some row`, Schema: "public", Table: "t", Constraint: "ck"})
			_, err = tx.Exec("SELECT * FROM t")
			notNullDiagnostic(t, err, stuntgres.Error{Code: "25P02", Message: "current transaction is aborted, commands ignored until end of transaction block"})
			exec("ROLLBACK TO after_type")
			exec("ALTER TABLE t ALTER c TYPE BIGINT USING c+1")
			exec("ROLLBACK TO after_type")
			exec("ROLLBACK TO after_type")
			alterTypePublicRows(t, tx, "SELECT * FROM t ORDER BY id", []string{"id", "c"}, []string{"INT4", "INT8"}, want)
			exec("ROLLBACK TO before_type")
			alterTypePublicRows(t, tx, "SELECT * FROM t ORDER BY id", []string{"id", "c"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(7)}, {int64(3), int64(3)}, {int64(4), int64(4)}})
			exec(action)
			if commit {
				err = tx.Commit()
			} else {
				err = tx.Rollback()
			}
			if err != nil {
				t.Fatal(err)
			}
			if commit {
				alterTypePublicRows(t, db, "SELECT * FROM t ORDER BY id", []string{"id", "c"}, []string{"INT4", "INT8"}, want)
			} else {
				alterTypePublicRows(t, db, "SELECT * FROM t ORDER BY id", []string{"id", "c"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(1)}, {int64(2), int64(2)}, {int64(3), int64(3)}})
			}
			_, err = db.Exec("INSERT INTO t VALUES(5,-1)")
			notNullDiagnostic(t, err, stuntgres.Error{Code: "23514", Message: `new row for relation "t" violates check constraint "ck"`, Detail: "Failing row contains (5, -1).", Schema: "public", Table: "t", Constraint: "ck"})
		})
	}
}

func TestAlterTypeCheckControlsAndDefaultRemoval(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	execSQL(t, db, "CREATE TABLE t(p INT,c INT DEFAULT 7 CONSTRAINT ck CHECK(c>0) NO INHERIT,n INT DEFAULT 1 CONSTRAINT nn NOT NULL)")
	execSQL(t, db, "INSERT INTO t VALUES(9,7,1)")
	execSQL(t, db, "ALTER TABLE t ALTER c TYPE BIGINT USING c+1,DROP p")
	var dump bytes.Buffer
	if err := stuntgres.Dump(t.Context(), db, &dump); err != nil {
		t.Fatal(err)
	}
	for _, text := range []string{"BIGINT", "ck", "NO INHERIT", "nn", "DEFAULT"} {
		if !strings.Contains(dump.String(), text) {
			t.Fatal(text, dump.String())
		}
	}
	if strings.Contains(dump.String(), "ALTER TABLE") {
		t.Fatal(dump.String())
	}
	if err := stuntgres.Reset(t.Context(), db); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(t.Context(), db, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	var again bytes.Buffer
	if err := stuntgres.Dump(t.Context(), db, &again); err != nil || again.String() != dump.String() {
		t.Fatal(err, again.String(), dump.String())
	}
	execSQL(t, db, "INSERT INTO t DEFAULT VALUES")
	execSQL(t, db, "INSERT INTO t(c) VALUES(NULL)")
	alterTypePublicRows(t, db, "SELECT * FROM t ORDER BY c", []string{"c", "n"}, []string{"INT8", "INT4"}, [][]any{{int64(7), int64(1)}, {int64(8), int64(1)}, {nil, int64(1)}})
	_, err := db.Exec("INSERT INTO t(c) VALUES(-1)")
	notNullDiagnostic(t, err, stuntgres.Error{Code: "23514", Message: `new row for relation "t" violates check constraint "ck"`, Detail: "Failing row contains (-1, 1).", Schema: "public", Table: "t", Constraint: "ck"})
	execSQL(t, db, "CREATE TABLE d(c TEXT DEFAULT '7' CONSTRAINT ck CHECK(c>'0'))")
	execSQL(t, db, "INSERT INTO d VALUES('7')")
	execSQL(t, db, "ALTER TABLE d ALTER c DROP DEFAULT")
	_, err = db.Exec("ALTER TABLE d ALTER c TYPE INT USING c::int")
	notNullDiagnostic(t, err, stuntgres.Error{Code: "42883", Message: "operator does not exist: integer > text", Hint: "No operator matches the given name and argument types. You might need to add explicit type casts."})
	execSQL(t, db, "ALTER TABLE d DROP CONSTRAINT ck,ALTER c TYPE INT USING c::int")
	alterTypePublicRows(t, db, "SELECT c FROM d", []string{"c"}, []string{"INT4"}, [][]any{{int64(7)}})
}

func TestAlterTypeCheckFloatingControls(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprint(prepared), func(t *testing.T) {
			db := openLocal(t, stuntgres.NewDriver(), t.Name())
			execSQL(t, db, "CREATE TABLE t(c REAL CONSTRAINT ck CHECK(c>'1.0000001' AND c<>NULL))")
			execSQL(t, db, "INSERT INTO t VALUES(2),(NULL)")
			action := "ALTER TABLE t ALTER c TYPE DOUBLE PRECISION USING c"
			if prepared {
				s, err := db.Prepare(action)
				if err != nil {
					t.Fatal(err)
				}
				defer s.Close()
				if _, err := s.Exec(); err != nil {
					t.Fatal(err)
				}
			} else {
				execSQL(t, db, action)
			}
			reject := func() {
				_, err := db.Exec("INSERT INTO t VALUES(1.00000011)")
				notNullDiagnostic(t, err, stuntgres.Error{Code: "23514", Message: `new row for relation "t" violates check constraint "ck"`, Detail: "Failing row contains (1.00000011).", Schema: "public", Table: "t", Constraint: "ck"})
			}
			reject()
			alterTypePublicRows(t, db, "SELECT c FROM t ORDER BY c", []string{"c"}, []string{"FLOAT8"}, [][]any{{float64(2)}, {nil}})
			tableCoreExec(t, db, prepared, "INSERT INTO t VALUES(1.00000013)", 1)
			alterTypePublicRows(t, db, "SELECT c FROM t ORDER BY c", []string{"c"}, []string{"FLOAT8"}, [][]any{{float64(1.00000013)}, {float64(2)}, {nil}})
			var dump bytes.Buffer
			if err := stuntgres.Dump(t.Context(), db, &dump); err != nil {
				t.Fatal(err)
			}
			if strings.Contains(dump.String(), "$1") || strings.Contains(dump.String(), "ALTER TABLE") {
				t.Fatal("private TYPE history leaked", dump.String())
			}
			if err := stuntgres.Reset(t.Context(), db); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(t.Context(), db, bytes.NewReader(dump.Bytes())); err != nil {
				t.Fatal(err)
			}
			reject()
			alterTypePublicRows(t, db, "SELECT c FROM t ORDER BY c", []string{"c"}, []string{"FLOAT8"}, [][]any{{float64(1.00000013)}, {float64(2)}, {nil}})
			tableCoreExec(t, db, prepared, "INSERT INTO t VALUES(1.00000013)", 1)
			alterTypePublicRows(t, db, "SELECT c FROM t ORDER BY c", []string{"c"}, []string{"FLOAT8"}, [][]any{{float64(1.00000013)}, {float64(1.00000013)}, {float64(2)}, {nil}})
			execSQL(t, db, "INSERT INTO t VALUES(2.5),(NULL)")
			alterTypePublicRows(t, db, "SELECT c FROM t ORDER BY c", []string{"c"}, []string{"FLOAT8"}, [][]any{{float64(1.00000013)}, {float64(1.00000013)}, {float64(2)}, {float64(2.5)}, {nil}, {nil}})
		})
	}
}
