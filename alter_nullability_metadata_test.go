package stuntgres_test

import (
	"database/sql"
	"reflect"
	"stuntgres"
	"testing"
)

func TestAlterNullabilityValidation(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "prepared"}[prepared], func(t *testing.T) {
			db := openLocal(t, stuntgres.NewDriver(), t.Name())
			execSQL(t, db, "CREATE TABLE t(a INT,b TEXT)")
			execSQL(t, db, "INSERT INTO t VALUES(NULL,'old')")
			for _, q := range []string{"ALTER TABLE t ALTER a SET NOT NULL", "ALTER TABLE t ADD CONSTRAINT nn NOT NULL a"} {
				var err error
				if prepared {
					s, e := db.Prepare(q)
					if e != nil {
						t.Fatal(e)
					}
					_, err = s.Exec()
					s.Close()
				} else {
					_, err = db.Exec(q)
				}
				notNullDiagnostic(t, err, stuntgres.Error{Code: "23502", Message: `column "a" of relation "t" contains null values`, Schema: "public", Table: "t", Column: "a"})
			}
			tableCoreExec(t, db, prepared, "UPDATE t SET a=7", 1)
			selectStmt, err := db.Prepare("SELECT * FROM t ORDER BY b")
			if err != nil {
				t.Fatal(err)
			}
			defer selectStmt.Close()
			insert, err := db.Prepare("INSERT INTO t VALUES($1,$2)")
			if err != nil {
				t.Fatal(err)
			}
			defer insert.Close()
			update, err := db.Prepare("UPDATE t SET a=$1 WHERE b=$2")
			if err != nil {
				t.Fatal(err)
			}
			defer update.Close()
			notNullRetainedRows(t, selectStmt, true, [][]any{{int64(7), "old"}})
			tableCoreExec(t, db, prepared, "ALTER TABLE t ADD CONSTRAINT nn NOT NULL a, ALTER b SET NOT NULL", 0)
			rows, err := selectStmt.Query()
			if err != nil {
				t.Fatal(err)
			}
			types, err := rows.ColumnTypes()
			rows.Close()
			if err != nil {
				t.Fatal(err)
			}
			for _, typ := range types {
				n, k := typ.Nullable()
				if !k || n {
					t.Fatal(typ, n, k)
				}
			}
			_, err = insert.Exec(nil, "bad")
			notNullDiagnostic(t, err, stuntgres.Error{Code: "23502", Message: `null value in column "a" of relation "t" violates not-null constraint`, Detail: "Failing row contains (null, bad).", Schema: "public", Table: "t", Column: "a"})
			_, err = update.Exec(nil, "old")
			notNullDiagnostic(t, err, stuntgres.Error{Code: "23502", Message: `null value in column "a" of relation "t" violates not-null constraint`, Detail: "Failing row contains (null, old).", Schema: "public", Table: "t", Column: "a"})
			tableCoreExec(t, db, prepared, "ALTER TABLE t DROP CONSTRAINT nn, ALTER b DROP NOT NULL", 0)
			result, err := insert.Exec(nil, "after")
			if err != nil {
				t.Fatal(err)
			}
			n, err := result.RowsAffected()
			if err != nil || n != 1 {
				t.Fatal(n, err)
			}
			notNullRetainedRows(t, selectStmt, true, [][]any{{nil, "after"}, {int64(7), "old"}})
			tableCoreExec(t, db, prepared, "DELETE FROM t WHERE b='after'", 1)
			tableCoreExec(t, db, prepared, "ALTER TABLE t ALTER a SET NOT NULL", 0)
			_, err = db.Exec("INSERT INTO t SELECT NULL,'query'")
			notNullDiagnostic(t, err, stuntgres.Error{Code: "23502", Message: `null value in column "a" of relation "t" violates not-null constraint`, Detail: "Failing row contains (null, query).", Schema: "public", Table: "t", Column: "a"})
		})
	}
}

func TestAlterNullabilityPreparedMetadata(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	execSQL(t, db, "CREATE TABLE t(a INT,b TEXT)")
	execSQL(t, db, "INSERT INTO t VALUES(7,'seed')")
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	s, err := tx.Prepare("SELECT * FROM t")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	utility, err := tx.Prepare("ALTER TABLE t ALTER a SET NOT NULL")
	if err != nil {
		t.Fatal(err)
	}
	defer utility.Close()
	if _, err = utility.Exec(); err != nil {
		t.Fatal(err)
	}
	rows, err := s.Query()
	if err != nil {
		t.Fatal(err)
	}
	types, err := rows.ColumnTypes()
	rows.Close()
	if err != nil {
		t.Fatal(err)
	}
	nullable, known := types[0].Nullable()
	if !known || nullable || types[0].ScanType() != reflect.TypeFor[int64]() {
		t.Fatal(types[0])
	}
	if _, err = tx.Exec("SAVEPOINT child"); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec("ALTER TABLE t ALTER a DROP NOT NULL"); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec("INSERT INTO t VALUES(NULL,'child')"); err != nil {
		t.Fatal(err)
	}
	if _, err = utility.Exec(); err == nil {
		t.Fatal("own NULL accepted")
	} else {
		notNullDiagnostic(t, err, stuntgres.Error{Code: "23502", Message: `column "a" of relation "t" contains null values`, Schema: "public", Table: "t", Column: "a"})
	}
	if _, err = tx.Exec("ROLLBACK TO child"); err != nil {
		t.Fatal(err)
	}
	rows, err = s.Query()
	if err != nil {
		t.Fatal(err)
	}
	types, err = rows.ColumnTypes()
	rows.Close()
	if err != nil {
		t.Fatal(err)
	}
	nullable, known = types[0].Nullable()
	if !known || nullable {
		t.Fatal(types[0])
	}
	if err = tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	alterDropMetadata(t, db, false, "SELECT * FROM t", []string{"a", "b"}, []string{"INT4", "TEXT"}, []bool{true, true}, 1)
}

func TestAlterNullabilityAuthorizationAndPhase(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	execSQL(t, db, "CREATE TABLE t(a INT,b INT)")
	execSQL(t, db, "CREATE USER reader")
	for _, q := range []string{"ALTER TABLE absent ALTER missing SET NOT NULL, DROP COLUMN b", "ALTER TABLE t DROP CONSTRAINT IF EXISTS missing, ALTER absent SET NOT NULL"} {
		s, err := db.Prepare(q)
		if err != nil {
			t.Fatal("metadata-only prepare", err)
		}
		s.Close()
	}
	execSQL(t, db, "SET SESSION AUTHORIZATION reader")
	_, err := db.Exec("ALTER TABLE t ALTER a DROP NOT NULL, DROP IF EXISTS missing")
	notNullDiagnostic(t, err, stuntgres.Error{Code: "42501", Message: "must be owner of table t"})
	execSQL(t, db, "RESET SESSION AUTHORIZATION")
	tx, err := db.BeginTx(t.Context(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	s, err := tx.Prepare("ALTER TABLE absent ALTER missing SET NOT NULL, DROP b")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	_, err = s.Exec()
	notNullDiagnostic(t, err, stuntgres.Error{Code: "25006", Message: "cannot execute ALTER TABLE in a read-only transaction"})
	_, err = tx.Exec("ALTER TABLE t ALTER a DROP NOT NULL")
	notNullDiagnostic(t, err, stuntgres.Error{Code: "25P02", Message: "current transaction is aborted, commands ignored until end of transaction block"})
}
