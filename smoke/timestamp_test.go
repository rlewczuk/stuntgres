package smoke_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"reflect"
	"strconv"
	"strings"
	"stuntgres"
	"testing"
	"time"
	"unicode/utf8"
)

func TestTimestampSmoke(t *testing.T) {
	ctx := context.Background()
	d := stuntgres.NewDriver()
	base, e := d.OpenConnector(t.Name())
	if e != nil {
		t.Fatal(e)
	}
	var notices []stuntgres.Error
	db := sql.OpenDB(base.(*stuntgres.Connector).WithNoticeHandler(func(n stuntgres.Error) { notices = append(notices, n) }))
	defer db.Close()
	db.SetMaxOpenConns(1)
	exec := func(q string, count int64, args ...any) {
		t.Helper()
		r, e := db.ExecContext(ctx, q, args...)
		if e != nil {
			t.Fatal(q, e)
		}
		n, e := r.RowsAffected()
		if e != nil || n != count {
			t.Fatal(n, e)
		}
	}
	check := func(q string, names, types []string, want [][]any, args ...any) {
		t.Helper()
		rows, e := db.QueryContext(ctx, q, args...)
		if e != nil {
			t.Fatal(q, e)
		}
		defer rows.Close()
		cols, e := rows.Columns()
		if e != nil || !reflect.DeepEqual(cols, names) {
			t.Fatal(cols, e)
		}
		metadata, e := rows.ColumnTypes()
		if e != nil {
			t.Fatal(e)
		}
		for i, n := range types {
			if metadata[i].DatabaseTypeName() != n {
				t.Fatal(metadata[i])
			}
			if n == "TIMESTAMP" && metadata[i].ScanType() != reflect.TypeFor[time.Time]() {
				t.Fatal(metadata[i])
			}
		}
		var got [][]any
		for rows.Next() {
			row := make([]any, len(names))
			dst := make([]any, len(names))
			for i := range dst {
				dst[i] = &row[i]
			}
			if e = rows.Scan(dst...); e != nil {
				t.Fatal(e)
			}
			got = append(got, row)
		}
		if e = rows.Err(); e != nil {
			t.Fatal(e)
		}
		if len(got) != len(want) {
			t.Fatal(got, want)
		}
		for i := range got {
			if !reflect.DeepEqual(got[i], want[i]) {
				t.Fatal(got, want)
			}
		}
	}
	diagnostic := func(err error, want stuntgres.Error) {
		t.Helper()
		want.Severity, want.SeverityUnlocalized = "ERROR", "ERROR"
		var got *stuntgres.Error
		if !errors.As(err, &got) || *got != want {
			t.Fatalf("got %#v (%v), want %#v", got, err, want)
		}
	}
	exec("CREATE TABLE t(id INTEGER,v TIMESTAMP,p TIMESTAMP(3) WITHOUT TIME ZONE)", 0)
	exec("CREATE TABLE decoder_forms(id INTEGER,v TIMESTAMP(3))", 0)
	timestampBranchSmoke(t, ctx, db, exec, check)
	exec("INSERT INTO decoder_forms VALUES(1,TIMESTAMP 'J2451187.1'),(2,'1999-01-08 04:05:06.')", 2)
	forms := [][]any{
		{int64(1), time.Date(1999, 1, 8, 2, 24, 0, 0, time.UTC)},
		{int64(2), time.Date(1999, 1, 8, 4, 5, 6, 0, time.UTC)},
		{int64(3), time.Date(1999, 1, 8, 2, 24, 0, 0, time.UTC)},
		{int64(4), time.Date(1999, 1, 8, 4, 5, 6, 0, time.UTC)},
		{int64(5), time.Date(1999, 1, 8, 4, 5, 6, 100000000, time.UTC)},
		{int64(6), time.Date(1999, 1, 8, 4, 5, 6, 100000000, time.UTC)},
		{int64(7), time.Date(1999, 1, 8, 0, 0, 0, 0, time.UTC)},
		{int64(8), time.Date(1999, 1, 8, 4, 5, 6, 0, time.UTC)},
		{int64(9), time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)},
		{int64(10), time.Date(2000, 1, 1, 4, 5, 6, 0, time.UTC)},
		{int64(11), time.Date(1999, 1, 8, 16, 5, 6, 0, time.UTC)},
		{int64(12), time.Date(1999, 1, 8, 0, 0, 0, 0, time.UTC)},
		{int64(15), time.Date(1999, 1, 8, 0, 0, 0, 0, time.UTC)},
		{int64(16), time.Date(1999, 1, 8, 0, 0, 0, 0, time.UTC)},
		{int64(17), time.Date(2004, 10, 19, 10, 23, 54, 123000000, time.UTC)},
		{int64(18), time.Date(2004, 10, 19, 10, 23, 54, 123000000, time.UTC)},
		{int64(19), time.Date(1999, 1, 8, 0, 0, 0, 0, time.UTC)},
		{int64(20), time.Date(1999, 1, 8, 0, 0, 0, 0, time.UTC)},
		{int64(21), time.Date(1999, 1, 8, 0, 0, 0, 0, time.UTC)},
		{int64(22), time.Date(1999, 1, 8, 0, 0, 0, 0, time.UTC)},
		{int64(23), time.Date(1999, 1, 8, 0, 0, 0, 0, time.UTC)},
		{int64(24), time.Date(1999, 1, 8, 0, 0, 0, 0, time.UTC)},
		{int64(25), time.Date(1999, 1, 8, 4, 5, 6, 0, time.UTC)},
		{int64(26), time.Date(1999, 1, 8, 0, 0, 0, 0, time.UTC)},
		{int64(27), time.Date(1999, 1, 1, 0, 0, 0, 0, time.UTC)},
		{int64(28), time.Date(1999, 1, 31, 0, 0, 0, 0, time.UTC)},
		{int64(29), time.Date(1999, 1, 8, 0, 0, 0, 0, time.UTC)},
		{int64(30), time.Date(1999, 1, 8, 0, 0, 0, 0, time.UTC)},
		{int64(31), time.Date(1999, 1, 8, 4, 5, 6, 0, time.UTC)},
		{int64(32), time.Date(1999, 1, 8, 4, 5, 6, 0, time.UTC)},
		{int64(33), time.Date(0, 1, 8, 4, 5, 6, 0, time.UTC)},
		{int64(34), time.Date(1999, 1, 8, 16, 5, 6, 0, time.UTC)},
		{int64(35), time.Date(1999, 1, 8, 2, 24, 0, 0, time.UTC)},
		{int64(36), time.Date(1970, 1, 1, 0, 0, 0, 0, time.UTC)},
		{int64(37), time.Date(1999, 1, 8, 0, 0, 0, 0, time.UTC)},
	}
	abortAtomic := func(input any, want stuntgres.Error) {
		t.Helper()
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		_, err = tx.ExecContext(ctx, "INSERT INTO decoder_forms VALUES(13,'epoch'),(14,$1)", input)
		diagnostic(err, want)
		aborted := stuntgres.Error{Code: "25P02", Message: "current transaction is aborted, commands ignored until end of transaction block"}
		_, err = tx.ExecContext(ctx, "SELECT v FROM decoder_forms")
		diagnostic(err, aborted)
		_, err = tx.ExecContext(ctx, "INSERT INTO decoder_forms VALUES(13,'epoch')")
		diagnostic(err, aborted)
		if err := tx.Rollback(); err != nil {
			t.Fatal(err)
		}
	}
	projection := "SELECT TIMESTAMP 'J2451187.1',TIMESTAMP(3) '1999-01-08 04:05:06.',TIMESTAMP '1999-01-08 04:05:06.1e2' FROM decoder_forms WHERE id=1"
	check(projection, []string{"timestamp", "timestamp", "timestamp"}, []string{"TIMESTAMP", "TIMESTAMP", "TIMESTAMP"}, [][]any{{time.Date(1999, 1, 8, 2, 24, 0, 0, time.UTC), time.Date(1999, 1, 8, 4, 5, 6, 0, time.UTC), time.Date(1999, 1, 8, 4, 5, 6, 100000000, time.UTC)}})
	preparedProjection, e := db.PrepareContext(ctx, projection)
	if e != nil {
		t.Fatal(e)
	}
	var julian, dotted, posixFraction time.Time
	if e = preparedProjection.QueryRowContext(ctx).Scan(&julian, &dotted, &posixFraction); e != nil ||
		julian != time.Date(1999, 1, 8, 2, 24, 0, 0, time.UTC) || dotted != time.Date(1999, 1, 8, 4, 5, 6, 0, time.UTC) ||
		posixFraction != time.Date(1999, 1, 8, 4, 5, 6, 100000000, time.UTC) {
		t.Fatal(julian, dotted, posixFraction, e)
	}
	preparedProjection.Close()
	exec("INSERT INTO decoder_forms VALUES($1,$2)", 1, 3, "J2451187.1")
	exec("INSERT INTO decoder_forms VALUES($1,$2)", 1, 5, "1999-01-08 04:05:06.1e2")
	exec("INSERT INTO decoder_forms VALUES(7,TIMESTAMP '1999-01-08 allballs'),(8,'1999-01-08 04:05:06 weds e2d')", 2)
	exec("INSERT INTO decoder_forms VALUES(9,TIMESTAMP '1999.366'),(10,'1999 366 04:05:06'),(11,'PM January 8 1999 04:05:06'),(12,'1999 01-08')", 4)
	exec("INSERT INTO decoder_forms VALUES(15,TIMESTAMP 'January 08-1999')", 1)
	exec("INSERT INTO decoder_forms VALUES($1,$2)", 1, 17, time.Date(2004, 10, 19, 10, 23, 54, 123456600, time.FixedZone("east", 57599)))
	exec("INSERT INTO decoder_forms VALUES(19,TIMESTAMP '1999-01-08-'),(21,'1999//01//08'),(23,TIMESTAMP '1999 8 January'),(25,'1999-01-08 04:05:06 e2')", 4)
	exec("INSERT INTO decoder_forms VALUES(26,'1999-Jan08'),(27,TIMESTAMP '1999 1 January')", 2)
	exec("INSERT INTO decoder_forms VALUES(29,TIMESTAMP '1999.01.08.')", 1)
	exec("INSERT INTO decoder_forms VALUES(31,TIMESTAMP '1999 8 JaNuArY 04:05:06 WEDS'),(32,'1999-01-08 04:05:06 aMeRiCa/nEw_yOrK'),(33,TIMESTAMP '0001-01-08 04:05:06 bC')", 3)
	exec("INSERT INTO decoder_forms VALUES(34,TIMESTAMP 'On JaNuArY 8 1999 At 04:05:06 pM aD PsT'),(35,'JuLiAn2451187.1'),(36,'EpOcH'),(37,'1999-01-08 aLlBaLlS')", 4)
	preparedForms, e := db.PrepareContext(ctx, "INSERT INTO decoder_forms VALUES($1,$2)")
	if e != nil {
		t.Fatal(e)
	}
	if r, err := preparedForms.ExecContext(ctx, 4, "1999-01-08 04:05:06."); err != nil {
		t.Fatal(err)
	} else if n, err := r.RowsAffected(); err != nil || n != 1 {
		t.Fatal(n, err)
	}
	if r, err := preparedForms.ExecContext(ctx, 6, "1999-01-08 04:05:06.1e2"); err != nil {
		t.Fatal(err)
	} else if n, err := r.RowsAffected(); err != nil || n != 1 {
		t.Fatal(n, err)
	}
	for _, tc := range []struct {
		id    int
		input any
	}{
		{16, "1999-Jan-08"},
		{18, time.Date(2004, 10, 19, 10, 23, 54, 123456600, time.FixedZone("west", -57599))},
		{20, "1999/01/08/"},
		{22, "1999--01--08"},
		{24, "1999 8 January"},
		{28, "1999 31 January"},
		{30, "1999.01..08"},
	} {
		if r, err := preparedForms.ExecContext(ctx, tc.id, tc.input); err != nil {
			t.Fatal(err)
		} else if n, err := r.RowsAffected(); err != nil || n != 1 {
			t.Fatal(n, err)
		}
	}
	preparedForms.Close()
	check("SELECT * FROM decoder_forms ORDER BY id", []string{"id", "v"}, []string{"INT4", "TIMESTAMP"}, forms)
	for _, tc := range []struct{ input, code, message string }{
		{"1999 1.5 8", "22007", "invalid input syntax for type timestamp: \"1999 1.5 8\""},
		{"1999 1.1 8", "22007", "invalid input syntax for type timestamp: \"1999 1.1 8\""},
		{"4714-11-23 000000 BC", "22008", "timestamp out of range: \"4714-11-23 000000 BC\""},
		{"J .1 04:05:06", "22007", "invalid input syntax for type timestamp: \"J .1 04:05:06\""},
		{"J . 04:05:06", "22007", "invalid input syntax for type timestamp: \"J . 04:05:06\""},
		{"1999-01-08 250000 PM", "22008", "date/time field value out of range: \"1999-01-08 250000 PM\""},
		{"EDT 1999.008", "22007", "invalid input syntax for type timestamp: \"EDT 1999.008\""},
		{"PST dst 1999.008", "22007", "invalid input syntax for type timestamp: \"PST dst 1999.008\""},
		{"1999-01-08 UTC America/New_York", "22007", "invalid input syntax for type timestamp: \"1999-01-08 UTC America/New_York\""},
		{"1999-01-08 UTC America/Bogus", "22023", "time zone \"america/bogus\" not recognized"},
		{"294276-12-31 250000", "22008", "timestamp out of range: \"294276-12-31 250000\""},
		{"1999-01-08 25:00:00", "22008", "date/time field value out of range: \"1999-01-08 25:00:00\""},
		{"1999-01-08 04:99:00", "22008", "date/time field value out of range: \"1999-01-08 04:99:00\""},
		{"1999-01-08 04:05:99", "22008", "date/time field value out of range: \"1999-01-08 04:05:99\""},
		{"1999-01-08 T 2147483648", "22008", "date/time field value out of range: \"1999-01-08 T 2147483648\""},
		{"1999-01-08 04:05:06 T 2147483648", "22008", "date/time field value out of range: \"1999-01-08 04:05:06 T 2147483648\""},
		{"1999-01-08 04:05:06 T 25:00:00", "22008", "date/time field value out of range: \"1999-01-08 04:05:06 T 25:00:00\""},
		{"J -2147483648", "22009", "time zone displacement out of range: \"J -2147483648\""},
		{"J on", "22007", "invalid input syntax for type timestamp: \"J on\""},
		{"J J2451187", "22007", "invalid input syntax for type timestamp: \"J J2451187\""},
		{"J 04:05:06 2451187", "22007", "invalid input syntax for type timestamp: \"J 04:05:06 2451187\""},
		{"1999-01-08 04:05:06 T 040506", "22007", "invalid input syntax for type timestamp: \"1999-01-08 04:05:06 T 040506\""},
		{"1999-01-08 T America/New_York 040506", "22007", "invalid input syntax for type timestamp: \"1999-01-08 T America/New_York 040506\""},
		{"1999-01-08 T America/Bogus", "22007", "invalid input syntax for type timestamp: \"1999-01-08 T America/Bogus\""},
		{"1999-01-08 04:05:06 EDT dst", "22007", "invalid input syntax for type timestamp: \"1999-01-08 04:05:06 EDT dst\""},
		{"1999-01-08 04:05:06 dst EDT", "22007", "invalid input syntax for type timestamp: \"1999-01-08 04:05:06 dst EDT\""},
		{"1999-01-08 04:05:06 MSK dst", "22007", "invalid input syntax for type timestamp: \"1999-01-08 04:05:06 MSK dst\""},
		{"1999-01-08 04:05:06 dst MSK", "22007", "invalid input syntax for type timestamp: \"1999-01-08 04:05:06 dst MSK\""},
		{"1999-01-08 04:05:06 America/New_York dst", "22007", "invalid input syntax for type timestamp: \"1999-01-08 04:05:06 America/New_York dst\""},
		{"1999-01-08 04:05:06 dst America/New_York", "22007", "invalid input syntax for type timestamp: \"1999-01-08 04:05:06 dst America/New_York\""},
		{"1999-01-08 04:05:06 FOOBAR0 dst", "22007", "invalid input syntax for type timestamp: \"1999-01-08 04:05:06 FOOBAR0 dst\""},
		{"1999-01-08 04:05:06 dst FOOBAR0", "22007", "invalid input syntax for type timestamp: \"1999-01-08 04:05:06 dst FOOBAR0\""},
		{"1999-01-08 04:05:06 dst", "22007", "invalid input syntax for type timestamp: \"1999-01-08 04:05:06 dst\""},
		{"1999-01-08 04:05:06 PST dst dst", "22007", "invalid input syntax for type timestamp: \"1999-01-08 04:05:06 PST dst dst\""},
		{"1999-01-08 04:05:06 dst PST UTC", "22007", "invalid input syntax for type timestamp: \"1999-01-08 04:05:06 dst PST UTC\""},
		{"EDT 1999-01-08", "22007", "invalid input syntax for type timestamp: \"EDT 1999-01-08\""},
		{"PST dst 1999-01-08", "22007", "invalid input syntax for type timestamp: \"PST dst 1999-01-08\""},
		{"at at at at at at at at at at at at at at at at at at at at at at at at 1999-01-08!", "22007", "invalid input syntax for type timestamp: \"at at at at at at at at at at at at at at at at at at at at at at at at 1999-01-08!\""},
		{"at at at at at at at at at at at at at at at at at at at at at at at at at 1999-01-08", "22007", "invalid input syntax for type timestamp: \"at at at at at at at at at at at at at at at at at at at at at at at at at 1999-01-08\""},
		{"1999-01-08\u007f", "22007", "invalid input syntax for type timestamp: \"1999-01-08\u007f\""},
		{"\u007f1999-01-08", "22007", "invalid input syntax for type timestamp: \"\u007f1999-01-08\""},
		{"y 2147483648", "22008", "date/time field value out of range: \"y 2147483648\""},
		{"y 1", "22007", "invalid input syntax for type timestamp: \"y 1\""},
		{"y on 2147483648", "22008", "date/time field value out of range: \"y on 2147483648\""},
		{"m 2147483648", "22008", "date/time field value out of range: \"m 2147483648\""},
		{"m 1", "22007", "invalid input syntax for type timestamp: \"m 1\""},
		{"m on 2147483648", "22008", "date/time field value out of range: \"m on 2147483648\""},
		{"d 2147483648", "22008", "date/time field value out of range: \"d 2147483648\""},
		{"d 1", "22007", "invalid input syntax for type timestamp: \"d 1\""},
		{"d on 2147483648", "22008", "date/time field value out of range: \"d on 2147483648\""},
		{"h 2147483648", "22008", "date/time field value out of range: \"h 2147483648\""},
		{"h 1", "22007", "invalid input syntax for type timestamp: \"h 1\""},
		{"h on 2147483648", "22008", "date/time field value out of range: \"h on 2147483648\""},
		{"mm 2147483648", "22008", "date/time field value out of range: \"mm 2147483648\""},
		{"mm 1", "22007", "invalid input syntax for type timestamp: \"mm 1\""},
		{"mm on 2147483648", "22008", "date/time field value out of range: \"mm on 2147483648\""},
		{"s 2147483648", "22008", "date/time field value out of range: \"s 2147483648\""},
		{"s 1", "22007", "invalid input syntax for type timestamp: \"s 1\""},
		{"s on 2147483648", "22008", "date/time field value out of range: \"s on 2147483648\""},
		{"dow 2147483648", "22008", "date/time field value out of range: \"dow 2147483648\""},
		{"dow 1", "22007", "invalid input syntax for type timestamp: \"dow 1\""},
		{"dow on 2147483648", "22008", "date/time field value out of range: \"dow on 2147483648\""},
		{"doy 2147483648", "22008", "date/time field value out of range: \"doy 2147483648\""},
		{"doy 1", "22007", "invalid input syntax for type timestamp: \"doy 1\""},
		{"doy on 2147483648", "22008", "date/time field value out of range: \"doy on 2147483648\""},
		{"isodow 2147483648", "22008", "date/time field value out of range: \"isodow 2147483648\""},
		{"isodow 1", "22007", "invalid input syntax for type timestamp: \"isodow 1\""},
		{"isodow on 2147483648", "22008", "date/time field value out of range: \"isodow on 2147483648\""},
		{"isoyear 2147483648", "22008", "date/time field value out of range: \"isoyear 2147483648\""},
		{"isoyear 1", "22007", "invalid input syntax for type timestamp: \"isoyear 1\""},
		{"isoyear on 2147483648", "22008", "date/time field value out of range: \"isoyear on 2147483648\""},
		{"1999-01-08 04:05:06 + 16", "22009", "time zone displacement out of range: \"1999-01-08 04:05:06 + 16\""},
		{"1999-01-08 04:05:06 +\t16", "22009", "time zone displacement out of range: \"1999-01-08 04:05:06 +\t16\""},
		{"1999-01-08 04:05:06 +\n16", "22009", "time zone displacement out of range: \"1999-01-08 04:05:06 +\n16\""},
		{"1999-01-08 04:05:06 +\r16", "22009", "time zone displacement out of range: \"1999-01-08 04:05:06 +\r16\""},
		{"1999-01-08 04:05:06 +\f16", "22009", "time zone displacement out of range: \"1999-01-08 04:05:06 +\f16\""},
		{"1999-01-08 04:05:06 +\u000b16", "22009", "time zone displacement out of range: \"1999-01-08 04:05:06 +\u000b16\""},
		{"1999-01-08 04:05:06 - 16", "22009", "time zone displacement out of range: \"1999-01-08 04:05:06 - 16\""},
		{"1999-01-08 04:05:06 -\t16", "22009", "time zone displacement out of range: \"1999-01-08 04:05:06 -\t16\""},
		{"1999-01-08 04:05:06 -\n16", "22009", "time zone displacement out of range: \"1999-01-08 04:05:06 -\n16\""},
		{"1999-01-08 04:05:06 -\r16", "22009", "time zone displacement out of range: \"1999-01-08 04:05:06 -\r16\""},
		{"1999-01-08 04:05:06 -\f16", "22009", "time zone displacement out of range: \"1999-01-08 04:05:06 -\f16\""},
		{"1999-01-08 04:05:06 -\u000b16", "22009", "time zone displacement out of range: \"1999-01-08 04:05:06 -\u000b16\""},
		{"January 8 04:05:06 2147483648", "22008", `date/time field value out of range: "January 8 04:05:06 2147483648"`},
		{"1999-01-08T2147483648", "22008", `date/time field value out of range: "1999-01-08T2147483648"`},
		{"epoch 1999-01-08", "22007", `invalid input syntax for type timestamp: "epoch 1999-01-08"`},
		{"1999-01-08 epoch", "22007", `invalid input syntax for type timestamp: "1999-01-08 epoch"`},
		{"epoch 04:05:06", "22007", `invalid input syntax for type timestamp: "epoch 04:05:06"`},
		{"04:05:06 epoch", "22007", `invalid input syntax for type timestamp: "04:05:06 epoch"`},
		{"epoch UTC", "22007", `invalid input syntax for type timestamp: "epoch UTC"`},
		{"UTC epoch", "22007", `invalid input syntax for type timestamp: "UTC epoch"`},
		{"epoch +02", "22007", `invalid input syntax for type timestamp: "epoch +02"`},
		{"+02 epoch", "22007", `invalid input syntax for type timestamp: "+02 epoch"`},
		{"infinity 1999-01-08", "22007", `invalid input syntax for type timestamp: "infinity 1999-01-08"`},
		{"1999-01-08 infinity", "22007", `invalid input syntax for type timestamp: "1999-01-08 infinity"`},
		{"infinity 04:05:06", "22007", `invalid input syntax for type timestamp: "infinity 04:05:06"`},
		{"04:05:06 infinity", "22007", `invalid input syntax for type timestamp: "04:05:06 infinity"`},
		{"infinity UTC", "22007", `invalid input syntax for type timestamp: "infinity UTC"`},
		{"UTC infinity", "22007", `invalid input syntax for type timestamp: "UTC infinity"`},
		{"infinity +02", "22007", `invalid input syntax for type timestamp: "infinity +02"`},
		{"+02 infinity", "22007", `invalid input syntax for type timestamp: "+02 infinity"`},
		{"+infinity 1999-01-08", "22007", `invalid input syntax for type timestamp: "+infinity 1999-01-08"`},
		{"1999-01-08 +infinity", "22007", `invalid input syntax for type timestamp: "1999-01-08 +infinity"`},
		{"+infinity 04:05:06", "22007", `invalid input syntax for type timestamp: "+infinity 04:05:06"`},
		{"04:05:06 +infinity", "22007", `invalid input syntax for type timestamp: "04:05:06 +infinity"`},
		{"+infinity UTC", "22007", `invalid input syntax for type timestamp: "+infinity UTC"`},
		{"UTC +infinity", "22007", `invalid input syntax for type timestamp: "UTC +infinity"`},
		{"+infinity +02", "22007", `invalid input syntax for type timestamp: "+infinity +02"`},
		{"+02 +infinity", "22007", `invalid input syntax for type timestamp: "+02 +infinity"`},
		{"- InFiNiTy 1999-01-08", "22007", `invalid input syntax for type timestamp: "- InFiNiTy 1999-01-08"`},
		{"1999-01-08 - InFiNiTy", "22007", `invalid input syntax for type timestamp: "1999-01-08 - InFiNiTy"`},
		{"- InFiNiTy 04:05:06", "22007", `invalid input syntax for type timestamp: "- InFiNiTy 04:05:06"`},
		{"04:05:06 - InFiNiTy", "22007", `invalid input syntax for type timestamp: "04:05:06 - InFiNiTy"`},
		{"- InFiNiTy UTC", "22007", `invalid input syntax for type timestamp: "- InFiNiTy UTC"`},
		{"UTC - InFiNiTy", "22007", `invalid input syntax for type timestamp: "UTC - InFiNiTy"`},
		{"- InFiNiTy +02", "22007", `invalid input syntax for type timestamp: "- InFiNiTy +02"`},
		{"+02 - InFiNiTy", "22007", `invalid input syntax for type timestamp: "+02 - InFiNiTy"`},
		{"epoch epoch", "22007", `invalid input syntax for type timestamp: "epoch epoch"`},
		{"infinity -infinity", "22007", `invalid input syntax for type timestamp: "infinity -infinity"`},
		{"on epoch at nonsense", "22007", `invalid input syntax for type timestamp: "on epoch at nonsense"`},
		{"2147483648-on-08", "22008", `date/time field value out of range: "2147483648-on-08"`},
		{"1999-at-08", "22007", `invalid input syntax for type timestamp: "1999-at-08"`},
		{"1999-on-08", "22007", `invalid input syntax for type timestamp: "1999-on-08"`},
		{"1999.01.08..", "22007", `invalid input syntax for type timestamp: "1999.01.08.."`},
		{"1999-01-08-1", "22007", `invalid input syntax for type timestamp: "1999-01-08-1"`},
		{"1999 0 January", "22007", `invalid input syntax for type timestamp: "1999 0 January"`},
		{"1999 32 January", "22007", `invalid input syntax for type timestamp: "1999 32 January"`},
		{"1999 31 February", "22008", `date/time field value out of range: "1999 31 February"`},
		{"2147483648-at-08", "22008", `date/time field value out of range: "2147483648-at-08"`},
		{"1999-01-08-2147483648", "22008", `date/time field value out of range: "1999-01-08-2147483648"`},
		{"2147483648-xxx-08", "22007", `invalid input syntax for type timestamp: "2147483648-xxx-08"`},
		{"0 January 1999", "22007", `invalid input syntax for type timestamp: "0 January 1999"`},
		{"32 January 1999", "22007", `invalid input syntax for type timestamp: "32 January 1999"`},
		{"1999-01-08--", "22007", `invalid input syntax for type timestamp: "1999-01-08--"`},
		{"1999/01/08//", "22007", `invalid input syntax for type timestamp: "1999/01/08//"`},
		{"99 January 1999", "22007", `invalid input syntax for type timestamp: "99 January 1999"`},
		{"1999-01-08 04:05:06 now1", "22007", `invalid input syntax for type timestamp: "1999-01-08 04:05:06 now1"`},
		{"1999-01-08 04:05:06 now+2", "22007", `invalid input syntax for type timestamp: "1999-01-08 04:05:06 now+2"`},
		{"1999-01-08 04:05:06 today1", "22007", `invalid input syntax for type timestamp: "1999-01-08 04:05:06 today1"`},
		{"1999-01-08 04:05:06 today+2", "22007", `invalid input syntax for type timestamp: "1999-01-08 04:05:06 today+2"`},
		{"1999-01-08 04:05:06 tomorrow1", "22007", `invalid input syntax for type timestamp: "1999-01-08 04:05:06 tomorrow1"`},
		{"1999-01-08 04:05:06 tomorrow+2", "22007", `invalid input syntax for type timestamp: "1999-01-08 04:05:06 tomorrow+2"`},
		{"1999-01-08 04:05:06 yesterday1", "22007", `invalid input syntax for type timestamp: "1999-01-08 04:05:06 yesterday1"`},
		{"1999-01-08 04:05:06 yesterday+2", "22007", `invalid input syntax for type timestamp: "1999-01-08 04:05:06 yesterday+2"`},
		{"1999-01-08 04:05:06 epoch1", "22007", `invalid input syntax for type timestamp: "1999-01-08 04:05:06 epoch1"`},
		{"1999-01-08 04:05:06 epoch+2", "22007", `invalid input syntax for type timestamp: "1999-01-08 04:05:06 epoch+2"`},
		{"1999-01-08 04:05:06 infinity1", "22007", `invalid input syntax for type timestamp: "1999-01-08 04:05:06 infinity1"`},
		{"1999-01-08 04:05:06 infinity+2", "22007", `invalid input syntax for type timestamp: "1999-01-08 04:05:06 infinity+2"`},
		{"1999-01-08 04:05:06 allballs1", "22007", `invalid input syntax for type timestamp: "1999-01-08 04:05:06 allballs1"`},
		{"1999-01-08 04:05:06 allballs+2", "22007", `invalid input syntax for type timestamp: "1999-01-08 04:05:06 allballs+2"`},
		{"Friday 1999-01-08 04:05:06", "22007", `invalid input syntax for type timestamp: "Friday 1999-01-08 04:05:06"`},
		{"BC 0001-01-01", "22007", `invalid input syntax for type timestamp: "BC 0001-01-01"`},
		{"PM 1999-01-08 04:05:06", "22007", `invalid input syntax for type timestamp: "PM 1999-01-08 04:05:06"`},
		{"Friday 1999.008 04:05:06", "22007", `invalid input syntax for type timestamp: "Friday 1999.008 04:05:06"`},
		{"1999.367", "22007", `invalid input syntax for type timestamp: "1999.367"`},
		{"294277.001", "22008", `timestamp out of range: "294277.001"`},
		{"allballs 1999-01-08", "22007", `invalid input syntax for type timestamp: "allballs 1999-01-08"`},
		{"04:05:06 1999-01-08", "22007", `invalid input syntax for type timestamp: "04:05:06 1999-01-08"`},
		{"allballs 1999.008", "22007", `invalid input syntax for type timestamp: "allballs 1999.008"`},
		{"04:05:06 1999.008", "22007", `invalid input syntax for type timestamp: "04:05:06 1999.008"`},
		{"J2451187.", "22007", `invalid input syntax for type timestamp: "J2451187."`},
		{"1999-01-08 040506.", "22007", `invalid input syntax for type timestamp: "1999-01-08 040506."`},
		{"J2451187.0 04:05:06", "22007", `invalid input syntax for type timestamp: "J2451187.0 04:05:06"`},
		{"2147483648-01-01", "22008", `date/time field value out of range: "2147483648-01-01"`},
		{"1999-01-08 2147483648:00:00", "22008", `date/time field value out of range: "1999-01-08 2147483648:00:00"`},
		{"1999-01-08 04:05:06+2147483648", "22009", `time zone displacement out of range: "1999-01-08 04:05:06+2147483648"`},
		{"now UTC", "22007", `invalid input syntax for type timestamp: "now UTC"`},
		{"UTC now", "22007", `invalid input syntax for type timestamp: "UTC now"`},
		{"1999-01-08 allballs UTC", "22007", `invalid input syntax for type timestamp: "1999-01-08 allballs UTC"`},
		{"1999-01-08 UTC allballs", "22007", `invalid input syntax for type timestamp: "1999-01-08 UTC allballs"`},
	} {
		q := "/*é*/ SELECT TIMESTAMP '" + tc.input + "' FROM t"
		want := stuntgres.Error{Code: tc.code, Message: tc.message, Position: strconv.Itoa(utf8.RuneCountInString(q[:strings.Index(q, "'")]) + 1)}
		_, e = db.ExecContext(ctx, q)
		diagnostic(e, want)
		bad, err := db.PrepareContext(ctx, q)
		if bad != nil {
			bad.Close()
			t.Fatal("bad decoder literal exposed handle")
		}
		diagnostic(err, want)
		atomic := "INSERT INTO decoder_forms VALUES(9,'epoch'),(10,$1)"
		_, e = db.ExecContext(ctx, atomic, tc.input)
		diagnostic(e, stuntgres.Error{Code: tc.code, Message: tc.message})
		prepared, err := db.PrepareContext(ctx, atomic)
		if err != nil {
			t.Fatal(err)
		}
		_, e = prepared.ExecContext(ctx, tc.input)
		prepared.Close()
		diagnostic(e, stuntgres.Error{Code: tc.code, Message: tc.message})
		abortAtomic(tc.input, stuntgres.Error{Code: tc.code, Message: tc.message})
		check("SELECT * FROM decoder_forms ORDER BY id", []string{"id", "v"}, []string{"INT4", "TIMESTAMP"}, forms)
	}
	for _, input := range []string{"1999 367", "1999 000", "January 1999-08", "Jan 1999/08", "1999 1 0.5", "1999 1 32.5"} {
		q := "/*é*/ SELECT TIMESTAMP '" + input + "' FROM t"
		want := stuntgres.Error{
			Code: "22008", Message: `date/time field value out of range: "` + input + `"`,
			Hint:     `Perhaps you need a different "DateStyle" setting.`,
			Position: strconv.Itoa(utf8.RuneCountInString(q[:strings.Index(q, "'")]) + 1),
		}
		_, e = db.ExecContext(ctx, q)
		diagnostic(e, want)
		bad, err := db.PrepareContext(ctx, q)
		if bad != nil {
			bad.Close()
			t.Fatal("bad partial-date literal exposed handle")
		}
		diagnostic(err, want)
		want.Position = ""
		atomic := "INSERT INTO decoder_forms VALUES(13,'epoch'),(14,$1)"
		_, e = db.ExecContext(ctx, atomic, input)
		diagnostic(e, want)
		prepared, err := db.PrepareContext(ctx, atomic)
		if err != nil {
			t.Fatal(err)
		}
		_, e = prepared.ExecContext(ctx, input)
		prepared.Close()
		diagnostic(e, want)
		abortAtomic(input, want)
		check("SELECT * FROM decoder_forms ORDER BY id", []string{"id", "v"}, []string{"INT4", "TIMESTAMP"}, forms)
	}
	for _, tc := range []struct {
		input         time.Time
		code, message string
	}{
		{time.Date(294277, 1, 1, 0, 0, 0, 0, time.UTC), "22008", `timestamp out of range: "294277-01-01 00:00:00Z"`},
		{time.Date(2004, 10, 19, 10, 23, 54, 0, time.FixedZone("east", 57600)), "22009", `time zone displacement out of range: "2004-10-19 10:23:54+16:00"`},
		{time.Date(2004, 10, 19, 10, 23, 54, 0, time.FixedZone("west", -57600)), "22009", `time zone displacement out of range: "2004-10-19 10:23:54-16:00"`},
		{time.Date(294277, 1, 1, 0, 0, 0, 0, time.FixedZone("east", 57600)), "22009", `time zone displacement out of range: "294277-01-01 00:00:00+16:00"`},
		{time.Date(-5000, 1, 1, 0, 0, 0, 0, time.FixedZone("west", -57600)), "22009", `time zone displacement out of range: "5001-01-01 00:00:00-16:00 BC"`},
		{time.Date(2147483648, 1, 1, 0, 0, 0, 0, time.FixedZone("east", 57600)), "22008", `date/time field value out of range: "2147483648-01-01 00:00:00+16:00"`},
		{time.Date(-2147483647, 1, 1, 0, 0, 0, 0, time.FixedZone("west", -57600)), "22008", `date/time field value out of range: "2147483648-01-01 00:00:00-16:00 BC"`},
		{time.Date(2004, 10, 19, 10, 23, 54, 123456789, time.FixedZone("east", 57600)), "22009", `time zone displacement out of range: "2004-10-19 10:23:54.123456789+16:00"`},
		{time.Date(2004, 10, 19, 10, 23, 54, 123456789, time.FixedZone("west", -57600)), "22009", `time zone displacement out of range: "2004-10-19 10:23:54.123456789-16:00"`},
		{time.Date(294277, 1, 1, 0, 0, 0, 0, time.FixedZone("west", -57600)), "22009", `time zone displacement out of range: "294277-01-01 00:00:00-16:00"`},
		{time.Date(294277, 1, 1, 0, 0, 0, 0, time.FixedZone("east", 57599)), "22008", `timestamp out of range: "294277-01-01 00:00:00+15:59:59"`},
		{time.Date(294277, 1, 1, 0, 0, 0, 0, time.FixedZone("west", -57599)), "22008", `timestamp out of range: "294277-01-01 00:00:00-15:59:59"`},
	} {
		for _, q := range []string{"INSERT INTO decoder_forms VALUES(13,$1)", "INSERT INTO decoder_forms VALUES(13,'epoch'),(14,$1)"} {
			want := stuntgres.Error{Code: tc.code, Message: tc.message}
			_, e = db.ExecContext(ctx, q, tc.input)
			diagnostic(e, want)
			prepared, err := db.PrepareContext(ctx, q)
			if err != nil {
				t.Fatal("time parameter Prepare must succeed", err)
			}
			_, e = prepared.ExecContext(ctx, tc.input)
			prepared.Close()
			diagnostic(e, want)
			check("SELECT * FROM decoder_forms ORDER BY id", []string{"id", "v"}, []string{"INT4", "TIMESTAMP"}, forms)
		}
		abortAtomic(tc.input, stuntgres.Error{Code: tc.code, Message: tc.message})
		check("SELECT * FROM decoder_forms ORDER BY id", []string{"id", "v"}, []string{"INT4", "TIMESTAMP"}, forms)
	}
	bad, e := db.PrepareContext(ctx, "SELECT TIMESTAMP 'nonsense' FROM t")
	if bad != nil {
		bad.Close()
		t.Fatal("bad constant exposed handle")
	}
	diagnostic(e, stuntgres.Error{Code: "22007", Message: `invalid input syntax for type timestamp: "nonsense"`, Position: "18"})
	exec("INSERT INTO t VALUES(1,'2004-10-19 10:23:54+02','1999-12-31 23:59:59.9996'),(2,NULL,NULL)", 2)
	check("SELECT * FROM t ORDER BY id", []string{"id", "v", "p"}, []string{"INT4", "TIMESTAMP", "TIMESTAMP"}, [][]any{{int64(1), time.Date(2004, 10, 19, 10, 23, 54, 0, time.UTC), time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)}, {int64(2), nil, nil}})
	check("SELECT TIMESTAMP '1999-01-08T04:05:06',TIMESTAMP(3) WITHOUT TIME ZONE '1999-01-08 04:05:06.7896+02' FROM t ORDER BY id", []string{"timestamp", "timestamp"}, []string{"TIMESTAMP", "TIMESTAMP"}, [][]any{{time.Date(1999, 1, 8, 4, 5, 6, 0, time.UTC), time.Date(1999, 1, 8, 4, 5, 6, 790000000, time.UTC)}, {time.Date(1999, 1, 8, 4, 5, 6, 0, time.UTC), time.Date(1999, 1, 8, 4, 5, 6, 790000000, time.UTC)}})
	ins, e := db.PrepareContext(ctx, "INSERT INTO t VALUES($1,$2,$3)")
	if e != nil {
		t.Fatal(e)
	}
	defer ins.Close()
	for _, args := range [][]any{{3, "0001-01-01 BC", "infinity"}, {4, []byte("-infinity"), nil}, {5, time.Date(2004, 10, 19, 10, 23, 54, 123456600, time.FixedZone("east", 7200)), "epoch"}} {
		r, e := ins.ExecContext(ctx, args...)
		if e != nil {
			t.Fatal(e)
		}
		n, _ := r.RowsAffected()
		if n != 1 {
			t.Fatal(n)
		}
	}
	check("SELECT v FROM t ORDER BY v", []string{"v"}, []string{"TIMESTAMP"}, [][]any{{[]byte("-infinity")}, {time.Date(0, 1, 1, 0, 0, 0, 0, time.UTC)}, {time.Date(2004, 10, 19, 10, 23, 54, 0, time.UTC)}, {time.Date(2004, 10, 19, 10, 23, 54, 123457000, time.UTC)}, {nil}})
	predicate, e := db.PrepareContext(ctx, "SELECT id FROM t WHERE v=$1 ORDER BY id")
	if e != nil {
		t.Fatal(e)
	}
	defer predicate.Close()
	rows, e := predicate.QueryContext(ctx, "2004-10-19 10:23:54+02")
	if e != nil {
		t.Fatal(e)
	}
	if !rows.Next() {
		t.Fatal("missing local value")
	}
	var id int64
	if e = rows.Scan(&id); e != nil || id != 1 {
		t.Fatal(id, e)
	}
	rows.Close()
	check("SELECT id FROM t WHERE v=$1", []string{"id"}, []string{"INT4"}, nil, nil)
	_, e = db.ExecContext(ctx, "INSERT INTO t VALUES(6,'epoch',NULL),(7,'nonsense',NULL)")
	diagnostic(e, stuntgres.Error{Code: "22007", Message: `invalid input syntax for type timestamp: "nonsense"`, Position: "41"})
	check("SELECT id FROM t ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}, {int64(3)}, {int64(4)}, {int64(5)}})
	exec("CREATE TABLE endpoint(v TIMESTAMP(6))", 0)
	exec("INSERT INTO endpoint VALUES(TIMESTAMP(0) '294276-12-31 23:59:59.999999')", 1)
	check("SELECT v FROM endpoint", []string{"v"}, []string{"TIMESTAMP"}, [][]any{{time.Date(294277, 1, 1, 0, 0, 0, 0, time.UTC)}})
	notices = nil
	stmt, e := db.PrepareContext(ctx, "SELECT TIMESTAMP(7) 'epoch' FROM t WHERE id=1")
	if e != nil {
		t.Fatal(e)
	}
	wantNotice := []stuntgres.Error{{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: "22023", Message: "TIMESTAMP(7) precision reduced to maximum allowed, 6", Position: "8"}}
	if !reflect.DeepEqual(notices, wantNotice) {
		t.Fatal(notices, wantNotice)
	}
	notices = nil
	rows, e = stmt.QueryContext(ctx)
	if e != nil {
		t.Fatal(e)
	}
	rows.Close()
	stmt.Close()
	if len(notices) != 0 {
		t.Fatal("prepared warning repeated", notices)
	}
	notices = nil
	utilitySQL := "CREATE TABLE widened(v TIMESTAMP(7))"
	utility, e := db.PrepareContext(ctx, utilitySQL)
	if e != nil {
		t.Fatal(e)
	}
	if len(notices) != 0 {
		t.Fatal("utility warned at preparation", notices)
	}
	result, e := utility.ExecContext(ctx)
	utility.Close()
	if e != nil {
		t.Fatal(e)
	}
	affected, e := result.RowsAffected()
	if e != nil || affected != 0 {
		t.Fatal(affected, e)
	}
	utilityNotice := []stuntgres.Error{wantNotice[0], wantNotice[0]}
	utilityNotice[0].Position = strconv.Itoa(strings.Index(utilitySQL, "TIMESTAMP") + 1)
	utilityNotice[1].Position = ""
	if !reflect.DeepEqual(notices, utilityNotice) {
		t.Fatal(notices, utilityNotice)
	}
	var dump bytes.Buffer
	if e = stuntgres.Dump(ctx, db, &dump); e != nil {
		t.Fatal(e)
	}
	if e = stuntgres.Reset(ctx, db); e != nil {
		t.Fatal(e)
	}
	if e = stuntgres.Restore(ctx, db, bytes.NewReader(dump.Bytes())); e != nil {
		t.Fatal(e)
	}
	var again bytes.Buffer
	if e = stuntgres.Dump(ctx, db, &again); e != nil || again.String() != dump.String() {
		t.Fatal(again.String(), e)
	}
	check("SELECT v FROM endpoint", []string{"v"}, []string{"TIMESTAMP"}, [][]any{{time.Date(294277, 1, 1, 0, 0, 0, 0, time.UTC)}})
	check("SELECT * FROM decoder_forms ORDER BY id", []string{"id", "v"}, []string{"INT4", "TIMESTAMP"}, forms)
	// Reusing the original Conn-prepared handle preserves its frozen constant.
	// Tx.StmtContext creates an independent preparation on this same connection.
	conn, e := db.Conn(ctx)
	if e != nil {
		t.Fatal(e)
	}
	defer conn.Close()
	literal, e := conn.PrepareContext(ctx, "SELECT TIMESTAMP 'now' FROM t WHERE id=1")
	if e != nil {
		t.Fatal(e)
	}
	defer literal.Close()
	var frozen time.Time
	if e = literal.QueryRowContext(ctx).Scan(&frozen); e != nil {
		t.Fatal(e)
	}
	time.Sleep(3 * time.Millisecond)
	parameter, e := conn.PrepareContext(ctx, "INSERT INTO t VALUES(8,$1,NULL)")
	if e != nil {
		t.Fatal(e)
	}
	defer parameter.Close()
	tx, e := conn.BeginTx(ctx, nil)
	if e != nil {
		t.Fatal(e)
	}
	defer tx.Rollback()
	var now, zonedToday time.Time
	if e = tx.QueryRowContext(ctx, "SELECT TIMESTAMP 'now',TIMESTAMP 'today+2' FROM t WHERE id=1").Scan(&now, &zonedToday); e != nil {
		t.Fatal(e)
	}
	if !now.After(frozen) {
		t.Fatal("clock failed to refresh", now, frozen)
	}
	midnight := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	if zonedToday != midnight {
		t.Fatal("core today plus numeric zone shifted civil fields", zonedToday, midnight)
	}
	var held time.Time
	if e = literal.QueryRowContext(ctx).Scan(&held); e != nil || !held.Equal(frozen) {
		t.Fatal("physical prepared now changed", held, frozen, e)
	}
	// Tx.StmtContext reparses a Conn-prepared statement: its new physical
	// handle captures this transaction's start, not the original handle's value.
	txLiteral := tx.StmtContext(ctx, literal)
	var freshConstant time.Time
	if e = txLiteral.QueryRowContext(ctx).Scan(&freshConstant); e != nil || !freshConstant.Equal(now) {
		t.Fatal("new transaction handle did not capture start", freshConstant, now, e)
	}
	txLiteral.Close()
	if _, e = tx.ExecContext(ctx, "INSERT INTO t VALUES(6,$1,NULL),(7,$1,NULL)", "now"); e != nil {
		t.Fatal(e)
	}
	txParameter := tx.StmtContext(ctx, parameter)
	if _, e = txParameter.ExecContext(ctx, "now"); e != nil {
		t.Fatal(e)
	}
	txParameter.Close()
	rows, e = tx.QueryContext(ctx, "SELECT v FROM t WHERE v=$1", now)
	if e != nil {
		t.Fatal(e)
	}
	count := 0
	for rows.Next() {
		var got time.Time
		if e = rows.Scan(&got); e != nil || !got.Equal(now) {
			t.Fatal(got, e)
		}
		count++
	}
	e = rows.Err()
	rows.Close()
	if e != nil || count != 3 {
		t.Fatal(count, e)
	}
	if e = tx.Commit(); e != nil {
		t.Fatal(e)
	}
	if e = literal.QueryRowContext(ctx).Scan(&held); e != nil || !held.Equal(frozen) {
		t.Fatal("physical handle changed after commit", held, frozen, e)
	}
	time.Sleep(3 * time.Millisecond)
	if _, e = parameter.ExecContext(ctx, "now"); e != nil {
		t.Fatal(e)
	}
	var refreshed time.Time
	if e = conn.QueryRowContext(ctx, "SELECT v FROM t WHERE id=8 ORDER BY v DESC").Scan(&refreshed); e != nil || !refreshed.After(now) {
		t.Fatal("prepared parameter did not refresh", refreshed, e)
	}
}
