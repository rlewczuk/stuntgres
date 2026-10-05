package stuntgres_test

import (
	"bytes"
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"reflect"
	"strconv"
	"strings"
	"stuntgres"
	"testing"
	"time"
)

func TestTimestampClockAndPreparedConstants(t *testing.T) {
	ctx := context.Background()
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	execSQL(t, db, "CREATE TABLE anchor(n INTEGER)")
	execSQL(t, db, "INSERT INTO anchor VALUES(1)")
	execSQL(t, db, "CREATE TABLE t(v TIMESTAMP)")
	conn, e := db.Conn(ctx)
	if e != nil {
		t.Fatal(e)
	}
	defer conn.Close()
	before := time.Now().UTC()
	literal, e := conn.PrepareContext(ctx, "SELECT TIMESTAMP 'now' FROM anchor")
	if e != nil {
		t.Fatal(e)
	}
	defer literal.Close()
	after := time.Now().UTC()
	contextual, e := conn.PrepareContext(ctx, "INSERT INTO t VALUES('now')")
	if e != nil {
		t.Fatal(e)
	}
	defer contextual.Close()
	param, e := conn.PrepareContext(ctx, "INSERT INTO t VALUES($1)")
	if e != nil {
		t.Fatal(e)
	}
	defer param.Close()
	var frozen time.Time
	if e = literal.QueryRowContext(ctx).Scan(&frozen); e != nil {
		t.Fatal(e)
	}
	if frozen.Before(before.Add(-time.Microsecond)) || frozen.After(after.Add(time.Microsecond)) {
		t.Fatal("constant not analysis clock", frozen, before, after)
	}
	if _, e = contextual.ExecContext(ctx); e != nil {
		t.Fatal(e)
	}
	var first time.Time
	if e = conn.QueryRowContext(ctx, "SELECT v FROM t").Scan(&first); e != nil {
		t.Fatal(e)
	}
	time.Sleep(3 * time.Millisecond)
	for range 2 {
		var again time.Time
		if e = literal.QueryRowContext(ctx).Scan(&again); e != nil || !again.Equal(frozen) {
			t.Fatal("prepared constant changed", again, e)
		}
		if _, e = contextual.ExecContext(ctx); e != nil {
			t.Fatal(e)
		}
	}
	rows, e := conn.QueryContext(ctx, "SELECT v FROM t")
	if e != nil {
		t.Fatal(e)
	}
	for rows.Next() {
		var got time.Time
		if e = rows.Scan(&got); e != nil || !got.Equal(first) {
			t.Fatal("contextual constant changed", got, e)
		}
	}
	if e = rows.Err(); e != nil {
		t.Fatal(e)
	}
	rows.Close()
	txBefore := time.Now().UTC()
	tx, e := conn.BeginTx(ctx, nil)
	if e != nil {
		t.Fatal(e)
	}
	defer tx.Rollback()
	txAfter := time.Now().UTC()
	time.Sleep(3 * time.Millisecond)
	if _, e = tx.ExecContext(ctx, "INSERT INTO t VALUES('now'),('now')"); e != nil {
		t.Fatal(e)
	}
	var now, today, tomorrow, yesterday time.Time
	if e = tx.QueryRowContext(ctx, "SELECT TIMESTAMP 'now',TIMESTAMP 'today',TIMESTAMP 'tomorrow',TIMESTAMP 'yesterday' FROM anchor").Scan(&now, &today, &tomorrow, &yesterday); e != nil {
		t.Fatal(e)
	}
	if now.Before(txBefore.Add(-time.Microsecond)) || now.After(txAfter.Add(time.Microsecond)) {
		t.Fatal("statement arrival replaced transaction clock", now, txBefore, txAfter)
	}
	midnight := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	if !today.Equal(midnight) || !tomorrow.Equal(midnight.AddDate(0, 0, 1)) || !yesterday.Equal(midnight.AddDate(0, 0, -1)) {
		t.Fatal(today, tomorrow, yesterday)
	}
	var during time.Time
	if e = literal.QueryRowContext(ctx).Scan(&during); e != nil || !during.Equal(frozen) {
		t.Fatal("original physical literal changed in transaction", during, frozen, e)
	}
	if _, e = contextual.ExecContext(ctx); e != nil {
		t.Fatal(e)
	}
	txParam := tx.StmtContext(ctx, param)
	if _, e = txParam.ExecContext(ctx, "now"); e != nil {
		t.Fatal(e)
	}
	if _, e = txParam.ExecContext(ctx, "now"); e != nil {
		t.Fatal(e)
	}
	txParam.Close()
	rows, e = tx.QueryContext(ctx, "SELECT v FROM t WHERE v=$1", now)
	if e != nil {
		t.Fatal(e)
	}
	count := 0
	for rows.Next() {
		count++
	}
	e = rows.Err()
	rows.Close()
	if e != nil || count != 4 {
		t.Fatal("transaction clock unequal", count, e)
	}
	if e = tx.Commit(); e != nil {
		t.Fatal(e)
	}
	var afterCommit time.Time
	if e = literal.QueryRowContext(ctx).Scan(&afterCommit); e != nil || !afterCommit.Equal(frozen) {
		t.Fatal("original physical literal changed after commit", afterCommit, frozen, e)
	}
	rows, e = conn.QueryContext(ctx, "SELECT v FROM t WHERE v=$1", first)
	if e != nil {
		t.Fatal(e)
	}
	timestampCheckRows(t, rows, []string{"v"}, []string{"TIMESTAMP"}, [][]any{{first}, {first}, {first}, {first}})
	time.Sleep(3 * time.Millisecond)
	if _, e = param.ExecContext(ctx, "now"); e != nil {
		t.Fatal(e)
	}
	var refreshed time.Time
	if e = conn.QueryRowContext(ctx, "SELECT v FROM t ORDER BY v DESC").Scan(&refreshed); e != nil || !refreshed.After(now) {
		t.Fatal("parameter clock frozen", refreshed, e)
	}
}
func TestTimestampPrecisionNoticesAndUtilityPhase(t *testing.T) {
	d := stuntgres.NewDriver()
	base, e := d.OpenConnector(t.Name())
	if e != nil {
		t.Fatal(e)
	}
	var notices []stuntgres.Error
	db := sql.OpenDB(base.(*stuntgres.Connector).WithNoticeHandler(func(n stuntgres.Error) { notices = append(notices, n) }))
	defer db.Close()
	db.SetMaxOpenConns(1)
	execSQL(t, db, "CREATE TABLE anchor(n INT)")
	execSQL(t, db, "INSERT INTO anchor VALUES(1)")
	stmt, e := db.Prepare("SELECT TIMESTAMP(7) '2000-01-01 00:00:00.1234567' FROM anchor")
	if e != nil {
		t.Fatal(e)
	}
	defer stmt.Close()
	want := []stuntgres.Error{{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: "22023", Message: "TIMESTAMP(7) precision reduced to maximum allowed, 6", Position: "8"}}
	if !reflect.DeepEqual(notices, want) {
		t.Fatal(notices, want)
	}
	notices = nil
	for range 2 {
		rows, e := stmt.Query()
		if e != nil {
			t.Fatal(e)
		}
		timestampCheckRows(t, rows, []string{"timestamp"}, []string{"TIMESTAMP"}, [][]any{{time.Date(2000, 1, 1, 0, 0, 0, 123457000, time.UTC)}})
	}
	if len(notices) != 0 {
		t.Fatal("execution repeated warning", notices)
	}
	utilitySQL := "CREATE TABLE t(v TIMESTAMP(7))"
	utility, e := db.Prepare(utilitySQL)
	if e != nil {
		t.Fatal(e)
	}
	if len(notices) != 0 {
		t.Fatal("utility transformed at prepare")
	}
	if _, e = utility.Exec(); e != nil {
		t.Fatal(e)
	}
	utility.Close()
	want = append(want, want[0])
	want[0].Position = strconv.Itoa(strings.Index(utilitySQL, "TIMESTAMP") + 1)
	want[1].Position = ""
	if !reflect.DeepEqual(notices, want) {
		t.Fatal(notices, want)
	}
	notices = nil
	execSQL(t, db, "CREATE TABLE IF NOT EXISTS t(v TIMESTAMP(100))")
	skip := []stuntgres.Error{{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "42P07", Message: `relation "t" already exists, skipping`}}
	if !reflect.DeepEqual(notices, skip) {
		t.Fatal(notices, skip)
	}
	notices = nil
	execSQL(t, db, "SET client_min_messages TO error")
	execSQL(t, db, "CREATE TABLE silent(v TIMESTAMP(100))")
	if len(notices) != 0 {
		t.Fatal(notices)
	}
	execSQL(t, db, "SET client_min_messages TO notice")
	loudSQL := "CREATE TABLE loud(v TIMESTAMP(100))"
	execSQL(t, db, loudSQL)
	for i := range want {
		want[i].Message = "TIMESTAMP(100) precision reduced to maximum allowed, 6"
	}
	want[0].Position = strconv.Itoa(strings.Index(loudSQL, "TIMESTAMP") + 1)
	if !reflect.DeepEqual(notices, want) {
		t.Fatal(notices, want)
	}
	notices = nil
	manySQL := "CREATE TABLE many(a TIMESTAMP(7),b TIMESTAMP(8))"
	execSQL(t, db, manySQL)
	phaseWant := []stuntgres.Error{}
	for _, phase := range []bool{true, false} {
		for i, p := range []int{7, 8} {
			n := stuntgres.Error{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: "22023", Message: "TIMESTAMP(" + strconv.Itoa(p) + ") precision reduced to maximum allowed, 6"}
			if phase {
				offset := strings.Index(manySQL, "TIMESTAMP")
				if i == 1 {
					offset = strings.LastIndex(manySQL, "TIMESTAMP")
				}
				n.Position = strconv.Itoa(offset + 1)
			}
			phaseWant = append(phaseWant, n)
		}
	}
	if !reflect.DeepEqual(notices, phaseWant) {
		t.Fatal(notices, phaseWant)
	}
}
func TestTimestampOrderingAndNativeRoundTrip(t *testing.T) {
	ctx := context.Background()
	d := stuntgres.NewDriver()
	db := openLocal(t, d, t.Name())
	execSQL(t, db, "CREATE TABLE t(id INTEGER NOT NULL,v TIMESTAMP,p0 TIMESTAMP(0),p3 TIMESTAMP(3),p6 TIMESTAMP(6))")
	input := []any{"-infinity", "0001-01-01 BC", "epoch", "1999-12-31 23:59:59.999999", "2000-01-01", "2000-01-01", "2000-01-01 00:00:00.000001", "infinity", nil}
	for i, s := range input {
		execSQL(t, db, "INSERT INTO t VALUES($1,$2,$2,$2,$2)", i+1, s)
	}
	execSQL(t, db, "CREATE TABLE endpoint(v TIMESTAMP(6))")
	heldEndpoint, e := db.Prepare("INSERT INTO endpoint VALUES(TIMESTAMP(0) '294276-12-31 23:59:59.999999')")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = heldEndpoint.Exec(); e != nil {
		t.Fatal(e)
	}
	heldEndpoint.Close()
	timestampQuery(t, db, "SELECT v FROM endpoint", []string{"v"}, []string{"TIMESTAMP"}, [][]any{{time.Date(294277, 1, 1, 0, 0, 0, 0, time.UTC)}})
	asc := [][]any{}
	desc := [][]any{}
	for i := range 9 {
		asc = append(asc, []any{int64(i + 1)})
		desc = append(desc, []any{int64(9 - i)})
	}
	// Duplicate midnight keys have no portable secondary order: assert their bag.
	for _, q := range []string{"SELECT id FROM t ORDER BY v", "SELECT id FROM t ORDER BY v DESC"} {
		rows, e := db.Query(q)
		if e != nil {
			t.Fatal(e)
		}
		var ids []int64
		for rows.Next() {
			var id int64
			if e = rows.Scan(&id); e != nil {
				t.Fatal(e)
			}
			ids = append(ids, id)
		}
		e = rows.Err()
		rows.Close()
		if e != nil || len(ids) != 9 {
			t.Fatal(ids, e)
		}
		want := asc
		if strings.HasSuffix(q, "DESC") {
			want = desc
		}
		duplicates := map[int64]int{}
		for i, id := range ids {
			expected := want[i][0].(int64)
			if (expected == 5 || expected == 6) && (id == 5 || id == 6) {
				duplicates[id]++
				continue
			}
			if id != expected {
				t.Fatal(ids, want)
			}
		}
		if duplicates[5] != 1 || duplicates[6] != 1 {
			t.Fatal("duplicate-key multiplicity", ids)
		}
	}
	timestampQuery(t, db, "SELECT id FROM t WHERE v='2000-01-01' ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(5)}, {int64(6)}})
	timestampQuery(t, db, "SELECT id FROM t WHERE v=$1 ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(8)}}, "infinity")
	timestampQuery(t, db, "SELECT id FROM t WHERE v=$1 ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}}, "-infinity")
	timestampQuery(t, db, "SELECT id FROM t WHERE v=$1", []string{"id"}, []string{"INT4"}, nil, nil)
	timestampQuery(t, db, "SELECT id FROM t WHERE p0='2000-01-01 00:00:00.4'", []string{"id"}, []string{"INT4"}, nil)
	raw, e := d.Open(t.Name())
	if e != nil {
		t.Fatal(e)
	}
	defer raw.Close()
	held, e := raw.(driver.QueryerContext).QueryContext(ctx, "SELECT v FROM t ORDER BY id", nil)
	if e != nil {
		t.Fatal(e)
	}
	defer held.Close()
	heldNames := held.Columns()
	execSQL(t, db, "CREATE TABLE fraction_forms(id INTEGER,v TIMESTAMP(3))")
	execSQL(t, db, "INSERT INTO fraction_forms VALUES(1,TIMESTAMP 'J2451187.1'),(2,'1999-01-08 04:05:06.')")
	var dump bytes.Buffer
	if e = stuntgres.Dump(ctx, db, &dump); e != nil {
		t.Fatal(e)
	}
	for _, fragment := range []string{`"v" TIMESTAMP`, `"p0" TIMESTAMP(0)`, `"p3" TIMESTAMP(3)`, `"p6" TIMESTAMP(6)`, `'0001-01-01 00:00:00 BC'`, `'infinity'`, `TIMESTAMP(0) '294276-12-31 23:59:59.999999'`, `'1999-01-08 02:24:00'`, `'1999-01-08 04:05:06'`} {
		if !strings.Contains(dump.String(), fragment) {
			t.Fatal(fragment, dump.String())
		}
	}
	execSQL(t, db, "INSERT INTO t VALUES(10,'now',NULL,NULL,NULL)")
	if e = stuntgres.Reset(ctx, db); e != nil {
		t.Fatal(e)
	}
	if e = stuntgres.Restore(ctx, db, bytes.NewReader(dump.Bytes())); e != nil {
		t.Fatal(e)
	}
	var redump bytes.Buffer
	if e = stuntgres.Dump(ctx, db, &redump); e != nil || redump.String() != dump.String() {
		t.Fatal(redump.String(), e)
	}
	timestampQuery(t, db, "SELECT v FROM fraction_forms ORDER BY id", []string{"v"}, []string{"TIMESTAMP"}, [][]any{{time.Date(1999, 1, 8, 2, 24, 0, 0, time.UTC)}, {time.Date(1999, 1, 8, 4, 5, 6, 0, time.UTC)}})
	if !reflect.DeepEqual(heldNames, []string{"v"}) || held.(driver.RowsColumnTypeDatabaseTypeName).ColumnTypeDatabaseTypeName(0) != "TIMESTAMP" {
		t.Fatal("retained metadata changed")
	}
	dst := make([]driver.Value, 1)
	for i := range 9 {
		if e = held.Next(dst); e != nil {
			t.Fatal(i, e)
		}
		if i == 0 && !reflect.DeepEqual(dst[0], []byte("-infinity")) {
			t.Fatal(dst)
		}
		if i == 1 {
			tm, ok := dst[0].(time.Time)
			if !ok || tm.Year() != 0 {
				t.Fatal(dst)
			}
		}
	}
	if e = held.Next(dst); e != io.EOF {
		t.Fatal(e)
	}
	for _, tc := range []struct{ script, code, message, token string }{
		{"CREATE TABLE replacement(v TIMESTAMP); INSERT INTO replacement VALUES('epoch'),('nonsense')", "22007", `invalid input syntax for type timestamp: "nonsense"`, "'nonsense'"},
		{`CREATE TABLE replacement(v "timestamp"(-1))`, "22023", "TIMESTAMP(-1) precision must not be negative", `"timestamp"`},
		{"CREATE TABLE replacement(v TIMESTAMP); INSERT INTO replacement VALUES($1)", "42P02", "parameters are not allowed in restore", "$1"},
		{"CREATE TABLE replacement(v TIMESTAMP); INSERT INTO replacement VALUES('2023-02-29')", "22008", `date/time field value out of range: "2023-02-29"`, "'2023-02-29'"},
	} {
		e = stuntgres.Restore(ctx, db, strings.NewReader(tc.script))
		typeCoreDiagnostic(t, e, stuntgres.Error{Code: tc.code, Message: tc.message, Position: strconv.Itoa(strings.Index(tc.script, tc.token) + 1)})
		redump.Reset()
		if e = stuntgres.Dump(ctx, db, &redump); e != nil || redump.String() != dump.String() {
			t.Fatal("failed replacement changed state", e, redump.String())
		}
	}
	cancelCtx, cancel := context.WithCancel(ctx)
	cursor, e := raw.(driver.QueryerContext).QueryContext(cancelCtx, "SELECT v FROM t", nil)
	if e != nil {
		t.Fatal(e)
	}
	cancel()
	e = cursor.Next(dst)
	cursor.Close()
	if !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
}
