package smoke_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"reflect"
	"stuntgres"
	"testing"
)

func TestTimeSmoke(t *testing.T) {
	ctx := context.Background()
	driver := stuntgres.NewDriver()
	raw, err := driver.OpenConnector(t.Name())
	if err != nil {
		t.Fatal(err)
	}
	var notices []stuntgres.Error
	db := sql.OpenDB(raw.(*stuntgres.Connector).WithNoticeHandler(func(n stuntgres.Error) { notices = append(notices, n) }))
	defer db.Close()
	exec := func(q string, count int64, args ...any) {
		t.Helper()
		r, err := db.ExecContext(ctx, q, args...)
		if err != nil {
			t.Fatal(q, err)
		}
		n, err := r.RowsAffected()
		if err != nil || n != count {
			t.Fatal(q, n, err)
		}
	}
	check := func(pool *sql.DB, q string, names, types []string, want [][]any, args ...any) {
		t.Helper()
		rows, err := pool.QueryContext(ctx, q, args...)
		if err != nil {
			t.Fatal(q, err)
		}
		defer rows.Close()
		columns, err := rows.Columns()
		if err != nil || !reflect.DeepEqual(columns, names) {
			t.Fatal(columns, err)
		}
		meta, err := rows.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		for i, typ := range types {
			if meta[i].DatabaseTypeName() != typ || typ == "TIME" && meta[i].ScanType() != reflect.TypeFor[string]() {
				t.Fatal(meta[i])
			}
		}
		var got [][]any
		for rows.Next() {
			row, dst := make([]any, len(names)), make([]any, len(names))
			for i := range row {
				dst[i] = &row[i]
			}
			if err = rows.Scan(dst...); err != nil {
				t.Fatal(err)
			}
			got = append(got, row)
		}
		if err = rows.Err(); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("rows %#v want %#v", got, want)
		}
	}
	exec("CREATE TABLE clocks(id INTEGER,v TIME(0),fine TIME(6))", 0)
	ins, err := db.PrepareContext(ctx, "INSERT INTO clocks VALUES($1,$2,$3)")
	if err != nil {
		t.Fatal(err)
	}
	defer ins.Close()
	for _, tc := range []struct {
		id      int
		v, fine any
	}{{1, "00:00", "00:00"}, {2, "24:00", "24:00"}, {3, "04:05 PM", "04:05:06.120000"}, {4, nil, nil}, {5, "23:59:59.5", "2003-03-07 15:36:39 America/New_York"}, {6, "04:05:06.5", "04:05:06.789-8"}} {
		r, err := ins.ExecContext(ctx, tc.id, tc.v, tc.fine)
		if err != nil {
			t.Fatal(err)
		}
		n, err := r.RowsAffected()
		if err != nil || n != 1 {
			t.Fatal(n, err)
		}
	}
	want := [][]any{{int64(1), "00:00:00", "00:00:00"}, {int64(2), "24:00:00", "24:00:00"}, {int64(3), "16:05:00", "04:05:06.12"}, {int64(4), nil, nil}, {int64(5), "24:00:00", "15:36:39"}, {int64(6), "04:05:07", "04:05:06.789"}}
	check(db, "SELECT id,v,fine FROM clocks ORDER BY id", []string{"id", "v", "fine"}, []string{"INT4", "TIME", "TIME"}, want)
	check(db, "SELECT TIME '24:00',TIME(3) '04:05:06.1234999' FROM clocks WHERE id=1", []string{"time", "time"}, []string{"TIME", "TIME"}, [][]any{{"24:00:00", "04:05:06.124"}})
	held, err := db.PrepareContext(ctx, "SELECT id FROM clocks WHERE v=$1 ORDER BY id")
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	for _, tc := range []struct {
		arg any
		ids []int64
	}{{"00:00", []int64{1}}, {"24:00", []int64{2, 5}}, {nil, nil}} {
		rows, err := held.QueryContext(ctx, tc.arg)
		if err != nil {
			t.Fatal(err)
		}
		var ids []int64
		for rows.Next() {
			var id int64
			if err = rows.Scan(&id); err != nil {
				t.Fatal(err)
			}
			ids = append(ids, id)
		}
		if err = rows.Err(); err != nil {
			t.Fatal(err)
		}
		rows.Close()
		if !reflect.DeepEqual(ids, tc.ids) {
			t.Fatal(ids, tc.ids)
		}
	}
	check(db, "SELECT fine FROM clocks ORDER BY fine", []string{"fine"}, []string{"TIME"}, [][]any{{"00:00:00"}, {"04:05:06.12"}, {"04:05:06.789"}, {"15:36:39"}, {"24:00:00"}, {nil}})
	_, err = db.ExecContext(ctx, "INSERT INTO clocks VALUES(7,'00:00','00:00'),(8,'00:00','bad')")
	var diagnostic *stuntgres.Error
	expected := stuntgres.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22007", Message: "invalid input syntax for type time: \"bad\"", Position: "57"}
	if !errors.As(err, &diagnostic) {
		t.Fatal(err)
	}
	// The original quote position is part of the complete public error record.
	if !reflect.DeepEqual(*diagnostic, expected) {
		t.Fatal(diagnostic, expected)
	}
	check(db, "SELECT id,v,fine FROM clocks ORDER BY id", []string{"id", "v", "fine"}, []string{"INT4", "TIME", "TIME"}, want)
	_, err = db.ExecContext(ctx, "INSERT INTO clocks VALUES(7,'00:00','00:00'),(8,'25:00 UTC UTC','00:00')")
	expected = stuntgres.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22007", Message: "invalid input syntax for type time: \"25:00 UTC UTC\"", Position: "49"}
	if !errors.As(err, &diagnostic) || !reflect.DeepEqual(*diagnostic, expected) {
		t.Fatal(diagnostic, err, expected)
	}
	check(db, "SELECT id,v,fine FROM clocks ORDER BY id", []string{"id", "v", "fine"}, []string{"INT4", "TIME", "TIME"}, want)
	exec("CREATE TABLE ints(v INTEGER)", 0)
	exec("INSERT INTO ints VALUES(9)", 1)
	_, err = db.ExecContext(ctx, "INSERT INTO ints VALUES(1),(TIME '04:05')")
	expected = stuntgres.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42804", Message: "column \"v\" is of type integer but expression is of type time without time zone", Hint: "You will need to rewrite or cast the expression.", Position: "34"}
	if !errors.As(err, &diagnostic) || !reflect.DeepEqual(*diagnostic, expected) {
		t.Fatal(diagnostic, err, expected)
	}
	check(db, "SELECT v FROM ints ORDER BY v", []string{"v"}, []string{"INT4"}, [][]any{{int64(9)}})
	exec("CREATE TABLE capped(v TIME(7))", 0)
	warning := stuntgres.Error{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: "22023", Message: "TIME(7) precision reduced to maximum allowed, 6", Position: "23"}
	plain := warning
	plain.Position = ""
	if !reflect.DeepEqual(notices, []stuntgres.Error{warning, plain}) {
		t.Fatal(notices)
	}
	var dump bytes.Buffer
	if err = stuntgres.Dump(ctx, db, &dump); err != nil {
		t.Fatal(err)
	}
	connector, err := driver.OpenConnector(t.Name() + "/restored")
	if err != nil {
		t.Fatal(err)
	}
	restored := sql.OpenDB(connector)
	defer restored.Close()
	if err = stuntgres.Restore(ctx, restored, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	check(restored, "SELECT id,v,fine FROM clocks ORDER BY id", []string{"id", "v", "fine"}, []string{"INT4", "TIME", "TIME"}, want)
	var redump bytes.Buffer
	if err = stuntgres.Dump(ctx, restored, &redump); err != nil || redump.String() != dump.String() {
		t.Fatal("redump", err)
	}
}
