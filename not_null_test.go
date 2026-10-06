package stuntgres_test

import (
	"bytes"
	"context"
	"errors"
	"stuntgres"
	"testing"
)

func notNullDiagnostic(t *testing.T, err error, want stuntgres.Error) {
	t.Helper()
	want.Severity = "ERROR"
	want.SeverityUnlocalized = "ERROR"
	var got *stuntgres.Error
	if !errors.As(err, &got) || *got != want {
		t.Fatalf("got %#v (%v), want %#v", got, err, want)
	}
}

func TestNotNullFormsAlterAndIdentity(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "prepared"}[prepared], func(t *testing.T) {
			db := openLocal(t, stuntgres.NewDriver(), t.Name())
			tableCoreExec(t, db, prepared, "CREATE TABLE t(CONSTRAINT nn NOT NULL a,a INT,b TEXT CONSTRAINT ignored NULL)", 0)
			tableCoreExec(t, db, true, "INSERT INTO t VALUES($1,$2)", 1, 7, nil)
			alterDropMetadata(t, db, true, "SELECT * FROM t", []string{"a", "b"}, []string{"INT4", "TEXT"}, []bool{false, true}, 1)
			wantTuples(t, db, "SELECT * FROM t", [][]any{{int64(7), nil}})
			for _, q := range []string{"INSERT INTO t VALUES(NULL,'bad')", "INSERT INTO t(b) VALUES('bad')"} {
				_, err := db.Exec(q)
				notNullDiagnostic(t, err, stuntgres.Error{Code: "23502", Message: `null value in column "a" of relation "t" violates not-null constraint`, Detail: "Failing row contains (null, bad).", Schema: "public", Table: "t", Column: "a"})
			}
			tableCoreExec(t, db, prepared, "ALTER TABLE t ADD CONSTRAINT other NOT NULL a", 0)
			_, err := db.Exec("ALTER TABLE t DROP CONSTRAINT other")
			notNullDiagnostic(t, err, stuntgres.Error{Code: "42704", Message: `constraint "other" of relation "t" does not exist`})
			tableCoreExec(t, db, prepared, "ALTER TABLE ONLY(t) DROP CONSTRAINT nn", 0)
			tableCoreExec(t, db, prepared, "INSERT INTO t VALUES(NULL,'keep')", 1)
			var before bytes.Buffer
			if err = stuntgres.Dump(context.Background(), db, &before); err != nil {
				t.Fatal(err)
			}
			for _, q := range []string{"ALTER TABLE t ALTER a SET NOT NULL", "ALTER TABLE t ADD CONSTRAINT nn NOT NULL a"} {
				_, err = db.Exec(q)
				notNullDiagnostic(t, err, stuntgres.Error{Code: "23502", Message: `column "a" of relation "t" contains null values`, Schema: "public", Table: "t", Column: "a"})
			}
			var after bytes.Buffer
			if err = stuntgres.Dump(context.Background(), db, &after); err != nil || before.String() != after.String() {
				t.Fatal(err, after.String())
			}
			tableCoreExec(t, db, prepared, "CREATE TABLE valid(a INT,b TEXT)", 0)
			tableCoreExec(t, db, prepared, "INSERT INTO valid VALUES(1,'keep')", 1)
			query, err := db.Prepare("SELECT * FROM valid")
			if err != nil {
				t.Fatal(err)
			}
			defer query.Close()
			tableCoreExec(t, db, prepared, "ALTER TABLE valid ALTER COLUMN a SET NOT NULL", 0)
			rows, err := query.Query()
			if err != nil {
				t.Fatal(err)
			}
			types, err := rows.ColumnTypes()
			if err != nil {
				t.Fatal(err)
			}
			nullable, known := types[0].Nullable()
			rows.Close()
			if nullable || !known {
				t.Fatal(nullable, known)
			}
			tableCoreExec(t, db, prepared, "ALTER TABLE valid DROP CONSTRAINT valid_a_not_null", 0)
			tableCoreExec(t, db, prepared, "ALTER TABLE valid ALTER a DROP NOT NULL", 0)
			tableCoreExec(t, db, prepared, "INSERT INTO valid VALUES(NULL,NULL)", 1)
		})
	}
}

func TestNotNullNamesLikeAndCleanup(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	execSQL(t, db, "CREATE TABLE occupied(x INT CONSTRAINT t_a_not_null NOT NULL)")
	execSQL(t, db, "CREATE TABLE t(a INT NOT NULL,b INT CONSTRAINT survivor NOT NULL)")
	execSQL(t, db, "CREATE TABLE clone(LIKE t)")
	execSQL(t, db, "ALTER TABLE t DROP a")
	execSQL(t, db, "ALTER TABLE t DROP CONSTRAINT survivor")
	execSQL(t, db, "ALTER TABLE clone DROP CONSTRAINT t_a_not_null1")
	execSQL(t, db, "INSERT INTO clone VALUES(NULL,2)")
	execSQL(t, db, "ALTER TABLE clone DROP CONSTRAINT survivor")
	execSQL(t, db, "CREATE SCHEMA nested CREATE TABLE first(x INT CONSTRAINT second_a_not_null NOT NULL) CREATE TABLE second(a INT NOT NULL)")
	execSQL(t, db, "ALTER TABLE nested.second DROP CONSTRAINT second_a_not_null1")
}

func TestNotNullDeclarationDiagnostics(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	for _, tc := range []struct{ q, code, message, position string }{
		{"CREATE TABLE t (a INT NULL NOT NULL)", "42601", `conflicting NULL/NOT NULL declarations for column "a" of table "t"`, "28"},
		{"CREATE TABLE t (a INT NOT NULL NULL)", "42601", `conflicting NULL/NOT NULL declarations for column "a" of table "t"`, "32"},
		{"CREATE TABLE t (a INT CONSTRAINT x NOT NULL CONSTRAINT y NOT NULL)", "XX000", `conflicting not-null constraint names "x" and "y"`, ""},
		{"CREATE TABLE t (a INT CONSTRAINT x NOT NULL, CONSTRAINT y NOT NULL a)", "42601", `conflicting not-null constraint names "x" and "y"`, ""},
		{"CREATE TABLE t(CONSTRAINT x NOT NULL a,a INT NOT NULL CONSTRAINT y NOT NULL)", "42601", `conflicting not-null constraint names "x" and "y"`, ""},
		{"CREATE TABLE t(a INT,b INT,CONSTRAINT nn NOT NULL a,CONSTRAINT nn NOT NULL b)", "42710", `constraint "nn" for relation "t" already exists`, ""},
		{"CREATE TABLE t(a INT,NOT NULL missing)", "42703", `column "missing" of relation "t" does not exist`, ""},
	} {
		_, err := db.Exec(tc.q)
		notNullDiagnostic(t, err, stuntgres.Error{Code: tc.code, Message: tc.message, Position: tc.position})
	}
	execSQL(t, db, "CREATE TABLE t(a INT)")
	execSQL(t, db, "INSERT INTO t VALUES(NULL)")
}

func TestNotNullColumnLocalDeclarations(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		for _, tc := range []struct{ q, name string }{
			{"CREATE TABLE t(a INT NULL,NOT NULL a)", "t_a_not_null"},
			{"CREATE TABLE t(NOT NULL a,a INT NULL)", "t_a_not_null"},
			{"CREATE TABLE t(a INT CONSTRAINT ignored NULL,CONSTRAINT nn NOT NULL a)", "nn"},
			{"CREATE TABLE t(CONSTRAINT nn NOT NULL a,a INT CONSTRAINT ignored NULL)", "nn"},
		} {
			t.Run(tc.q+map[bool]string{false: "/direct", true: "/prepared"}[prepared], func(t *testing.T) {
				db := openLocal(t, stuntgres.NewDriver(), t.Name())
				tableCoreExec(t, db, prepared, tc.q, 0)
				tableCoreMetadata(t, db, prepared, "SELECT * FROM t", []string{"a"}, []string{"INT4"}, []bool{false})
				_, err := db.Exec("INSERT INTO t VALUES(NULL)")
				notNullDiagnostic(t, err, stuntgres.Error{Code: "23502", Message: `null value in column "a" of relation "t" violates not-null constraint`, Detail: "Failing row contains (null).", Schema: "public", Table: "t", Column: "a"})
				wantTuples(t, db, "SELECT * FROM t", [][]any{})
				tableCoreExec(t, db, prepared, "INSERT INTO t VALUES($1)", 1, 7)
				alterDropMetadata(t, db, prepared, "SELECT * FROM t", []string{"a"}, []string{"INT4"}, []bool{false}, 1)
				tableCoreExec(t, db, prepared, "ALTER TABLE t DROP CONSTRAINT "+tc.name, 0)
				tableCoreExec(t, db, prepared, "INSERT INTO t VALUES($1)", 1, nil)
				alterDropMetadata(t, db, prepared, "SELECT * FROM t", []string{"a"}, []string{"INT4"}, []bool{true}, 2)
				wantTuples(t, db, "SELECT * FROM t ORDER BY a", [][]any{{int64(7)}, {nil}})
			})
		}
		t.Run(map[bool]string{false: "conflict/direct", true: "conflict/prepared"}[prepared], func(t *testing.T) {
			db := openLocal(t, stuntgres.NewDriver(), t.Name())
			q := "CREATE TABLE t(CONSTRAINT x NOT NULL a,a INT CONSTRAINT x NOT NULL CONSTRAINT y NOT NULL)"
			var err error
			if prepared {
				stmt, prepareErr := db.Prepare(q)
				if prepareErr != nil {
					t.Fatal(prepareErr)
				}
				_, err = stmt.Exec()
				stmt.Close()
			} else {
				_, err = db.Exec(q)
			}
			notNullDiagnostic(t, err, stuntgres.Error{Code: "XX000", Message: `conflicting not-null constraint names "x" and "y"`})
			_, err = db.Query("SELECT * FROM t")
			notNullDiagnostic(t, err, stuntgres.Error{Code: "42P01", Message: `relation "t" does not exist`})
		})
	}
}

func TestNotNullCreateSystemTarget(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		for _, q := range []string{"CREATE TABLE t(a INT,NOT NULL ctid)", "CREATE TABLE t(a INT,CONSTRAINT nn NOT NULL ctid)"} {
			t.Run(q+map[bool]string{false: "/direct", true: "/prepared"}[prepared], func(t *testing.T) {
				db := openLocal(t, stuntgres.NewDriver(), t.Name())
				execSQL(t, db, "CREATE TABLE keeper(v INT)")
				execSQL(t, db, "INSERT INTO keeper VALUES(91)")
				var err error
				if prepared {
					stmt, prepareErr := db.Prepare(q)
					if prepareErr != nil {
						t.Fatal("utility Prepare", prepareErr)
					}
					_, err = stmt.Exec()
					stmt.Close()
				} else {
					_, err = db.Exec(q)
				}
				notNullDiagnostic(t, err, stuntgres.Error{Code: "0A000", Message: `cannot add not-null constraint on system column "ctid"`})
				_, err = db.Query("SELECT * FROM t")
				notNullDiagnostic(t, err, stuntgres.Error{Code: "42P01", Message: `relation "t" does not exist`})
				wantTuples(t, db, "SELECT * FROM keeper", [][]any{{int64(91)}})
			})
		}
	}
}
