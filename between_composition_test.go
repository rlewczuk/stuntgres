package stuntgres_test

import (
	"bytes"
	"context"
	"database/sql/driver"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"

	"stuntgres"
)

func TestBetweenConsumersAndAtomicity(t *testing.T) {
	_, db := betweenSetup(t)
	compareOpsDirectPrepared(t, db, "SELECT CASE WHEN v BETWEEN 1 AND 3 THEN 1 ELSE 0 END FROM t ORDER BY id", []string{"INT4"}, [][]any{{int64(0)}, {int64(1)}, {int64(1)}, {int64(1)}, {int64(0)}, {int64(0)}})
	compareOpsDirectPrepared(t, db, "SELECT CASE WHEN v BETWEEN 1 AND 3 = TRUE THEN 1 ELSE 0 END FROM t ORDER BY id", []string{"INT4"}, [][]any{{int64(0)}, {int64(1)}, {int64(1)}, {int64(1)}, {int64(0)}, {int64(0)}})
	wantTuples(t, db, "SELECT COALESCE(v BETWEEN 1 AND 3,FALSE) FROM t ORDER BY id", [][]any{{false}, {true}, {true}, {true}, {false}, {false}})
	execSQL(t, db, "CREATE TABLE result(id INTEGER,b BOOLEAN)")
	result, err := db.Exec("INSERT INTO result SELECT id,v BETWEEN 1 AND 3 FROM t")
	if err != nil {
		t.Fatal(err)
	}
	if n, err := result.RowsAffected(); err != nil || n != 6 {
		t.Fatal(n, err)
	}
	wantTuples(t, db, "SELECT id,b FROM result ORDER BY id", [][]any{{int64(1), false}, {int64(2), true}, {int64(3), true}, {int64(4), true}, {int64(5), false}, {int64(6), nil}})
	stmt, err := db.Prepare("UPDATE result SET b=id BETWEEN 2 AND 4 WHERE id BETWEEN 2 AND 4")
	if err != nil {
		t.Fatal(err)
	}
	result, err = stmt.Exec()
	stmt.Close()
	if err != nil {
		t.Fatal(err)
	}
	if n, err := result.RowsAffected(); err != nil || n != 3 {
		t.Fatal(n, err)
	}
	for _, query := range []string{"UPDATE result SET b=id BETWEEN 1 AND TRUE", "UPDATE result SET b=id BETWEEN 7 AND 'oops'"} {
		if _, err := db.Exec(query); err == nil {
			t.Fatal("invalid mutation accepted")
		}
		if held, err := db.Prepare(query); err == nil {
			held.Close()
			t.Fatal("invalid mutation prepared")
		}
	}
	wantTuples(t, db, "SELECT id,b FROM result ORDER BY id", [][]any{{int64(1), false}, {int64(2), true}, {int64(3), true}, {int64(4), true}, {int64(5), false}, {int64(6), nil}})
	want := [][]any{{false, int64(2)}, {true, int64(3)}, {nil, int64(1)}}
	rows, err := db.Query("SELECT v BETWEEN 1 AND 3,count(*) FROM t GROUP BY v BETWEEN 1 AND 3")
	if err != nil {
		t.Fatal(err)
	}
	got := [][]any{}
	for rows.Next() {
		var b, n any
		if err := rows.Scan(&b, &n); err != nil {
			t.Fatal(err)
		}
		got = append(got, []any{b, n})
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	rows.Close()
	for _, row := range want {
		found := false
		for _, actual := range got {
			if reflect.DeepEqual(row, actual) {
				found = true
			}
		}
		if !found {
			t.Fatal(got, want)
		}
	}
	if len(got) != 3 {
		t.Fatal(got)
	}
	compareOpsDirectPrepared(t, db, "SELECT count(*) FROM t GROUP BY v BETWEEN 1 AND 3 HAVING v BETWEEN 1 AND 3", []string{"INT8"}, [][]any{{int64(3)}})
	result, err = db.Exec("DELETE FROM t WHERE v BETWEEN SYMMETRIC 3 AND 1 = TRUE")
	if err != nil {
		t.Fatal(err)
	}
	if n, err := result.RowsAffected(); err != nil || n != 3 {
		t.Fatal(n, err)
	}
	wantTuples(t, db, "SELECT id FROM t ORDER BY id", [][]any{{int64(1)}, {int64(5)}, {int64(6)}})
	stmt, err = db.Prepare("DELETE FROM t WHERE v NOT BETWEEN 1 AND 3")
	if err != nil {
		t.Fatal(err)
	}
	result, err = stmt.Exec()
	stmt.Close()
	if err != nil {
		t.Fatal(err)
	}
	if n, err := result.RowsAffected(); err != nil || n != 2 {
		t.Fatal(n, err)
	}
	wantTuples(t, db, "SELECT id FROM t", [][]any{{int64(6)}})
}

func TestBetweenControlsAndCancellation(t *testing.T) {
	d, db := betweenSetup(t)
	ctx := context.Background()
	raw, err := d.Open("between")
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	cursor, err := raw.(driver.QueryerContext).QueryContext(ctx, "SELECT v BETWEEN 1 AND 3 FROM t ORDER BY id", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cursor.Close()
	execSQL(t, db, "CREATE TABLE defaults(b BOOL DEFAULT (2 BETWEEN 1 AND 3),s BOOL DEFAULT (2 BETWEEN SYMMETRIC 3 AND 1),n BOOL DEFAULT (2 NOT BETWEEN 1 AND 3))")
	execSQL(t, db, "INSERT INTO defaults DEFAULT VALUES")
	var dump bytes.Buffer
	if err := stuntgres.Dump(ctx, db, &dump); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Reset(ctx, db); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(ctx, db, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	execSQL(t, db, "INSERT INTO defaults DEFAULT VALUES")
	wantTuples(t, db, "SELECT * FROM defaults", [][]any{{true, true, false}, {true, true, false}})
	if err := stuntgres.Restore(ctx, db, strings.NewReader("CREATE TABLE bad(i INT); INSERT INTO bad VALUES('oops')")); err == nil {
		t.Fatal("invalid restore accepted")
	}
	wantTuples(t, db, "SELECT * FROM defaults", [][]any{{true, true, false}, {true, true, false}})
	execSQL(t, db, "DELETE FROM t WHERE v BETWEEN 1 AND 3")
	wantTuples(t, db, "SELECT id FROM t ORDER BY id", [][]any{{int64(1)}, {int64(5)}, {int64(6)}})
	dst := make([]driver.Value, 1)
	for _, want := range []driver.Value{false, true, true, true, false, nil} {
		if err := cursor.Next(dst); err != nil || !reflect.DeepEqual(dst[0], want) {
			t.Fatal(dst, want, err)
		}
	}
	if err := cursor.Next(dst); err != io.EOF {
		t.Fatal(err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if rows, err := db.QueryContext(canceled, "SELECT v BETWEEN 1 AND 3 FROM t"); !errors.Is(err, context.Canceled) {
		if rows != nil {
			rows.Close()
		}
		t.Fatal(err)
	}
	stmt, err := db.Prepare("DELETE FROM t WHERE v NOT BETWEEN 1 AND 3")
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	if _, err := stmt.ExecContext(canceled); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(canceled, "DELETE FROM t WHERE v NOT BETWEEN 1 AND 3"); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	wantTuples(t, db, "SELECT id FROM t ORDER BY id", [][]any{{int64(1)}, {int64(5)}, {int64(6)}})
}
