package stuntgres_test

import (
	"context"
	"database/sql"
	"reflect"
	"stuntgres"
	"testing"
)

func TestAlterNullabilityActionLists(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		for _, tc := range []struct {
			name, seed, actions string
			want                stuntgres.Error
			nn                  bool
			columns             []string
			rows                [][]any
		}{
			{name: "semantic-before-scan", seed: "NULL,NULL", actions: "ALTER a SET NOT NULL, ALTER missing SET NOT NULL", want: stuntgres.Error{Code: "42703", Message: `column "missing" of relation "t" does not exist`}},
			{name: "drop-before-set", seed: "7,9", actions: "ALTER a SET NOT NULL, ALTER a DROP NOT NULL", nn: true},
			{name: "reverse-drop-before-set", seed: "7,9", actions: "ALTER a DROP NOT NULL, ALTER a SET NOT NULL", nn: true},
			{name: "drop-column-before-set", seed: "7,9", actions: "ALTER a SET NOT NULL, DROP COLUMN a", want: stuntgres.Error{Code: "42703", Message: `column "a" of relation "t" does not exist`}},
			{name: "physical-scan-order", seed: "NULL,NULL", actions: "ADD CONSTRAINT b_nn NOT NULL b, ADD CONSTRAINT a_nn NOT NULL a", want: stuntgres.Error{Code: "23502", Message: `column "a" of relation "t" contains null values`, Schema: "public", Table: "t", Column: "a"}},
			{name: "drop-before-add", seed: "7,9", actions: "ADD CONSTRAINT n NOT NULL a, DROP CONSTRAINT n", want: stuntgres.Error{Code: "42704", Message: `constraint "n" of relation "t" does not exist`}},
			{name: "projection", seed: "7,9", actions: "DROP COLUMN b, ALTER a SET NOT NULL", nn: true, columns: []string{"a"}, rows: [][]any{{int64(7)}}},
			{name: "projection-rollback", seed: "NULL,9", actions: "DROP COLUMN b, ALTER a SET NOT NULL", want: stuntgres.Error{Code: "23502", Message: `column "a" of relation "t" contains null values`, Schema: "public", Table: "t", Column: "a"}},
		} {
			t.Run(tc.name+map[bool]string{false: "/direct", true: "/prepared"}[prepared], func(t *testing.T) {
				db := openLocal(t, stuntgres.NewDriver(), t.Name())
				execSQL(t, db, "CREATE TABLE t(a INT,b INT)")
				execSQL(t, db, "INSERT INTO t VALUES("+tc.seed+")")
				q := "ALTER TABLE t " + tc.actions
				if tc.want.Code != "" {
					var err error
					if prepared {
						s, e := db.Prepare(q)
						if e != nil {
							t.Fatal("effect-free prepare", e)
						}
						_, err = s.Exec()
						s.Close()
					} else {
						_, err = db.Exec(q)
					}
					notNullDiagnostic(t, err, tc.want)
				} else {
					tableCoreExec(t, db, prepared, q, 0)
				}
				columns := tc.columns
				if columns == nil {
					columns = []string{"a", "b"}
				}
				types := []string{"INT4"}
				nullable := []bool{!tc.nn}
				if len(columns) == 2 {
					types = append(types, "INT4")
					nullable = append(nullable, true)
				}
				alterDropMetadata(t, db, false, "SELECT * FROM t", columns, types, nullable, 1)
				rows := tc.rows
				if rows == nil {
					if tc.seed == "7,9" {
						rows = [][]any{{int64(7), int64(9)}}
					} else if tc.seed == "NULL,9" {
						rows = [][]any{{nil, int64(9)}}
					} else {
						rows = [][]any{{nil, nil}}
					}
				}
				wantTuples(t, db, "SELECT * FROM t", rows)
			})
		}
	}
}

func TestAlterNullabilityFailingActionNotice(t *testing.T) {
	c, err := stuntgres.NewDriver().OpenConnector(t.Name())
	if err != nil {
		t.Fatal(err)
	}
	var notices []stuntgres.Error
	db := sql.OpenDB(c.(*stuntgres.Connector).WithNoticeHandler(func(n stuntgres.Error) { notices = append(notices, n) }))
	defer db.Close()
	db.SetMaxOpenConns(1)
	execSQL(t, db, "CREATE TABLE t(a INT)")
	execSQL(t, db, "INSERT INTO t VALUES(NULL)")
	_, err = db.Exec("ALTER TABLE t DROP CONSTRAINT IF EXISTS missing, ALTER a SET NOT NULL")
	notNullDiagnostic(t, err, stuntgres.Error{Code: "23502", Message: `column "a" of relation "t" contains null values`, Schema: "public", Table: "t", Column: "a"})
	want := []stuntgres.Error{{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: `constraint "missing" of relation "t" does not exist, skipping`}}
	if !reflect.DeepEqual(notices, want) {
		t.Fatalf("notices %#v want %#v", notices, want)
	}
	wantTuples(t, db, "SELECT * FROM t", [][]any{{nil}})
}

func TestAlterNullabilityTransactions(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	execSQL(t, db, "CREATE TABLE t(a INT,b INT)")
	execSQL(t, db, "INSERT INTO t VALUES(NULL,9)")
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	run := func(q string) {
		t.Helper()
		if _, e := tx.Exec(q); e != nil {
			t.Fatal(q, e)
		}
	}
	run("UPDATE t SET a=7")
	run("SAVEPOINT s")
	run("ALTER TABLE t DROP COLUMN b, ALTER a SET NOT NULL")
	run("INSERT INTO t VALUES(8)")
	rows, err := tx.Query("SELECT * FROM t ORDER BY a")
	if err != nil {
		t.Fatal(err)
	}
	cols, _ := rows.Columns()
	if !reflect.DeepEqual(cols, []string{"a"}) {
		t.Fatal(cols)
	}
	var values []int64
	for rows.Next() {
		var a int64
		if err := rows.Scan(&a); err != nil {
			t.Fatal(err)
		}
		values = append(values, a)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	rows.Close()
	if !reflect.DeepEqual(values, []int64{7, 8}) {
		t.Fatal(values)
	}
	run("ROLLBACK TO s")
	run("ROLLBACK TO s")
	run("INSERT INTO t VALUES(NULL,10)")
	run("DELETE FROM t WHERE b=10")
	run("ALTER TABLE t ALTER a SET NOT NULL")
	run("INSERT INTO t VALUES(8,11)")
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	wantTuples(t, db, "SELECT * FROM t ORDER BY a", [][]any{{int64(7), int64(9)}, {int64(8), int64(11)}})
	_, err = db.Exec("INSERT INTO t VALUES(NULL,12)")
	notNullDiagnostic(t, err, stuntgres.Error{Code: "23502", Message: `null value in column "a" of relation "t" violates not-null constraint`, Detail: "Failing row contains (null, 12).", Schema: "public", Table: "t", Column: "a"})
}

func TestAlterNullabilityFreshValidation(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	db.SetMaxOpenConns(2)
	execSQL(t, db, "CREATE TABLE t(a INT)")
	execSQL(t, db, "CREATE TABLE u(a INT)")
	execSQL(t, db, "INSERT INTO t VALUES(7)")
	execSQL(t, db, "INSERT INTO u VALUES(1)")
	tx, err := db.BeginTx(context.Background(), &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	var a int64
	if err := tx.QueryRow("SELECT a FROM u").Scan(&a); err != nil || a != 1 {
		t.Fatal(a, err)
	}
	execSQL(t, db, "INSERT INTO t VALUES(NULL)")
	_, err = tx.Exec("ALTER TABLE t ALTER a SET NOT NULL")
	notNullDiagnostic(t, err, stuntgres.Error{Code: "23502", Message: `column "a" of relation "t" contains null values`, Schema: "public", Table: "t", Column: "a"})
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	wantTuples(t, db, "SELECT * FROM t ORDER BY a", [][]any{{int64(7)}, {nil}})
}
