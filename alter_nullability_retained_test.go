package stuntgres_test

import (
	"database/sql"
	"fmt"
	"reflect"
	"stuntgres"
	"testing"
)

func alterNullabilityPrivateRows(t *testing.T, rows *sql.Rows, names, types []string, nullable []bool, want [][]any) {
	t.Helper()
	defer rows.Close()
	labels, err := rows.Columns()
	if err != nil || !reflect.DeepEqual(labels, names) {
		t.Fatal(labels, err)
	}
	metadata, err := rows.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	for i, column := range metadata {
		n, k := column.Nullable()
		scan := reflect.TypeFor[int64]()
		if types[i] == "TEXT" {
			scan = reflect.TypeFor[string]()
		}
		if column.DatabaseTypeName() != types[i] || column.ScanType() != scan || !k || n != nullable[i] {
			t.Fatal(column, n, k)
		}
	}
	got := make([][]any, 0)
	for rows.Next() {
		row := make([]any, len(names))
		dest := make([]any, len(names))
		for i := range row {
			dest[i] = &row[i]
		}
		if err = rows.Scan(dest...); err != nil {
			t.Fatal(err)
		}
		got = append(got, row)
	}
	if err = rows.Err(); err != nil || !reflect.DeepEqual(got, want) {
		t.Fatal(got, want, err)
	}
}

func TestAlterNullabilityReviewRetainedPositiveDML(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	ctx := t.Context()
	execSQL(t, db, "CREATE TABLE t(a INT,b INT,c INT,d INT)")
	execSQL(t, db, "INSERT INTO t VALUES(7,9,11,13)")
	originalUpdate, err := db.PrepareContext(ctx, "UPDATE t SET c=$1 WHERE c=$2")
	if err != nil {
		t.Fatal(err)
	}
	defer originalUpdate.Close()
	originalSelect, err := db.PrepareContext(ctx, "SELECT c FROM t")
	if err != nil {
		t.Fatal(err)
	}
	defer originalSelect.Close()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	update := tx.StmtContext(ctx, originalUpdate)
	defer update.Close()
	selected := tx.StmtContext(ctx, originalSelect)
	defer selected.Close()
	change := func(next, previous int64, nullable bool) {
		t.Helper()
		r, e := update.ExecContext(ctx, next, previous)
		if e != nil {
			t.Fatal(e)
		}
		count, e := r.RowsAffected()
		if e != nil || count != 1 {
			t.Fatal(count, e)
		}
		rows, e := selected.QueryContext(ctx)
		if e != nil {
			t.Fatal(e)
		}
		alterNullabilityPrivateRows(t, rows, []string{"c"}, []string{"INT4"}, []bool{nullable}, [][]any{{next}})
	}
	run := func(q string) { transactionAffected(t, ctx, tx, q, false, 0) }
	run("SAVEPOINT original_shape")
	run("ALTER TABLE t DROP b, ALTER c SET NOT NULL")
	change(14, 11, false)
	run("SAVEPOINT parent_shape")
	run("ALTER TABLE t DROP a")
	change(15, 14, false)
	run("ROLLBACK TO parent_shape")
	change(17, 14, false)
	run("ROLLBACK TO original_shape")
	change(18, 11, true)
	run("ALTER TABLE t DROP b, DROP a, ALTER c SET NOT NULL")
	change(19, 18, false)
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	wantTuples(t, db, "SELECT * FROM t", [][]any{{int64(19), int64(13)}})
}

func TestAlterNullabilityReviewSnapshotTimelines(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		for _, timeline := range []string{"middle", "multiple-before", "own-before"} {
			t.Run(fmt.Sprint(prepared)+timeline, func(t *testing.T) {
				db := openLocal(t, stuntgres.NewDriver(), t.Name())
				ctx := t.Context()
				execSQL(t, db, "CREATE TABLE t(a INT,b TEXT,c INT,d TEXT)")
				execSQL(t, db, "INSERT INTO t VALUES(7,'nine',11,'thirteen')")
				tx, err := db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
				if err != nil {
					t.Fatal(err)
				}
				defer tx.Rollback()
				run := func(q string, n int64) { transactionAffected(t, ctx, tx, q, prepared, n) }
				query := func(q string, names, types []string, nullable []bool, want [][]any) {
					t.Helper()
					rows, closeQuery, e := transactionQuery(ctx, tx, q, prepared)
					if e != nil {
						t.Fatal(e)
					}
					defer closeQuery()
					alterNullabilityPrivateRows(t, rows, names, types, nullable, want)
				}
				run("SAVEPOINT original_shape", 0)
				if timeline == "own-before" {
					run("INSERT INTO t VALUES(8,'own',12,'ownlast')", 1)
				}
				run("ALTER TABLE t DROP b, ALTER d SET NOT NULL", 0)
				if timeline != "middle" {
					run("SAVEPOINT parent_shape", 0)
					run("ALTER TABLE t DROP a", 0)
				}
				if timeline == "middle" {
					query("SELECT * FROM t", []string{"a", "c", "d"}, []string{"INT4", "INT4", "TEXT"}, []bool{true, true, false}, [][]any{{int64(7), int64(11), "thirteen"}})
				} else {
					want := [][]any{{int64(11), "thirteen"}}
					if timeline == "own-before" {
						want = append(want, []any{int64(12), "ownlast"})
					}
					query("SELECT * FROM t ORDER BY c", []string{"c", "d"}, []string{"INT4", "TEXT"}, []bool{true, false}, want)
					run("ROLLBACK TO parent_shape", 0)
					if timeline == "multiple-before" {
						run("ROLLBACK TO parent_shape", 0)
						query("SELECT * FROM t", []string{"a", "c", "d"}, []string{"INT4", "INT4", "TEXT"}, []bool{true, true, false}, [][]any{{int64(7), int64(11), "thirteen"}})
					}
				}
				run("ROLLBACK TO original_shape", 0)
				query("SELECT * FROM t", []string{"a", "b", "c", "d"}, []string{"INT4", "TEXT", "INT4", "TEXT"}, []bool{true, true, true, true}, [][]any{{int64(7), "nine", int64(11), "thirteen"}})
				run("ALTER TABLE t DROP b, ALTER d SET NOT NULL", 0)
				run("UPDATE t SET d='changed'", 1)
				if err = tx.Commit(); err != nil {
					t.Fatal(err)
				}
				wantTuples(t, db, "SELECT * FROM t", [][]any{{int64(7), int64(11), "changed"}})
			})
		}
	}
}
