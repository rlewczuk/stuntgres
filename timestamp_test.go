package stuntgres_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"stuntgres"
	"testing"
	"time"
	"unicode/utf8"
)

func timestampCheckRows(t *testing.T, rows *sql.Rows, names, types []string, want [][]any) {
	t.Helper()
	defer rows.Close()
	gotNames, e := rows.Columns()
	if e != nil || !reflect.DeepEqual(gotNames, names) {
		t.Fatal(gotNames, e)
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
		for i := range row {
			dst[i] = &row[i]
		}
		if e = rows.Scan(dst...); e != nil {
			t.Fatal(e)
		}
		for _, v := range row {
			if tm, ok := v.(time.Time); ok {
				_, off := tm.Zone()
				if off != 0 {
					t.Fatal("timestamp output offset", tm)
				}
			}
		}
		got = append(got, row)
	}
	if e = rows.Err(); e != nil {
		t.Fatal(e)
	}
	if len(got) != len(want) {
		t.Fatalf("rows %#v want %#v", got, want)
	}
	for i := range got {
		if !reflect.DeepEqual(got[i], want[i]) {
			t.Fatalf("row %#v want %#v", got[i], want[i])
		}
	}
}
func timestampQuery(t *testing.T, db *sql.DB, q string, names, types []string, want [][]any, args ...any) {
	t.Helper()
	rows, e := db.Query(q, args...)
	if e != nil {
		t.Fatal(q, e)
	}
	timestampCheckRows(t, rows, names, types, want)
}
func TestTimestampDocumentedInputs(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	execSQL(t, db, "CREATE TABLE t(v TIMESTAMP)")
	execSQL(t, db, "CREATE TABLE anchor(n INTEGER)")
	execSQL(t, db, "INSERT INTO anchor VALUES(1)")
	held, e := db.Prepare("SELECT v FROM t WHERE v=$1")
	if e != nil {
		t.Fatal(e)
	}
	defer held.Close()
	ins, e := db.Prepare("INSERT INTO t VALUES($1)")
	if e != nil {
		t.Fatal(e)
	}
	defer ins.Close()
	for _, tc := range timestampPublicInputs {
		t.Run(tc.input, func(t *testing.T) {
			q := "SELECT TIMESTAMP WITHOUT TIME ZONE '" + tc.input + "' FROM anchor"
			timestampQuery(t, db, q, []string{"timestamp"}, []string{"TIMESTAMP"}, [][]any{{tc.want}})
			literal, e := db.Prepare(q)
			if e != nil {
				t.Fatal(e)
			}
			rows, e := literal.Query()
			if e != nil {
				literal.Close()
				t.Fatal(e)
			}
			timestampCheckRows(t, rows, []string{"timestamp"}, []string{"TIMESTAMP"}, [][]any{{tc.want}})
			literal.Close()
			execSQL(t, db, "INSERT INTO t VALUES($1)", tc.input)
			r, e := ins.Exec(tc.input)
			if e != nil {
				t.Fatal(e)
			}
			n, _ := r.RowsAffected()
			if n != 1 {
				t.Fatal(n)
			}
			rows, e = held.Query(tc.input)
			if e != nil {
				t.Fatal(e)
			}
			defer rows.Close()
			found := false
			for rows.Next() {
				var got any
				if e = rows.Scan(&got); e != nil {
					t.Fatal(e)
				}
				if !reflect.DeepEqual(got, tc.want) {
					t.Fatal(got, tc.want)
				}
				found = true
			}
			if e = rows.Err(); e != nil || !found {
				t.Fatal(found, e)
			}
		})
	}
}
func TestTimestampPrecisionMetadataAndParameters(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	execSQL(t, db, "CREATE TABLE t(id INTEGER,v TIMESTAMP,p TIMESTAMP(3) WITHOUT TIME ZONE)")
	execSQL(t, db, "INSERT INTO t VALUES(1,'2004-10-19 10:23:54+02','2004-10-19 10:23:54.1236'),(2,NULL,NULL)")
	timestampQuery(t, db, "SELECT * FROM t ORDER BY id", []string{"id", "v", "p"}, []string{"INT4", "TIMESTAMP", "TIMESTAMP"}, [][]any{{int64(1), time.Date(2004, 10, 19, 10, 23, 54, 0, time.UTC), time.Date(2004, 10, 19, 10, 23, 54, 124000000, time.UTC)}, {int64(2), nil, nil}})
	for p, ns := range []int{0, 100000000, 120000000, 123000000, 123500000, 123460000, 123456000} {
		q := fmt.Sprintf("SELECT TIMESTAMP(%d) '2000-01-01 00:00:00.123456' FROM t WHERE id=1", p)
		timestampQuery(t, db, q, []string{"timestamp"}, []string{"TIMESTAMP"}, [][]any{{time.Date(2000, 1, 1, 0, 0, 0, ns, time.UTC)}})
	}
	execSQL(t, db, "CREATE TABLE sized(v TIMESTAMP(2))")
	execSQL(t, db, "INSERT INTO sized VALUES('2000-01-01 00:00:00.1245'),(TIMESTAMP(3) '2000-01-01 00:00:00.1245')")
	timestampQuery(t, db, "SELECT v FROM sized ORDER BY v", []string{"v"}, []string{"TIMESTAMP"}, [][]any{{time.Date(2000, 1, 1, 0, 0, 0, 120000000, time.UTC)}, {time.Date(2000, 1, 1, 0, 0, 0, 130000000, time.UTC)}})
	execSQL(t, db, "CREATE TABLE params(v TIMESTAMP(6))")
	ins, e := db.Prepare("INSERT INTO params VALUES($1)")
	if e != nil {
		t.Fatal(e)
	}
	defer ins.Close()
	for _, year := range []int{0, -98, 2004, 12000} {
		raw := time.Date(year, 10, 19, 10, 23, 54, 123456600, time.FixedZone("east", 7200))
		if _, e = ins.Exec(raw); e != nil {
			t.Fatal(e)
		}
	}
	for _, raw := range []any{[]byte("infinity"), "-infinity", nil} {
		if _, e = ins.Exec(raw); e != nil {
			t.Fatal(e)
		}
	}
	timestampQuery(t, db, "SELECT v FROM params ORDER BY v", []string{"v"}, []string{"TIMESTAMP"}, [][]any{{[]byte("-infinity")}, {time.Date(-98, 10, 19, 10, 23, 54, 123457000, time.UTC)}, {time.Date(0, 10, 19, 10, 23, 54, 123457000, time.UTC)}, {time.Date(2004, 10, 19, 10, 23, 54, 123457000, time.UTC)}, {time.Date(12000, 10, 19, 10, 23, 54, 123457000, time.UTC)}, {[]byte("infinity")}, {nil}})
	rows, e := db.Query("SELECT v FROM params WHERE v='infinity'")
	if e != nil {
		t.Fatal(e)
	}
	if !rows.Next() {
		t.Fatal("missing infinity")
	}
	var owned []byte
	if e = rows.Scan(&owned); e != nil {
		t.Fatal(e)
	}
	rows.Close()
	owned[0] = 'X'
	timestampQuery(t, db, "SELECT v FROM params WHERE v='infinity'", []string{"v"}, []string{"TIMESTAMP"}, [][]any{{[]byte("infinity")}})
	rows, e = db.Query("SELECT v FROM params WHERE v='2004-10-19 10:23:54.123457'")
	if e != nil {
		t.Fatal(e)
	}
	if !rows.Next() {
		t.Fatal("missing finite")
	}
	var finite time.Time
	if e = rows.Scan(&finite); e != nil {
		t.Fatal(e)
	}
	rows.Close()
	if finite.Hour() != 10 {
		t.Fatal(finite)
	}
}

var timestampBadInputs = []struct{ input, code, message, hint string }{
	{"1999 1.5 8", "22007", "invalid input syntax for type timestamp: \"1999 1.5 8\"", ""},
	{"1999 1.1 8", "22007", "invalid input syntax for type timestamp: \"1999 1.1 8\"", ""},
	{"4714-11-23 000000 BC", "22008", "timestamp out of range: \"4714-11-23 000000 BC\"", ""},
	{"J .1 04:05:06", "22007", "invalid input syntax for type timestamp: \"J .1 04:05:06\"", ""},
	{"J . 04:05:06", "22007", "invalid input syntax for type timestamp: \"J . 04:05:06\"", ""},
	{"1999-01-08 250000 PM", "22008", "date/time field value out of range: \"1999-01-08 250000 PM\"", ""},
	{"EDT 1999.008", "22007", "invalid input syntax for type timestamp: \"EDT 1999.008\"", ""},
	{"PST dst 1999.008", "22007", "invalid input syntax for type timestamp: \"PST dst 1999.008\"", ""},
	{"1999-01-08 UTC America/New_York", "22007", "invalid input syntax for type timestamp: \"1999-01-08 UTC America/New_York\"", ""},
	{"1999-01-08 UTC America/Bogus", "22023", "time zone \"america/bogus\" not recognized", ""},
	{"1999 1 0.5", "22008", "date/time field value out of range: \"1999 1 0.5\"", "Perhaps you need a different \"DateStyle\" setting."},
	{"1999 1 32.5", "22008", "date/time field value out of range: \"1999 1 32.5\"", "Perhaps you need a different \"DateStyle\" setting."},
	{"294276-12-31 250000", "22008", "timestamp out of range: \"294276-12-31 250000\"", ""},
	{"1999-01-08 25:00:00", "22008", "date/time field value out of range: \"1999-01-08 25:00:00\"", ""},
	{"1999-01-08 04:99:00", "22008", "date/time field value out of range: \"1999-01-08 04:99:00\"", ""},
	{"1999-01-08 04:05:99", "22008", "date/time field value out of range: \"1999-01-08 04:05:99\"", ""},
	{"1999-01-08 T 2147483648", "22008", "date/time field value out of range: \"1999-01-08 T 2147483648\"", ""},
	{"1999-01-08 04:05:06 T 2147483648", "22008", "date/time field value out of range: \"1999-01-08 04:05:06 T 2147483648\"", ""},
	{"1999-01-08 04:05:06 T 25:00:00", "22008", "date/time field value out of range: \"1999-01-08 04:05:06 T 25:00:00\"", ""},
	{"J -2147483648", "22009", "time zone displacement out of range: \"J -2147483648\"", ""},
	{"J on", "22007", "invalid input syntax for type timestamp: \"J on\"", ""},
	{"J J2451187", "22007", "invalid input syntax for type timestamp: \"J J2451187\"", ""},
	{"J 04:05:06 2451187", "22007", "invalid input syntax for type timestamp: \"J 04:05:06 2451187\"", ""},
	{"1999-01-08 04:05:06 T 040506", "22007", "invalid input syntax for type timestamp: \"1999-01-08 04:05:06 T 040506\"", ""},
	{"1999-01-08 T America/New_York 040506", "22007", "invalid input syntax for type timestamp: \"1999-01-08 T America/New_York 040506\"", ""},
	{"1999-01-08 T America/Bogus", "22007", "invalid input syntax for type timestamp: \"1999-01-08 T America/Bogus\"", ""},
	{"1999-01-08 04:05:06 EDT dst", "22007", "invalid input syntax for type timestamp: \"1999-01-08 04:05:06 EDT dst\"", ""},
	{"1999-01-08 04:05:06 dst EDT", "22007", "invalid input syntax for type timestamp: \"1999-01-08 04:05:06 dst EDT\"", ""},
	{"1999-01-08 04:05:06 MSK dst", "22007", "invalid input syntax for type timestamp: \"1999-01-08 04:05:06 MSK dst\"", ""},
	{"1999-01-08 04:05:06 dst MSK", "22007", "invalid input syntax for type timestamp: \"1999-01-08 04:05:06 dst MSK\"", ""},
	{"1999-01-08 04:05:06 America/New_York dst", "22007", "invalid input syntax for type timestamp: \"1999-01-08 04:05:06 America/New_York dst\"", ""},
	{"1999-01-08 04:05:06 dst America/New_York", "22007", "invalid input syntax for type timestamp: \"1999-01-08 04:05:06 dst America/New_York\"", ""},
	{"1999-01-08 04:05:06 FOOBAR0 dst", "22007", "invalid input syntax for type timestamp: \"1999-01-08 04:05:06 FOOBAR0 dst\"", ""},
	{"1999-01-08 04:05:06 dst FOOBAR0", "22007", "invalid input syntax for type timestamp: \"1999-01-08 04:05:06 dst FOOBAR0\"", ""},
	{"1999-01-08 04:05:06 dst", "22007", "invalid input syntax for type timestamp: \"1999-01-08 04:05:06 dst\"", ""},
	{"1999-01-08 04:05:06 PST dst dst", "22007", "invalid input syntax for type timestamp: \"1999-01-08 04:05:06 PST dst dst\"", ""},
	{"1999-01-08 04:05:06 dst PST UTC", "22007", "invalid input syntax for type timestamp: \"1999-01-08 04:05:06 dst PST UTC\"", ""},
	{"EDT 1999-01-08", "22007", "invalid input syntax for type timestamp: \"EDT 1999-01-08\"", ""},
	{"PST dst 1999-01-08", "22007", "invalid input syntax for type timestamp: \"PST dst 1999-01-08\"", ""},
	{"at at at at at at at at at at at at at at at at at at at at at at at at 1999-01-08!", "22007", "invalid input syntax for type timestamp: \"at at at at at at at at at at at at at at at at at at at at at at at at 1999-01-08!\"", ""},
	{"at at at at at at at at at at at at at at at at at at at at at at at at at 1999-01-08", "22007", "invalid input syntax for type timestamp: \"at at at at at at at at at at at at at at at at at at at at at at at at at 1999-01-08\"", ""},
	{"1999-01-08\u007f", "22007", "invalid input syntax for type timestamp: \"1999-01-08\u007f\"", ""},
	{"\u007f1999-01-08", "22007", "invalid input syntax for type timestamp: \"\u007f1999-01-08\"", ""},
	{"y 2147483648", "22008", "date/time field value out of range: \"y 2147483648\"", ""},
	{"y 1", "22007", "invalid input syntax for type timestamp: \"y 1\"", ""},
	{"y on 2147483648", "22008", "date/time field value out of range: \"y on 2147483648\"", ""},
	{"m 2147483648", "22008", "date/time field value out of range: \"m 2147483648\"", ""},
	{"m 1", "22007", "invalid input syntax for type timestamp: \"m 1\"", ""},
	{"m on 2147483648", "22008", "date/time field value out of range: \"m on 2147483648\"", ""},
	{"d 2147483648", "22008", "date/time field value out of range: \"d 2147483648\"", ""},
	{"d 1", "22007", "invalid input syntax for type timestamp: \"d 1\"", ""},
	{"d on 2147483648", "22008", "date/time field value out of range: \"d on 2147483648\"", ""},
	{"h 2147483648", "22008", "date/time field value out of range: \"h 2147483648\"", ""},
	{"h 1", "22007", "invalid input syntax for type timestamp: \"h 1\"", ""},
	{"h on 2147483648", "22008", "date/time field value out of range: \"h on 2147483648\"", ""},
	{"mm 2147483648", "22008", "date/time field value out of range: \"mm 2147483648\"", ""},
	{"mm 1", "22007", "invalid input syntax for type timestamp: \"mm 1\"", ""},
	{"mm on 2147483648", "22008", "date/time field value out of range: \"mm on 2147483648\"", ""},
	{"s 2147483648", "22008", "date/time field value out of range: \"s 2147483648\"", ""},
	{"s 1", "22007", "invalid input syntax for type timestamp: \"s 1\"", ""},
	{"s on 2147483648", "22008", "date/time field value out of range: \"s on 2147483648\"", ""},
	{"dow 2147483648", "22008", "date/time field value out of range: \"dow 2147483648\"", ""},
	{"dow 1", "22007", "invalid input syntax for type timestamp: \"dow 1\"", ""},
	{"dow on 2147483648", "22008", "date/time field value out of range: \"dow on 2147483648\"", ""},
	{"doy 2147483648", "22008", "date/time field value out of range: \"doy 2147483648\"", ""},
	{"doy 1", "22007", "invalid input syntax for type timestamp: \"doy 1\"", ""},
	{"doy on 2147483648", "22008", "date/time field value out of range: \"doy on 2147483648\"", ""},
	{"isodow 2147483648", "22008", "date/time field value out of range: \"isodow 2147483648\"", ""},
	{"isodow 1", "22007", "invalid input syntax for type timestamp: \"isodow 1\"", ""},
	{"isodow on 2147483648", "22008", "date/time field value out of range: \"isodow on 2147483648\"", ""},
	{"isoyear 2147483648", "22008", "date/time field value out of range: \"isoyear 2147483648\"", ""},
	{"isoyear 1", "22007", "invalid input syntax for type timestamp: \"isoyear 1\"", ""},
	{"isoyear on 2147483648", "22008", "date/time field value out of range: \"isoyear on 2147483648\"", ""},
	{"1999-01-08 04:05:06 + 16", "22009", "time zone displacement out of range: \"1999-01-08 04:05:06 + 16\"", ""},
	{"1999-01-08 04:05:06 +\t16", "22009", "time zone displacement out of range: \"1999-01-08 04:05:06 +\t16\"", ""},
	{"1999-01-08 04:05:06 +\n16", "22009", "time zone displacement out of range: \"1999-01-08 04:05:06 +\n16\"", ""},
	{"1999-01-08 04:05:06 +\r16", "22009", "time zone displacement out of range: \"1999-01-08 04:05:06 +\r16\"", ""},
	{"1999-01-08 04:05:06 +\f16", "22009", "time zone displacement out of range: \"1999-01-08 04:05:06 +\f16\"", ""},
	{"1999-01-08 04:05:06 +\u000b16", "22009", "time zone displacement out of range: \"1999-01-08 04:05:06 +\u000b16\"", ""},
	{"1999-01-08 04:05:06 - 16", "22009", "time zone displacement out of range: \"1999-01-08 04:05:06 - 16\"", ""},
	{"1999-01-08 04:05:06 -\t16", "22009", "time zone displacement out of range: \"1999-01-08 04:05:06 -\t16\"", ""},
	{"1999-01-08 04:05:06 -\n16", "22009", "time zone displacement out of range: \"1999-01-08 04:05:06 -\n16\"", ""},
	{"1999-01-08 04:05:06 -\r16", "22009", "time zone displacement out of range: \"1999-01-08 04:05:06 -\r16\"", ""},
	{"1999-01-08 04:05:06 -\f16", "22009", "time zone displacement out of range: \"1999-01-08 04:05:06 -\f16\"", ""},
	{"1999-01-08 04:05:06 -\u000b16", "22009", "time zone displacement out of range: \"1999-01-08 04:05:06 -\u000b16\"", ""},
	{"January 8 04:05:06 2147483648", "22008", `date/time field value out of range: "January 8 04:05:06 2147483648"`, ""},
	{"1999-01-08T2147483648", "22008", `date/time field value out of range: "1999-01-08T2147483648"`, ""},
	{"epoch 1999-01-08", "22007", `invalid input syntax for type timestamp: "epoch 1999-01-08"`, ""},
	{"1999-01-08 epoch", "22007", `invalid input syntax for type timestamp: "1999-01-08 epoch"`, ""},
	{"epoch 04:05:06", "22007", `invalid input syntax for type timestamp: "epoch 04:05:06"`, ""},
	{"04:05:06 epoch", "22007", `invalid input syntax for type timestamp: "04:05:06 epoch"`, ""},
	{"epoch UTC", "22007", `invalid input syntax for type timestamp: "epoch UTC"`, ""},
	{"UTC epoch", "22007", `invalid input syntax for type timestamp: "UTC epoch"`, ""},
	{"epoch +02", "22007", `invalid input syntax for type timestamp: "epoch +02"`, ""},
	{"+02 epoch", "22007", `invalid input syntax for type timestamp: "+02 epoch"`, ""},
	{"infinity 1999-01-08", "22007", `invalid input syntax for type timestamp: "infinity 1999-01-08"`, ""},
	{"1999-01-08 infinity", "22007", `invalid input syntax for type timestamp: "1999-01-08 infinity"`, ""},
	{"infinity 04:05:06", "22007", `invalid input syntax for type timestamp: "infinity 04:05:06"`, ""},
	{"04:05:06 infinity", "22007", `invalid input syntax for type timestamp: "04:05:06 infinity"`, ""},
	{"infinity UTC", "22007", `invalid input syntax for type timestamp: "infinity UTC"`, ""},
	{"UTC infinity", "22007", `invalid input syntax for type timestamp: "UTC infinity"`, ""},
	{"infinity +02", "22007", `invalid input syntax for type timestamp: "infinity +02"`, ""},
	{"+02 infinity", "22007", `invalid input syntax for type timestamp: "+02 infinity"`, ""},
	{"+infinity 1999-01-08", "22007", `invalid input syntax for type timestamp: "+infinity 1999-01-08"`, ""},
	{"1999-01-08 +infinity", "22007", `invalid input syntax for type timestamp: "1999-01-08 +infinity"`, ""},
	{"+infinity 04:05:06", "22007", `invalid input syntax for type timestamp: "+infinity 04:05:06"`, ""},
	{"04:05:06 +infinity", "22007", `invalid input syntax for type timestamp: "04:05:06 +infinity"`, ""},
	{"+infinity UTC", "22007", `invalid input syntax for type timestamp: "+infinity UTC"`, ""},
	{"UTC +infinity", "22007", `invalid input syntax for type timestamp: "UTC +infinity"`, ""},
	{"+infinity +02", "22007", `invalid input syntax for type timestamp: "+infinity +02"`, ""},
	{"+02 +infinity", "22007", `invalid input syntax for type timestamp: "+02 +infinity"`, ""},
	{"- InFiNiTy 1999-01-08", "22007", `invalid input syntax for type timestamp: "- InFiNiTy 1999-01-08"`, ""},
	{"1999-01-08 - InFiNiTy", "22007", `invalid input syntax for type timestamp: "1999-01-08 - InFiNiTy"`, ""},
	{"- InFiNiTy 04:05:06", "22007", `invalid input syntax for type timestamp: "- InFiNiTy 04:05:06"`, ""},
	{"04:05:06 - InFiNiTy", "22007", `invalid input syntax for type timestamp: "04:05:06 - InFiNiTy"`, ""},
	{"- InFiNiTy UTC", "22007", `invalid input syntax for type timestamp: "- InFiNiTy UTC"`, ""},
	{"UTC - InFiNiTy", "22007", `invalid input syntax for type timestamp: "UTC - InFiNiTy"`, ""},
	{"- InFiNiTy +02", "22007", `invalid input syntax for type timestamp: "- InFiNiTy +02"`, ""},
	{"+02 - InFiNiTy", "22007", `invalid input syntax for type timestamp: "+02 - InFiNiTy"`, ""},
	{"epoch epoch", "22007", `invalid input syntax for type timestamp: "epoch epoch"`, ""},
	{"infinity -infinity", "22007", `invalid input syntax for type timestamp: "infinity -infinity"`, ""},
	{"on epoch at nonsense", "22007", `invalid input syntax for type timestamp: "on epoch at nonsense"`, ""},
	{"2147483648-on-08", "22008", `date/time field value out of range: "2147483648-on-08"`, ""},
	{"1999-at-08", "22007", `invalid input syntax for type timestamp: "1999-at-08"`, ""},
	{"1999-on-08", "22007", `invalid input syntax for type timestamp: "1999-on-08"`, ""},
	{"1999.01.08..", "22007", `invalid input syntax for type timestamp: "1999.01.08.."`, ""},
	{"1999-01-08-1", "22007", `invalid input syntax for type timestamp: "1999-01-08-1"`, ""},
	{"1999 0 January", "22007", `invalid input syntax for type timestamp: "1999 0 January"`, ""},
	{"1999 32 January", "22007", `invalid input syntax for type timestamp: "1999 32 January"`, ""},
	{"1999 31 February", "22008", `date/time field value out of range: "1999 31 February"`, ""},
	{"2147483648-at-08", "22008", `date/time field value out of range: "2147483648-at-08"`, ""},
	{"1999-01-08-2147483648", "22008", `date/time field value out of range: "1999-01-08-2147483648"`, ""},
	{"2147483648-xxx-08", "22007", `invalid input syntax for type timestamp: "2147483648-xxx-08"`, ""},
	{"1999-01-08--", "22007", `invalid input syntax for type timestamp: "1999-01-08--"`, ""},
	{"1999/01/08//", "22007", `invalid input syntax for type timestamp: "1999/01/08//"`, ""},
	{"99 January 1999", "22007", `invalid input syntax for type timestamp: "99 January 1999"`, ""},
	{"0 January 1999", "22007", `invalid input syntax for type timestamp: "0 January 1999"`, ""},
	{"32 January 1999", "22007", `invalid input syntax for type timestamp: "32 January 1999"`, ""},
	{"1999-01-08 04:05:06 now1", "22007", `invalid input syntax for type timestamp: "1999-01-08 04:05:06 now1"`, ""},
	{"1999-01-08 04:05:06 now+2", "22007", `invalid input syntax for type timestamp: "1999-01-08 04:05:06 now+2"`, ""},
	{"1999-01-08 04:05:06 today1", "22007", `invalid input syntax for type timestamp: "1999-01-08 04:05:06 today1"`, ""},
	{"1999-01-08 04:05:06 today+2", "22007", `invalid input syntax for type timestamp: "1999-01-08 04:05:06 today+2"`, ""},
	{"1999-01-08 04:05:06 tomorrow1", "22007", `invalid input syntax for type timestamp: "1999-01-08 04:05:06 tomorrow1"`, ""},
	{"1999-01-08 04:05:06 tomorrow+2", "22007", `invalid input syntax for type timestamp: "1999-01-08 04:05:06 tomorrow+2"`, ""},
	{"1999-01-08 04:05:06 yesterday1", "22007", `invalid input syntax for type timestamp: "1999-01-08 04:05:06 yesterday1"`, ""},
	{"1999-01-08 04:05:06 yesterday+2", "22007", `invalid input syntax for type timestamp: "1999-01-08 04:05:06 yesterday+2"`, ""},
	{"1999-01-08 04:05:06 epoch1", "22007", `invalid input syntax for type timestamp: "1999-01-08 04:05:06 epoch1"`, ""},
	{"1999-01-08 04:05:06 epoch+2", "22007", `invalid input syntax for type timestamp: "1999-01-08 04:05:06 epoch+2"`, ""},
	{"1999-01-08 04:05:06 infinity1", "22007", `invalid input syntax for type timestamp: "1999-01-08 04:05:06 infinity1"`, ""},
	{"1999-01-08 04:05:06 infinity+2", "22007", `invalid input syntax for type timestamp: "1999-01-08 04:05:06 infinity+2"`, ""},
	{"1999-01-08 04:05:06 allballs1", "22007", `invalid input syntax for type timestamp: "1999-01-08 04:05:06 allballs1"`, ""},
	{"1999-01-08 04:05:06 allballs+2", "22007", `invalid input syntax for type timestamp: "1999-01-08 04:05:06 allballs+2"`, ""},
	{"2004-10-19 10:23:54+16:00", "22009", `time zone displacement out of range: "2004-10-19 10:23:54+16:00"`, ""},
	{"2004-10-19 10:23:54-16:00", "22009", `time zone displacement out of range: "2004-10-19 10:23:54-16:00"`, ""},
	{"January 1999-08", "22008", `date/time field value out of range: "January 1999-08"`, `Perhaps you need a different "DateStyle" setting.`},
	{"Jan 1999/08", "22008", `date/time field value out of range: "Jan 1999/08"`, `Perhaps you need a different "DateStyle" setting.`},
	{"2147483648-01-01 00:00:00+16:00", "22008", `date/time field value out of range: "2147483648-01-01 00:00:00+16:00"`, ""},
	{"1999 13", "22008", `date/time field value out of range: "1999 13"`, `Perhaps you need a different "DateStyle" setting.`},
	{"1999.13", "22007", `invalid input syntax for type timestamp: "1999.13"`, ""},
	{"Jan 32", "22008", `date/time field value out of range: "Jan 32"`, `Perhaps you need a different "DateStyle" setting.`},
	{"Jan 31", "22007", `invalid input syntax for type timestamp: "Jan 31"`, ""},
	{"1999 000", "22008", `date/time field value out of range: "1999 000"`, `Perhaps you need a different "DateStyle" setting.`},
	{"1999 367 04:05:06", "22008", `date/time field value out of range: "1999 367 04:05:06"`, `Perhaps you need a different "DateStyle" setting.`},
	{"13", "22008", `date/time field value out of range: "13"`, `Perhaps you need a different "DateStyle" setting.`},
	{"January 32", "22008", `date/time field value out of range: "January 32"`, `Perhaps you need a different "DateStyle" setting.`},
	{"000", "22008", `date/time field value out of range: "000"`, ""},
	{"1999 12", "22007", `invalid input syntax for type timestamp: "1999 12"`, ""},
	{"04:05:06", "22007", `invalid input syntax for type timestamp: "04:05:06"`, ""},
	{"AD 0001-01-01", "22007", `invalid input syntax for type timestamp: "AD 0001-01-01"`, ""},
	{"AM 1999-01-08 04:05:06", "22007", `invalid input syntax for type timestamp: "AM 1999-01-08 04:05:06"`, ""},
	{"weds 1999-01-08 04:05:06", "22007", `invalid input syntax for type timestamp: "weds 1999-01-08 04:05:06"`, ""},
	{"Friday 1999-01-08 04:05:06", "22007", `invalid input syntax for type timestamp: "Friday 1999-01-08 04:05:06"`, ""},
	{"BC 0001-01-01", "22007", `invalid input syntax for type timestamp: "BC 0001-01-01"`, ""},
	{"PM 1999-01-08 04:05:06", "22007", `invalid input syntax for type timestamp: "PM 1999-01-08 04:05:06"`, ""},
	{"Friday 1999.008 04:05:06", "22007", `invalid input syntax for type timestamp: "Friday 1999.008 04:05:06"`, ""},
	{"BC 0001.001", "22007", `invalid input syntax for type timestamp: "BC 0001.001"`, ""},
	{"PM 1999.008 04:05:06", "22007", `invalid input syntax for type timestamp: "PM 1999.008 04:05:06"`, ""},
	{"1999 01-08-1999", "22007", `invalid input syntax for type timestamp: "1999 01-08-1999"`, ""},
	{"1999.000", "22007", `invalid input syntax for type timestamp: "1999.000"`, ""},
	{"1999.367", "22007", `invalid input syntax for type timestamp: "1999.367"`, ""},
	{"1999 367", "22008", `date/time field value out of range: "1999 367"`, `Perhaps you need a different "DateStyle" setting.`},
	{"294277.001", "22008", `timestamp out of range: "294277.001"`, ""},
	{"allballs 1999-01-08", "22007", `invalid input syntax for type timestamp: "allballs 1999-01-08"`, ""},
	{"04:05:06 1999-01-08", "22007", `invalid input syntax for type timestamp: "04:05:06 1999-01-08"`, ""},
	{"allballs 1999.008", "22007", `invalid input syntax for type timestamp: "allballs 1999.008"`, ""},
	{"04:05:06 1999.008", "22007", `invalid input syntax for type timestamp: "04:05:06 1999.008"`, ""},
	{"1999-01-08 04:05:06+00:00:60", "22009", `time zone displacement out of range: "1999-01-08 04:05:06+00:00:60"`, ""},
	{"1999-01-08 04:05:06 A0:00:61", "22023", `time zone "a0:00:61" not recognized`, ""},
	{"J2451187.0 04:05:06", "22007", `invalid input syntax for type timestamp: "J2451187.0 04:05:06"`, ""},
	{"04:05:06 J2451187.0", "22007", `invalid input syntax for type timestamp: "04:05:06 J2451187.0"`, ""},
	{"J2451187.1 UTC UTC", "22007", `invalid input syntax for type timestamp: "J2451187.1 UTC UTC"`, ""},
	{"J2451187.", "22007", `invalid input syntax for type timestamp: "J2451187."`, ""},
	{"1999-01-08 040506.", "22007", `invalid input syntax for type timestamp: "1999-01-08 040506."`, ""},
	{"1999-01-08 04:05:06..1", "22007", `invalid input syntax for type timestamp: "1999-01-08 04:05:06..1"`, ""},
	{"1999-01-08 04:05:06.nonsense", "22007", `invalid input syntax for type timestamp: "1999-01-08 04:05:06.nonsense"`, ""},
	{"2147483648-01-01", "22008", `date/time field value out of range: "2147483648-01-01"`, ""},
	{"1999-2147483648-08", "22008", `date/time field value out of range: "1999-2147483648-08"`, ""},
	{"1999-01-2147483648", "22008", `date/time field value out of range: "1999-01-2147483648"`, ""},
	{"J2147483648", "22008", `date/time field value out of range: "J2147483648"`, ""},
	{"1999-01-08 2147483648:00:00", "22008", `date/time field value out of range: "1999-01-08 2147483648:00:00"`, ""},
	{"1999-01-08 04:2147483648:06", "22008", `date/time field value out of range: "1999-01-08 04:2147483648:06"`, ""},
	{"1999-01-08 04:05:2147483648", "22008", `date/time field value out of range: "1999-01-08 04:05:2147483648"`, ""},
	{"1999-01-08 9223372036854775808:00:00", "22008", `date/time field value out of range: "1999-01-08 9223372036854775808:00:00"`, ""},
	{"1999-01-08 2147483648:00:00..1", "22007", `invalid input syntax for type timestamp: "1999-01-08 2147483648:00:00..1"`, ""},
	{"1999-01-08 04:05:2147483648..1", "22008", `date/time field value out of range: "1999-01-08 04:05:2147483648..1"`, ""},
	{"1999-01-08 04:05:06+2147483648", "22009", `time zone displacement out of range: "1999-01-08 04:05:06+2147483648"`, ""},
	{"1999-01-08 04:05:06+02:2147483648", "22009", `time zone displacement out of range: "1999-01-08 04:05:06+02:2147483648"`, ""},
	{"1999-01-08 04:05:06+02:30:2147483648", "22009", `time zone displacement out of range: "1999-01-08 04:05:06+02:30:2147483648"`, ""},
	{"1999-01-08 04:05:06+16.0", "22009", `time zone displacement out of range: "1999-01-08 04:05:06+16.0"`, ""},
	{"1999-01-08 04:05:06+02.0", "22007", `invalid input syntax for type timestamp: "1999-01-08 04:05:06+02.0"`, ""},
	{"now UTC", "22007", `invalid input syntax for type timestamp: "now UTC"`, ""},
	{"UTC now", "22007", `invalid input syntax for type timestamp: "UTC now"`, ""},
	{"now +02", "22007", `invalid input syntax for type timestamp: "now +02"`, ""},
	{"+02 now", "22007", `invalid input syntax for type timestamp: "+02 now"`, ""},
	{"now GMT", "22007", `invalid input syntax for type timestamp: "now GMT"`, ""},
	{"now America/New_York", "22007", `invalid input syntax for type timestamp: "now America/New_York"`, ""},
	{"America/New_York now", "22007", `invalid input syntax for type timestamp: "America/New_York now"`, ""},
	{"1999-01-08 allballs UTC", "22007", `invalid input syntax for type timestamp: "1999-01-08 allballs UTC"`, ""},
	{"1999-01-08 UTC allballs", "22007", `invalid input syntax for type timestamp: "1999-01-08 UTC allballs"`, ""},
	{"1999-01-08 allballs +02", "22007", `invalid input syntax for type timestamp: "1999-01-08 allballs +02"`, ""},
	{"1999-01-08 +02 allballs", "22007", `invalid input syntax for type timestamp: "1999-01-08 +02 allballs"`, ""},
	{"allballs UTC 1999-01-08", "22007", `invalid input syntax for type timestamp: "allballs UTC 1999-01-08"`, ""},
	{"UTC allballs 1999-01-08", "22007", `invalid input syntax for type timestamp: "UTC allballs 1999-01-08"`, ""},
	{"January 8 040506-8 1999", "22007", `invalid input syntax for type timestamp: "January 8 040506-8 1999"`, ""},
	{"y2001m2d4", "22007", `invalid input syntax for type timestamp: "y2001m2d4"`, ""},
	{"2001-02-04 m2", "22007", `invalid input syntax for type timestamp: "2001-02-04 m2"`, ""},
	{"2001-02-04 d4", "22007", `invalid input syntax for type timestamp: "2001-02-04 d4"`, ""},
	{"2001-02-04 h4", "22007", `invalid input syntax for type timestamp: "2001-02-04 h4"`, ""},
	{"2001-02-04 mm5", "22007", `invalid input syntax for type timestamp: "2001-02-04 mm5"`, ""},
	{"2001-02-04 s6", "22007", `invalid input syntax for type timestamp: "2001-02-04 s6"`, ""},
	{"nonsense", "22007", "invalid input syntax for type timestamp: \"nonsense\"", ""},
	{"2023-02-29 12:00:00", "22008", "date/time field value out of range: \"2023-02-29 12:00:00\"", ""},
	{"1900-02-29", "22008", "date/time field value out of range: \"1900-02-29\"", ""},
	{"2024-13-01 12:00:00", "22008", "date/time field value out of range: \"2024-13-01 12:00:00\"", "Perhaps you need a different \"DateStyle\" setting."},
	{"2024-01-01 25:00:00", "22008", "date/time field value out of range: \"2024-01-01 25:00:00\"", ""},
	{"2024-01-01 12:60:00", "22008", "date/time field value out of range: \"2024-01-01 12:60:00\"", ""},
	{"2024-01-01 24:00:00.000001", "22008", "date/time field value out of range: \"2024-01-01 24:00:00.000001\"", ""},
	{"0000-01-01", "22008", "date/time field value out of range: \"0000-01-01\"", ""},
	{"294277-01-01 00:00:00", "22008", "timestamp out of range: \"294277-01-01 00:00:00\"", ""},
	{"4714-11-23 BC", "22008", "timestamp out of range: \"4714-11-23 BC\"", ""},
	{"99-Jan-08", "22008", "date/time field value out of range: \"99-Jan-08\"", "Perhaps you need a different \"DateStyle\" setting."},
	{"allballs", "22007", "invalid input syntax for type timestamp: \"allballs\"", ""},
	{"1999-01-08 13:00 PM", "22008", "date/time field value out of range: \"1999-01-08 13:00 PM\"", ""},
	{"1999-01-08 04:05:06+16", "22009", "time zone displacement out of range: \"1999-01-08 04:05:06+16\"", ""},
	{"1999-01-08 04:05:06+02:60", "22009", "time zone displacement out of range: \"1999-01-08 04:05:06+02:60\"", ""},
	{"1999-01-08 04:05:06 nonsense", "22007", "invalid input syntax for type timestamp: \"1999-01-08 04:05:06 nonsense\"", ""},
	{"1999-01-08 04:05:06 America/No_Such_Zone", "22023", "time zone \"america/no_such_zone\" not recognized", ""},
	{"1999-01-08 04:05:06 PST UTC", "22007", "invalid input syntax for type timestamp: \"1999-01-08 04:05:06 PST UTC\"", ""},
	{"1999-01-08 04:05:06 PST8PDT,M3.2.0,M11.1.0", "22007", "invalid input syntax for type timestamp: \"1999-01-08 04:05:06 PST8PDT,M3.2.0,M11.1.0\"", ""},
	{"1999-01-08 04:05:06 <FOO>0", "22007", "invalid input syntax for type timestamp: \"1999-01-08 04:05:06 <FOO>0\"", ""},
	{"1999-01-08 04:05:06 FOOBAR999", "22023", "time zone \"foobar999\" not recognized", ""},
}

func TestTimestampDiagnosticsPhasesAndAtomicity(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	execSQL(t, db, "CREATE TABLE t(v TIMESTAMP NOT NULL)")
	for _, tc := range timestampBadInputs {
		q := "/*é*/ SELECT TIMESTAMP '" + tc.input + "' FROM t"
		stmt, err := db.Prepare(q)
		if stmt != nil {
			stmt.Close()
			t.Fatal("bad input prepared against empty timestamp table")
		}
		position := strconv.Itoa(utf8.RuneCountInString(q[:strings.Index(q, "'")]) + 1)
		typeCoreDiagnostic(t, err, stuntgres.Error{Code: tc.code, Message: tc.message, Hint: tc.hint, Position: position})
	}
	execSQL(t, db, "INSERT INTO t VALUES('epoch')")
	for _, tc := range timestampBadInputs {
		t.Run(tc.input, func(t *testing.T) {
			for _, q := range []string{"INSERT INTO t VALUES ('" + tc.input + "')", "/*é*/ SELECT TIMESTAMP '" + tc.input + "' FROM t", "INSERT INTO t VALUES('2000-01-01'),('" + tc.input + "')"} {
				position := strconv.Itoa(utf8.RuneCountInString(q[:strings.LastIndex(q, "'"+tc.input)]) + 1)
				want := stuntgres.Error{Code: tc.code, Message: tc.message, Hint: tc.hint, Position: position}
				_, e := db.Exec(q)
				typeCoreDiagnostic(t, e, want)
				stmt, e := db.Prepare(q)
				if stmt != nil {
					stmt.Close()
					t.Fatal("bad literal prepared")
				}
				typeCoreDiagnostic(t, e, want)
			}
			_, e := db.Exec("INSERT INTO t VALUES($1)", tc.input)
			typeCoreDiagnostic(t, e, stuntgres.Error{Code: tc.code, Message: tc.message, Hint: tc.hint})
			stmt, e := db.Prepare("INSERT INTO t VALUES($1)")
			if e != nil {
				t.Fatal(e)
			}
			_, e = stmt.Exec(tc.input)
			stmt.Close()
			typeCoreDiagnostic(t, e, stuntgres.Error{Code: tc.code, Message: tc.message, Hint: tc.hint})
			atomic := "INSERT INTO t VALUES('2000-01-01'),($1)"
			_, e = db.Exec(atomic, tc.input)
			typeCoreDiagnostic(t, e, stuntgres.Error{Code: tc.code, Message: tc.message, Hint: tc.hint})
			stmt, e = db.Prepare(atomic)
			if e != nil {
				t.Fatal(e)
			}
			_, e = stmt.Exec(tc.input)
			stmt.Close()
			typeCoreDiagnostic(t, e, stuntgres.Error{Code: tc.code, Message: tc.message, Hint: tc.hint})
			timestampQuery(t, db, "SELECT v FROM t", []string{"v"}, []string{"TIMESTAMP"}, [][]any{{time.Date(1970, 1, 1, 0, 0, 0, 0, time.UTC)}})
		})
	}
	_, e := db.Prepare("SELECT $1 FROM t WHERE v=$1")
	typeCoreDiagnostic(t, e, stuntgres.Error{Code: "42P08", Message: "inconsistent types deduced for parameter $1", Detail: "timestamp without time zone versus text", Position: "8"})
	negativeDeclaration := "CREATE TABLE bad (v TIMESTAMP(-1))"
	_, e = db.Exec(negativeDeclaration)
	typeCoreDiagnostic(t, e, stuntgres.Error{Code: "42601", Message: `syntax error at or near "-"`, Position: strconv.Itoa(utf8.RuneCountInString(negativeDeclaration[:strings.Index(negativeDeclaration, "-")]) + 1)})
	stmt, e := db.Prepare(`CREATE TABLE bad (v "timestamp"(-1))`)
	if e != nil {
		t.Fatal("utility prepare", e)
	}
	_, e = stmt.Exec()
	stmt.Close()
	typeCoreDiagnostic(t, e, stuntgres.Error{Code: "22023", Message: "TIMESTAMP(-1) precision must not be negative", Position: "21"})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, e = db.ExecContext(ctx, "INSERT INTO t VALUES('now')")
	if !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
}

func TestTimestampTimeParameterRangeDiagnostics(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	execSQL(t, db, "CREATE TABLE t(v TIMESTAMP NOT NULL)")
	execSQL(t, db, "INSERT INTO t VALUES('epoch')")
	for _, tc := range []struct {
		input         time.Time
		code, message string
	}{
		{time.Date(294277, 1, 1, 0, 0, 0, 0, time.UTC), "22008", `timestamp out of range: "294277-01-01 00:00:00Z"`},
		{time.Date(294277, 1, 1, 4, 5, 6, 123456789, time.FixedZone("east", 7384)), "22008", `timestamp out of range: "294277-01-01 04:05:06.123456789+02:03:04"`},
		{time.Date(-4713, 11, 23, 4, 5, 6, 123456789, time.FixedZone("west", -7384)), "22008", `timestamp out of range: "4714-11-23 04:05:06.123456789-02:03:04 BC"`},
		{time.Date(294277, 1, 1, 4, 5, 6, 123456789, time.FixedZone("east", 9045)), "22008", `timestamp out of range: "294277-01-01 04:05:06.123456789+02:30:45"`},
		{time.Date(-5000, 1, 1, 0, 0, 0, 0, time.UTC), "22008", `timestamp out of range: "5001-01-01 00:00:00Z BC"`},
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
		for _, q := range []string{"INSERT INTO t VALUES($1)", "INSERT INTO t VALUES('2000-01-01'),($1)"} {
			want := stuntgres.Error{Code: tc.code, Message: tc.message}
			_, err := db.Exec(q, tc.input)
			typeCoreDiagnostic(t, err, want)
			stmt, err := db.Prepare(q)
			if err != nil {
				t.Fatal("parameter Prepare must succeed", err)
			}
			_, err = stmt.Exec(tc.input)
			stmt.Close()
			typeCoreDiagnostic(t, err, want)
			timestampQuery(t, db, "SELECT v FROM t", []string{"v"}, []string{"TIMESTAMP"}, [][]any{{time.Date(1970, 1, 1, 0, 0, 0, 0, time.UTC)}})
		}
	}
}

func TestTimestampOrdinalBoundaryOrdering(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	execSQL(t, db, "CREATE TABLE t(id INTEGER,v TIMESTAMP)")
	execSQL(t, db, "INSERT INTO t VALUES(1,'2000-01-01'),(2,TIMESTAMP '1999.366'),(3,'1999.365'),(4,'2000 366')")
	timestampQuery(t, db, "SELECT v FROM t ORDER BY v", []string{"v"}, []string{"TIMESTAMP"}, [][]any{
		{time.Date(1999, 12, 31, 0, 0, 0, 0, time.UTC)},
		{time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)},
		{time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)},
		{time.Date(2000, 12, 31, 0, 0, 0, 0, time.UTC)},
	})
	stmt, err := db.Prepare("SELECT id FROM t WHERE v=$1 ORDER BY id")
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	rows, err := stmt.Query("1999.366")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil || len(ids) != 2 || ids[0] != 1 || ids[1] != 2 {
		t.Fatal(ids, err)
	}
}

func TestTimestampNewInputErrorsAbortTransaction(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	execSQL(t, db, "CREATE TABLE t(v TIMESTAMP)")
	execSQL(t, db, "INSERT INTO t VALUES('epoch')")
	for _, tc := range []struct {
		input               any
		code, message, hint string
	}{
		{"2147483648-on-08", "22008", `date/time field value out of range: "2147483648-on-08"`, ""},
		{"1999-at-08", "22007", `invalid input syntax for type timestamp: "1999-at-08"`, ""},
		{"1999-on-08", "22007", `invalid input syntax for type timestamp: "1999-on-08"`, ""},
		{"1999-01-08--", "22007", `invalid input syntax for type timestamp: "1999-01-08--"`, ""},
		{"1999/01/08//", "22007", `invalid input syntax for type timestamp: "1999/01/08//"`, ""},
		{"99 January 1999", "22007", `invalid input syntax for type timestamp: "99 January 1999"`, ""},
		{"1999-01-08 04:05:06 now1", "22007", `invalid input syntax for type timestamp: "1999-01-08 04:05:06 now1"`, ""},
		{"1999-01-08 04:05:06 now+2", "22007", `invalid input syntax for type timestamp: "1999-01-08 04:05:06 now+2"`, ""},
		{"1999.01.08..", "22007", `invalid input syntax for type timestamp: "1999.01.08.."`, ""},
		{"1999-01-08-1", "22007", `invalid input syntax for type timestamp: "1999-01-08-1"`, ""},
		{"1999-01-08-2147483648", "22008", `date/time field value out of range: "1999-01-08-2147483648"`, ""},
		{"2147483648-xxx-08", "22007", `invalid input syntax for type timestamp: "2147483648-xxx-08"`, ""},
		{"2147483648-at-08", "22008", `date/time field value out of range: "2147483648-at-08"`, ""},
		{"1999 0 January", "22007", `invalid input syntax for type timestamp: "1999 0 January"`, ""},
		{"1999 32 January", "22007", `invalid input syntax for type timestamp: "1999 32 January"`, ""},
		{"0 January 1999", "22007", `invalid input syntax for type timestamp: "0 January 1999"`, ""},
		{"32 January 1999", "22007", `invalid input syntax for type timestamp: "32 January 1999"`, ""},
		{"1999 31 February", "22008", `date/time field value out of range: "1999 31 February"`, ""},
		{"1999-01-08 04:05:06 today1", "22007", `invalid input syntax for type timestamp: "1999-01-08 04:05:06 today1"`, ""},
		{"1999-01-08 04:05:06 today+2", "22007", `invalid input syntax for type timestamp: "1999-01-08 04:05:06 today+2"`, ""},
		{"1999-01-08 04:05:06 tomorrow1", "22007", `invalid input syntax for type timestamp: "1999-01-08 04:05:06 tomorrow1"`, ""},
		{"1999-01-08 04:05:06 tomorrow+2", "22007", `invalid input syntax for type timestamp: "1999-01-08 04:05:06 tomorrow+2"`, ""},
		{"1999-01-08 04:05:06 yesterday1", "22007", `invalid input syntax for type timestamp: "1999-01-08 04:05:06 yesterday1"`, ""},
		{"1999-01-08 04:05:06 yesterday+2", "22007", `invalid input syntax for type timestamp: "1999-01-08 04:05:06 yesterday+2"`, ""},
		{"1999-01-08 04:05:06 epoch1", "22007", `invalid input syntax for type timestamp: "1999-01-08 04:05:06 epoch1"`, ""},
		{"1999-01-08 04:05:06 epoch+2", "22007", `invalid input syntax for type timestamp: "1999-01-08 04:05:06 epoch+2"`, ""},
		{"1999-01-08 04:05:06 infinity1", "22007", `invalid input syntax for type timestamp: "1999-01-08 04:05:06 infinity1"`, ""},
		{"1999-01-08 04:05:06 infinity+2", "22007", `invalid input syntax for type timestamp: "1999-01-08 04:05:06 infinity+2"`, ""},
		{"1999-01-08 04:05:06 allballs1", "22007", `invalid input syntax for type timestamp: "1999-01-08 04:05:06 allballs1"`, ""},
		{"1999-01-08 04:05:06 allballs+2", "22007", `invalid input syntax for type timestamp: "1999-01-08 04:05:06 allballs+2"`, ""},
		{"Friday 1999-01-08 04:05:06", "22007", `invalid input syntax for type timestamp: "Friday 1999-01-08 04:05:06"`, ""},
		{"BC 0001-01-01", "22007", `invalid input syntax for type timestamp: "BC 0001-01-01"`, ""},
		{"PM 1999-01-08 04:05:06", "22007", `invalid input syntax for type timestamp: "PM 1999-01-08 04:05:06"`, ""},
		{time.Date(294277, 1, 1, 0, 0, 0, 0, time.UTC), "22008", `timestamp out of range: "294277-01-01 00:00:00Z"`, ""},
		{"January 1999-08", "22008", `date/time field value out of range: "January 1999-08"`, `Perhaps you need a different "DateStyle" setting.`},
		{"Jan 1999/08", "22008", `date/time field value out of range: "Jan 1999/08"`, `Perhaps you need a different "DateStyle" setting.`},
		{time.Date(2004, 10, 19, 10, 23, 54, 0, time.FixedZone("east", 57600)), "22009", `time zone displacement out of range: "2004-10-19 10:23:54+16:00"`, ""},
		{time.Date(2004, 10, 19, 10, 23, 54, 0, time.FixedZone("west", -57600)), "22009", `time zone displacement out of range: "2004-10-19 10:23:54-16:00"`, ""},
	} {
		tx, err := db.Begin()
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		_, err = tx.Exec("INSERT INTO t VALUES('2000-01-01'),($1)", tc.input)
		typeCoreDiagnostic(t, err, stuntgres.Error{Code: tc.code, Message: tc.message, Hint: tc.hint})
		_, err = tx.Exec("SELECT v FROM t")
		typeCoreDiagnostic(t, err, stuntgres.Error{Code: "25P02", Message: "current transaction is aborted, commands ignored until end of transaction block"})
		_, err = tx.Exec("INSERT INTO t VALUES('epoch')")
		typeCoreDiagnostic(t, err, stuntgres.Error{Code: "25P02", Message: "current transaction is aborted, commands ignored until end of transaction block"})
		if err := tx.Rollback(); err != nil {
			t.Fatal(err)
		}
		timestampQuery(t, db, "SELECT v FROM t", []string{"v"}, []string{"TIMESTAMP"}, [][]any{{time.Date(1970, 1, 1, 0, 0, 0, 0, time.UTC)}})
	}
}

func TestTimestampTimeDisplacementBounds(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	execSQL(t, db, "CREATE TABLE t(v TIMESTAMP)")
	stmt, err := db.Prepare("INSERT INTO t VALUES($1)")
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	for _, offset := range []int{57599, -57599} {
		input := time.Date(2004, 10, 19, 10, 23, 54, 123456600, time.FixedZone("civil", offset))
		result, err := db.Exec("INSERT INTO t VALUES($1)", input)
		if err != nil {
			t.Fatal(err)
		}
		if n, err := result.RowsAffected(); err != nil || n != 1 {
			t.Fatal(n, err)
		}
		result, err = stmt.Exec(input)
		if err != nil {
			t.Fatal(err)
		}
		if n, err := result.RowsAffected(); err != nil || n != 1 {
			t.Fatal(n, err)
		}
	}
	want := time.Date(2004, 10, 19, 10, 23, 54, 123457000, time.UTC)
	timestampQuery(t, db, "SELECT v FROM t", []string{"v"}, []string{"TIMESTAMP"}, [][]any{{want}, {want}, {want}, {want}})
}

func TestTimestampCoreSpecialZoneAdjacency(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	execSQL(t, db, "CREATE TABLE anchor(v INTEGER)")
	execSQL(t, db, "INSERT INTO anchor VALUES(1)")
	before := time.Now().UTC()
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	after := time.Now().UTC()
	var now, today time.Time
	if err := tx.QueryRow("SELECT TIMESTAMP 'now',TIMESTAMP 'today+2' FROM anchor").Scan(&now, &today); err != nil {
		t.Fatal(err)
	}
	if now.Before(before.Add(-time.Microsecond)) || now.After(after.Add(time.Microsecond)) {
		t.Fatal("transaction start interval", now, before, after)
	}
	want := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	if today != want {
		t.Fatal(today, want)
	}
	stmt, err := tx.Prepare("SELECT TIMESTAMP 'today+2' FROM anchor")
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	if err := stmt.QueryRow().Scan(&today); err != nil || today != want {
		t.Fatal(today, want, err)
	}
}
