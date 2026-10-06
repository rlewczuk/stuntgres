package stuntgres_test

import (
	"context"
	"database/sql/driver"
	"errors"
	"strconv"
	"strings"
	"stuntgres"
	"sync"
	"testing"
)

func TestArithmeticExecutionFramesAndTransactions(t *testing.T) {
	d, db := arithmeticDB(t)
	ctx := context.Background()
	arithmeticExec(t, db, "CREATE TABLE t(id INT,i INT,b BIGINT)", 0)
	arithmeticExec(t, db, "INSERT INTO t VALUES(1,7,7),(2,0,0)", 2)
	held, err := db.Prepare("SELECT i+$1 FROM t WHERE id=1")
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	for _, arg := range []any{2, nil, 3} {
		var got any
		if err := held.QueryRow(arg).Scan(&got); err != nil {
			t.Fatal(err)
		}
		var want any
		if arg != nil {
			want = int64(arg.(int) + 7)
		}
		if got != want {
			t.Fatal(got, want)
		}
	}
	var wg sync.WaitGroup
	for n := range 8 {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			var got int64
			if err := held.QueryRow(n).Scan(&got); err != nil || got != int64(n+7) {
				t.Error(got, err)
			}
		}(n)
	}
	wg.Wait()
	arithmeticRows(t, db, "SELECT 7/i FROM t WHERE id=1", []string{"INT4"}, [][]any{{int64(1)}})
	rows, err := db.Query("SELECT 7/i FROM t ORDER BY id")
	if rows != nil {
		rows.Close()
		t.Fatal("later error exposed native cursor")
	}
	arithmeticError(t, err, stuntgres.Error{Code: "22012", Message: "division by zero"})
	writer, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	reader, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	if _, err := writer.ExecContext(ctx, "BEGIN"); err != nil {
		t.Fatal(err)
	}
	if _, err := writer.ExecContext(ctx, "INSERT INTO t VALUES(3,3+1,4),(4,7/0,4)"); err == nil {
		t.Fatal("bad transaction insert accepted")
	}
	var count int64
	if err := reader.QueryRowContext(ctx, "SELECT id FROM t WHERE id=1").Scan(&count); err != nil || count != 1 {
		t.Fatal(count, err)
	}
	if rows, err := reader.QueryContext(ctx, "SELECT id FROM t WHERE id=3"); err != nil {
		t.Fatal(err)
	} else {
		if rows.Next() {
			t.Fatal("partial tuple published")
		}
		rows.Close()
	}
	if _, err := writer.ExecContext(ctx, "INSERT INTO t VALUES(5,5,5)"); err == nil {
		t.Fatal("aborted transaction accepted insert")
	}
	if _, err := writer.ExecContext(ctx, "ROLLBACK"); err != nil {
		t.Fatal(err)
	}
	if _, err := writer.ExecContext(ctx, "INSERT INTO t VALUES(5,5+1,5)"); err != nil {
		t.Fatal(err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := held.QueryContext(canceled, 1); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(canceled, db, strings.NewReader("CREATE TABLE replacement(i INT); INSERT INTO replacement VALUES(1+2)")); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	raw, err := d.Open(t.Name())
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	cursorContext, stop := context.WithCancel(ctx)
	cursor, err := raw.(driver.QueryerContext).QueryContext(cursorContext, "SELECT i+1 FROM t ORDER BY id", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cursor.Close()
	stop()
	dst := make([]driver.Value, 1)
	for range 2 {
		if err := cursor.Next(dst); !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	}
	script := "CREATE TABLE replacement(i INT); INSERT INTO replacement VALUES(/*é*/(7+$1)*2)"
	err = stuntgres.Restore(ctx, db, strings.NewReader(script))
	position := len([]rune(script[:strings.Index(script, "$1")])) + 1
	arithmeticError(t, err, stuntgres.Error{Code: "42P02", Message: "parameters are not allowed in restore", Position: strconv.Itoa(position)})
}
