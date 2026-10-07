package smoke_test

import (
	"bytes"
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"reflect"
	"stuntgres"
	"testing"
)

func TestIntervalSmoke(t *testing.T) {
	ctx := context.Background()
	drv := stuntgres.NewDriver()
	connector, err := drv.OpenConnector(t.Name())
	if err != nil {
		t.Fatal(err)
	}
	var notices []stuntgres.Error
	db := sql.OpenDB(connector.(*stuntgres.Connector).WithNoticeHandler(func(n stuntgres.Error) { notices = append(notices, n) }))
	defer db.Close()
	exec := func(q string, n int64, args ...any) {
		t.Helper()
		r, e := db.ExecContext(ctx, q, args...)
		if e != nil {
			t.Fatal(q, e)
		}
		if got, e := r.RowsAffected(); e != nil || got != n {
			t.Fatal(q, got, e)
		}
	}
	check := func(pool *sql.DB, q string, want [][]any) {
		t.Helper()
		rows, e := pool.QueryContext(ctx, q)
		if e != nil {
			t.Fatal(q, e)
		}
		defer rows.Close()
		cols, e := rows.Columns()
		if e != nil {
			t.Fatal(e)
		}
		meta, e := rows.ColumnTypes()
		if e != nil {
			t.Fatal(e)
		}
		for _, m := range meta {
			if m.DatabaseTypeName() == "INTERVAL" {
				if m.ScanType() != reflect.TypeFor[string]() {
					t.Fatal(m)
				}
				if _, _, ok := m.DecimalSize(); ok {
					t.Fatal(m)
				}
			}
		}
		var got [][]any
		for rows.Next() {
			row, dst := make([]any, len(cols)), make([]any, len(cols))
			for i := range row {
				dst[i] = &row[i]
			}
			if e = rows.Scan(dst...); e != nil {
				t.Fatal(e)
			}
			got = append(got, row)
		}
		if rows.Err() != nil || !reflect.DeepEqual(got, want) {
			t.Fatal(q, got, want, rows.Err())
		}
	}
	exec("CREATE TABLE iv(id INTEGER,v INTERVAL,y INTERVAL YEAR,m INTERVAL MINUTE TO SECOND(2))", 0)
	exec("INSERT INTO iv VALUES(1,'1 mon','1','1:02.345'),(2,'30 days',NULL,NULL),(3,'720 hours',NULL,NULL),(4,'P2Y10M15DT10H30M20S',NULL,NULL)", 4)
	stmt, err := db.PrepareContext(ctx, "INSERT INTO iv VALUES($1,$2,$3,$4)")
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	r, err := stmt.ExecContext(ctx, 6, []byte("24 hours"), "1", "1:02.345")
	if err != nil {
		t.Fatal(err)
	}
	if n, e := r.RowsAffected(); e != nil || n != 1 {
		t.Fatal(n, e)
	}
	r, err = stmt.ExecContext(ctx, 5, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if n, e := r.RowsAffected(); e != nil || n != 1 {
		t.Fatal(n, e)
	}
	check(db, "SELECT id,v,y,m FROM iv ORDER BY id", [][]any{{int64(1), "1 mon", "1 year", "00:01:02.35"}, {int64(2), "30 days", nil, nil}, {int64(3), "720:00:00", nil, nil}, {int64(4), "2 years 10 mons 15 days 10:30:20", nil, nil}, {int64(5), nil, nil, nil}, {int64(6), "24:00:00", "00:00:00", "00:01:02.35"}})
	check(db, "SELECT id FROM iv WHERE v=INTERVAL '1 mon' ORDER BY id", [][]any{{int64(1)}, {int64(2)}, {int64(3)}})
	exec("CREATE TABLE ivp(v INTERVAL(7))", 0)
	if len(notices) != 2 {
		t.Fatal(notices)
	}
	for i, n := range notices {
		want := stuntgres.Error{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: "22023", Message: "INTERVAL(7) precision reduced to maximum allowed, 6"}
		if i == 0 {
			want.Position = "20"
		}
		if n != want {
			t.Fatal(n, want)
		}
	}
	for _, tc := range []struct {
		q    string
		want stuntgres.Error
	}{
		{"SELECT INTERVAL 'invalid' FROM iv", stuntgres.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22007", Message: "invalid input syntax for type interval: \"invalid\"", Position: "17"}},
		{"CREATE TABLE ivbad(v INTERVAL MONTH TO DAY)", stuntgres.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42601", Message: "syntax error at or near \"TO\"", Position: "37"}},
		{"SET IntervalStyle=invalid", stuntgres.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22023", Message: "invalid value for parameter \"IntervalStyle\": \"invalid\"", Hint: "Available values: postgres, postgres_verbose, sql_standard, iso_8601."}},
	} {
		_, err = db.ExecContext(ctx, tc.q)
		var got *stuntgres.Error
		if !errors.As(err, &got) || *got != tc.want {
			t.Fatal(err, tc.want)
		}
	}
	exec("CREATE TABLE ivbad(v INTERVAL)", 0)
	a, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	b, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	literal, err := a.PrepareContext(ctx, "INSERT INTO iv VALUES(7,INTERVAL '-1 2:03:04',NULL,NULL)")
	if err != nil {
		t.Fatal(err)
	}
	defer literal.Close()
	if _, err = a.ExecContext(ctx, "SET IntervalStyle=sql_standard"); err != nil {
		t.Fatal(err)
	}
	if _, err = literal.ExecContext(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = a.ExecContext(ctx, "INSERT INTO iv VALUES(8,$1,NULL,NULL)", "-1 2:03:04"); err != nil {
		t.Fatal(err)
	}
	var v string
	if err = a.QueryRowContext(ctx, "SELECT v FROM iv WHERE id=7").Scan(&v); err != nil || v != "+0-0 -1 +2:03:04" {
		t.Fatal(v, err)
	}
	if err = a.QueryRowContext(ctx, "SELECT v FROM iv WHERE id=8").Scan(&v); err != nil || v != "-1 2:03:04" {
		t.Fatal(v, err)
	}
	if err = b.QueryRowContext(ctx, "SHOW IntervalStyle").Scan(&v); err != nil || v != "postgres" {
		t.Fatal(v, err)
	}
	for _, tc := range []struct{ style, out string }{{"postgres", "-1 years -2 mons +3 days -04:05:06"}, {"postgres_verbose", "@ 1 year 2 mons -3 days 4 hours 5 mins 6 secs ago"}, {"sql_standard", "-1-2 +3 -4:05:06"}, {"iso_8601", "P-1Y-2M3DT-4H-5M-6S"}} {
		if _, err = a.ExecContext(ctx, "SET IntervalStyle="+tc.style); err != nil {
			t.Fatal(err)
		}
		if err = a.QueryRowContext(ctx, "SELECT INTERVAL '-14 months +3 days -4:05:06' FROM iv WHERE id=1").Scan(&v); err != nil || v != tc.out {
			t.Fatal(tc.style, v, err)
		}
	}
	_, err = stmt.ExecContext(ctx, 9, "invalid", nil, nil)
	var diagnostic *stuntgres.Error
	want := stuntgres.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22007", Message: "invalid input syntax for type interval: \"invalid\""}
	if !errors.As(err, &diagnostic) || *diagnostic != want {
		t.Fatal(err, want)
	}
	_, err = b.ExecContext(ctx, "INSERT INTO iv VALUES(9,$1,NULL,NULL),(10,$2,NULL,NULL)", "1 day", "2147483648 days")
	want.Code = "22015"
	want.Message = "interval field value out of range: \"2147483648 days\""
	if !errors.As(err, &diagnostic) || *diagnostic != want {
		t.Fatal(err, want)
	}
	var count int64
	if err = b.QueryRowContext(ctx, "SELECT COUNT(*) FROM iv").Scan(&count); err != nil || count != 8 {
		t.Fatal(count, err)
	}
	raw, err := drv.Open(t.Name())
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	if _, err = raw.(driver.ExecerContext).ExecContext(ctx, "SET IntervalStyle=iso_8601", nil); err != nil {
		t.Fatal(err)
	}
	held, err := raw.(driver.QueryerContext).QueryContext(ctx, "SELECT v FROM iv WHERE id=1", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	if _, err = raw.(driver.ExecerContext).ExecContext(ctx, "SET IntervalStyle=postgres", nil); err != nil {
		t.Fatal(err)
	}
	var dump bytes.Buffer
	if err = stuntgres.Dump(ctx, db, &dump); err != nil {
		t.Fatal(err)
	}
	restoreConnector, err := drv.OpenConnector(t.Name() + "/restore")
	if err != nil {
		t.Fatal(err)
	}
	dest := sql.OpenDB(restoreConnector)
	defer dest.Close()
	if _, err = dest.Exec("SET IntervalStyle=sql_standard"); err != nil {
		t.Fatal(err)
	}
	if err = stuntgres.Restore(ctx, dest, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	var again bytes.Buffer
	if err = stuntgres.Dump(ctx, dest, &again); err != nil || again.String() != dump.String() {
		t.Fatal(err, "canonical redump mismatch")
	}
	if err = stuntgres.Reset(ctx, db); err != nil {
		t.Fatal(err)
	}
	values := make([]driver.Value, 1)
	if err = held.Next(values); err != nil || values[0] != "P1M" {
		t.Fatal(values, err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if err = stuntgres.Restore(canceled, dest, bytes.NewReader(dump.Bytes())); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if err = stuntgres.Restore(ctx, dest, bytes.NewBufferString("CREATE TABLE bad(v INTERVAL);INSERT INTO bad VALUES('1 day'),('invalid')")); err == nil {
		t.Fatal("bad restore succeeded")
	}
	if err = dest.QueryRow("SELECT COUNT(*) FROM iv").Scan(&count); err != nil || count != 8 {
		t.Fatal(count, err)
	}
}
