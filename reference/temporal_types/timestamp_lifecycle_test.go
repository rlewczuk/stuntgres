package reference_test

import (
	"bytes"
	"context"
	"database/sql/driver"
	"errors"
	"io"
	"reflect"
	"strconv"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func referenceTimestampOrderingSeed(t *testing.T, p *fixture.Pair) {
	t.Helper()
	db := p.Memory
	ctx := context.WithValue(p.Ctx, fixture.RecorderKey{}, p.MemoryRecord)
	referenceTimestampExec(t, ctx, db, "CREATE TABLE t(id INTEGER NOT NULL,v TIMESTAMP,p0 TIMESTAMP(0),p3 TIMESTAMP(3),p6 TIMESTAMP(6))")
	input := []any{"-infinity", "0001-01-01 BC", "epoch", "1999-12-31 23:59:59.999999", "2000-01-01", "2000-01-01", "2000-01-01 00:00:00.000001", "infinity", nil}
	for i, s := range input {
		referenceTimestampExec(t, ctx, db, "INSERT INTO t VALUES($1,$2,$2,$2,$2)", i+1, s)
	}
	referenceTimestampExec(t, ctx, db, "CREATE TABLE endpoint(v TIMESTAMP(6))")
	heldEndpoint, e := db.PrepareContext(ctx, "INSERT INTO endpoint VALUES(TIMESTAMP(0) '294276-12-31 23:59:59.999999')")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = heldEndpoint.ExecContext(ctx); e != nil {
		t.Fatal(e)
	}
	heldEndpoint.Close()
	referenceTimestampQuery(t, ctx, db, p.MemoryRecord, "SELECT v FROM endpoint", []string{"v"}, []string{"TIMESTAMP"}, [][]any{{time.Date(294277, 1, 1, 0, 0, 0, 0, time.UTC)}})
}

func TestReferenceTimestampRetainedNativeCursorAndRoundTrip(t *testing.T) {
	p := newTimestampReferenceScenario(t)
	referenceTimestampOrderingSeed(t, p)
	db := p.Memory
	ctx := context.WithValue(p.Ctx, fixture.RecorderKey{}, p.MemoryRecord)
	conn, err := p.Native.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := conn.Raw(func(raw any) error {
		held, e := raw.(driver.QueryerContext).QueryContext(ctx, "SELECT v FROM t ORDER BY id", nil)
		if e != nil {
			t.Fatal(e)
		}
		defer held.Close()
		heldNames := held.Columns()
		referenceTimestampExec(t, ctx, db, "CREATE TABLE fraction_forms(id INTEGER,v TIMESTAMP(3))")
		referenceTimestampExec(t, ctx, db, "INSERT INTO fraction_forms VALUES(1,TIMESTAMP 'J2451187.1'),(2,'1999-01-08 04:05:06.')")
		var dump bytes.Buffer
		if e = stuntgres.Dump(ctx, p.Native, &dump); e != nil {
			t.Fatal(e)
		}
		for _, fragment := range []string{`"v" TIMESTAMP`, `"p0" TIMESTAMP(0)`, `"p3" TIMESTAMP(3)`, `"p6" TIMESTAMP(6)`, `'0001-01-01 00:00:00 BC'`, `'infinity'`, `TIMESTAMP(0) '294276-12-31 23:59:59.999999'`, `'1999-01-08 02:24:00'`, `'1999-01-08 04:05:06'`} {
			if !strings.Contains(dump.String(), fragment) {
				t.Fatal(fragment, dump.String())
			}
		}
		referenceTimestampExec(t, ctx, db, "INSERT INTO t VALUES(10,'now',NULL,NULL,NULL)")
		if e = stuntgres.Reset(ctx, p.Native); e != nil {
			t.Fatal(e)
		}
		if e = stuntgres.Restore(ctx, p.Native, bytes.NewReader(dump.Bytes())); e != nil {
			t.Fatal(e)
		}
		var redump bytes.Buffer
		if e = stuntgres.Dump(ctx, p.Native, &redump); e != nil || redump.String() != dump.String() {
			t.Fatal(redump.String(), e)
		}
		referenceTimestampQuery(t, ctx, db, p.MemoryRecord, "SELECT v FROM fraction_forms ORDER BY id", []string{"v"}, []string{"TIMESTAMP"}, [][]any{{time.Date(1999, 1, 8, 2, 24, 0, 0, time.UTC)}, {time.Date(1999, 1, 8, 4, 5, 6, 0, time.UTC)}})
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
		if e = held.Close(); e != nil {
			t.Fatal(e)
		}
		if p.Postgres != nil {
			if _, e = p.Postgres.ExecContext(ctx, dump.String()); e != nil {
				t.Fatal(e)
			}
		}
		referenceTimestampStep(t, p, fixture.Select("SELECT v FROM t ORDER BY id", []string{"v"}, []string{"TIMESTAMP"}, [][]any{{"-infinity"}, {time.Date(0, 1, 1, 0, 0, 0, 0, time.UTC)}, {time.Date(1970, 1, 1, 0, 0, 0, 0, time.UTC)}, {time.Date(1999, 12, 31, 23, 59, 59, 999999000, time.UTC)}, {time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)}, {time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)}, {time.Date(2000, 1, 1, 0, 0, 0, 1000, time.UTC)}, {"infinity"}, {nil}}))
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestReferenceTimestampRestoreDiagnosticsAndCancellation(t *testing.T) {
	p := newTimestampReferenceScenario(t)
	referenceTimestampOrderingSeed(t, p)
	ctx := context.WithValue(p.Ctx, fixture.RecorderKey{}, p.MemoryRecord)
	var dump, redump bytes.Buffer
	if err := stuntgres.Dump(ctx, p.Native, &dump); err != nil {
		t.Fatal(err)
	}
	conn, err := p.Native.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := conn.Raw(func(raw any) error {
		var e error
		dst := make([]driver.Value, 1)
		for _, tc := range []struct{ script, code, message, token string }{
			{"CREATE TABLE replacement(v TIMESTAMP); INSERT INTO replacement VALUES('epoch'),('nonsense')", "22007", `invalid input syntax for type timestamp: "nonsense"`, "'nonsense'"},
			{`CREATE TABLE replacement(v "timestamp"(-1))`, "22023", "TIMESTAMP(-1) precision must not be negative", `"timestamp"`},
			{"CREATE TABLE replacement(v TIMESTAMP); INSERT INTO replacement VALUES($1)", "42P02", "parameters are not allowed in restore", "$1"},
			{"CREATE TABLE replacement(v TIMESTAMP); INSERT INTO replacement VALUES('2023-02-29')", "22008", `date/time field value out of range: "2023-02-29"`, "'2023-02-29'"},
		} {
			e = stuntgres.Restore(ctx, p.Native, strings.NewReader(tc.script))
			fixture.TemporalDiagnostic(t, e, stuntgres.Error{Code: tc.code, Message: tc.message, Position: strconv.Itoa(strings.Index(tc.script, tc.token) + 1)})
			redump.Reset()
			if e = stuntgres.Dump(ctx, p.Native, &redump); e != nil || redump.String() != dump.String() {
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
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
