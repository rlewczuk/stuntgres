package reference_test

import (
	"bytes"
	"database/sql"
	"database/sql/driver"
	"io"
	"reflect"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceDeleteReturningControls(t *testing.T) {
	p := fixture.NewScenario(t)
	ctx := p.Ctx
	db := p.Native
	for _, q := range []string{"CREATE SEQUENCE s START 101 CACHE 1", "CREATE TABLE t(id INT NOT NULL DEFAULT 9,v TEXT)", "COMMENT ON COLUMN t.v IS 'preserved'", "CREATE VIEW survivors AS SELECT id,v FROM t", "INSERT INTO t VALUES(1,'a'),(2,NULL),(2,NULL),(3,'c')"} {
		if _, err := db.ExecContext(ctx, q); err != nil {
			t.Fatal(q, err)
		}
	}
	borrower, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer borrower.Close()
	err = borrower.Raw(func(raw any) error {
		exec := func(q string) {
			t.Helper()
			if _, err := raw.(driver.ExecerContext).ExecContext(ctx, q, nil); err != nil {
				t.Fatal(q, err)
			}
		}
		exec("BEGIN")
		retained, err := raw.(driver.QueryerContext).QueryContext(ctx, "DELETE FROM t WHERE id=2 RETURNING old.id,new.id,v,nextval('s')", nil)
		if err != nil {
			t.Fatal(err)
		}
		defer retained.Close()
		var private bytes.Buffer
		if err = stuntgres.Dump(ctx, db, &private); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(private.String(), "DELETE") || !strings.Contains(private.String(), "preserved") || !strings.Contains(private.String(), "NOT NULL") {
			t.Fatal(private.String())
		}
		rows, err := db.Query("SELECT id FROM t ORDER BY id")
		fixture.DeleteReferenceConsume(t, false, rows, err, fixture.Select("", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}, {int64(2)}, {int64(3)}}))
		copyConnector, err := stuntgres.NewDriver().OpenConnector(t.Name() + "/private-dump")
		if err != nil {
			t.Fatal(err)
		}
		copyDB := sql.OpenDB(copyConnector)
		defer copyDB.Close()
		if err = stuntgres.Restore(ctx, copyDB, bytes.NewReader(private.Bytes())); err != nil {
			t.Fatal(err)
		}
		copyRows, copyErr := copyDB.Query("SELECT id FROM survivors ORDER BY id")
		fixture.DeleteReferenceConsume(t, false, copyRows, copyErr, fixture.Select("", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}, {int64(2)}, {int64(3)}}))
		exec("ROLLBACK")
		rows, err = db.Query("DELETE FROM t WHERE id=1 RETURNING id")
		fixture.DeleteReferenceConsume(t, false, rows, err, fixture.Select("", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}}))
		var dump bytes.Buffer
		if err = stuntgres.Dump(ctx, db, &dump); err != nil {
			t.Fatal(err)
		}
		if err = stuntgres.Reset(ctx, db); err != nil {
			t.Fatal(err)
		}
		if err = stuntgres.Restore(ctx, db, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}
		rows, err = db.Query("SELECT id,v FROM survivors ORDER BY id")
		fixture.DeleteReferenceConsume(t, false, rows, err, fixture.Select("", []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(2), nil}, {int64(2), nil}, {int64(3), "c"}}))
		var next int64
		if err = db.QueryRow("SELECT nextval('s')").Scan(&next); err != nil || next != 103 {
			t.Fatal("sequence state", next, err)
		}
		if _, err := db.ExecContext(ctx, "INSERT INTO t(v) VALUES('default')"); err != nil {
			t.Fatal(err)
		}
		rows, err = db.Query("SELECT id FROM t ORDER BY id")
		fixture.DeleteReferenceConsume(t, false, rows, err, fixture.Select("", []string{"id"}, []string{"INT4"}, [][]any{{int64(2)}, {int64(2)}, {int64(3)}, {int64(9)}}))
		if _, err = db.Exec("INSERT INTO t VALUES(NULL,'bad')"); err == nil {
			t.Fatal("lost NOT NULL")
		}
		if !reflect.DeepEqual(retained.Columns(), []string{"id", "id", "v", "nextval"}) {
			t.Fatal(retained.Columns())
		}
		meta := retained.(driver.RowsColumnTypeDatabaseTypeName)
		for i, want := range []string{"INT4", "INT4", "TEXT", "INT8"} {
			if meta.ColumnTypeDatabaseTypeName(i) != want {
				t.Fatal(i, meta.ColumnTypeDatabaseTypeName(i))
			}
		}
		dst := make([]driver.Value, 4)
		for _, draw := range []int64{101, 102} {
			if err = retained.Next(dst); err != nil || !reflect.DeepEqual(dst, []driver.Value{int64(2), nil, nil, draw}) {
				t.Fatal(dst, err)
			}
		}
		if err = retained.Next(dst); err != io.EOF {
			t.Fatal(err)
		}
		var sentinel bytes.Buffer
		if err = stuntgres.Dump(ctx, db, &sentinel); err != nil {
			t.Fatal(err)
		}
		if err = stuntgres.Restore(ctx, db, strings.NewReader("DELETE FROM t RETURNING *;")); err == nil {
			t.Fatal("Restore admitted mutation history")
		}
		var after bytes.Buffer
		if err = stuntgres.Dump(ctx, db, &after); err != nil || sentinel.String() != after.String() {
			t.Fatal("failed Restore changed state", err)
		}
		rows, err = db.Query("DELETE FROM t RETURNING *")
		final := fixture.Select("", []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(2), nil}, {int64(2), nil}, {int64(3), "c"}, {int64(9), "default"}})
		final.Bag = true
		fixture.DeleteReferenceConsume(t, false, rows, err, final)
		rows, err = db.Query("SELECT id,v FROM survivors")
		fixture.DeleteReferenceConsume(t, false, rows, err, fixture.Select("", []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{}))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
