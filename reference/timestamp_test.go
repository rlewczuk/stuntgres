//go:build reference

package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"stuntgres"
	"testing"
	"time"
)

func TestReferenceTimestampClockAndPreparedLifetime(t *testing.T) {
	p := newTimestampReferencePair(t)
	for backend, db := range []*sql.DB{p.memory, p.postgres} {
		t.Run(fmt.Sprint(backend), func(t *testing.T) {
			record := p.memoryRecord
			if backend == 1 {
				record = p.postgresRecord
			}
			ctx := context.WithValue(p.ctx, referenceRecorderKey{}, record)
			conn, e := db.Conn(ctx)
			if e != nil {
				t.Fatal(e)
			}
			defer conn.Close()
			for _, q := range []string{"CREATE TABLE anchor(n INTEGER)", "INSERT INTO anchor VALUES(1)", "CREATE TABLE t(v TIMESTAMP)"} {
				if _, e = conn.ExecContext(ctx, q); e != nil {
					t.Fatal(e)
				}
			}
			before := time.Now().UTC()
			typed, e := conn.PrepareContext(ctx, "SELECT TIMESTAMP 'now' FROM anchor")
			if e != nil {
				t.Fatal(e)
			}
			defer typed.Close()
			after := time.Now().UTC()
			var frozen time.Time
			if e = typed.QueryRowContext(ctx).Scan(&frozen); e != nil {
				t.Fatal(e)
			}
			if frozen.Before(before.Add(-time.Microsecond)) || frozen.After(after.Add(time.Microsecond)) {
				t.Fatal("analysis clock interval", frozen, before, after)
			}
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
			if _, e = contextual.ExecContext(ctx); e != nil {
				t.Fatal(e)
			}
			var captured time.Time
			if e = conn.QueryRowContext(ctx, "SELECT v FROM t").Scan(&captured); e != nil {
				t.Fatal(e)
			}
			time.Sleep(3 * time.Millisecond)
			for range 2 {
				var again time.Time
				if e = typed.QueryRowContext(ctx).Scan(&again); e != nil || !again.Equal(frozen) {
					t.Fatal("typed now did not freeze", again, e)
				}
				if _, e = contextual.ExecContext(ctx); e != nil {
					t.Fatal(e)
				}
			}
			referenceRunStep(t, ctx, conn, record, referenceSelect("SELECT v FROM t", []string{"v"}, []string{"TIMESTAMP"}, [][]any{{captured.Round(0).UTC()}, {captured.Round(0).UTC()}, {captured.Round(0).UTC()}}))
			before = time.Now().UTC()
			tx, e := conn.BeginTx(ctx, nil)
			if e != nil {
				t.Fatal(e)
			}
			defer tx.Rollback()
			after = time.Now().UTC()
			time.Sleep(3 * time.Millisecond)
			var now, today, tomorrow, yesterday, zonedToday time.Time
			if e = tx.QueryRowContext(ctx, "SELECT TIMESTAMP 'now',TIMESTAMP 'today',TIMESTAMP 'tomorrow',TIMESTAMP 'yesterday',TIMESTAMP 'today+2' FROM anchor").Scan(&now, &today, &tomorrow, &yesterday, &zonedToday); e != nil {
				t.Fatal(e)
			}
			if now.Before(before.Add(-time.Microsecond)) || now.After(after.Add(time.Microsecond)) {
				t.Fatal("transaction start interval", now, before, after)
			}
			midnight := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
			if !today.Equal(midnight) || !tomorrow.Equal(midnight.AddDate(0, 0, 1)) || !yesterday.Equal(midnight.AddDate(0, 0, -1)) {
				t.Fatal(today, tomorrow, yesterday)
			}
			if !zonedToday.Equal(midnight) {
				t.Fatal("core today plus numeric zone shifted civil fields", zonedToday, midnight)
			}
			zoned, e := tx.PrepareContext(ctx, "SELECT TIMESTAMP 'today+2' FROM anchor")
			if e != nil {
				t.Fatal(e)
			}
			defer zoned.Close()
			e = zoned.QueryRowContext(ctx).Scan(&zonedToday)
			zoned.Close()
			if e != nil || !zonedToday.Equal(midnight) {
				t.Fatal("prepared core today plus numeric zone", zonedToday, midnight, e)
			}
			if _, e = tx.ExecContext(ctx, "INSERT INTO t VALUES('now'),('now')"); e != nil {
				t.Fatal(e)
			}
			bound := tx.StmtContext(ctx, param)
			for range 2 {
				if _, e = bound.ExecContext(ctx, "now"); e != nil {
					t.Fatal(e)
				}
			}
			bound.Close()
			referenceRunStep(t, ctx, tx, record, referenceSelect("SELECT v FROM t WHERE v=$1", []string{"v"}, []string{"TIMESTAMP"}, [][]any{{now.Round(0).UTC()}, {now.Round(0).UTC()}, {now.Round(0).UTC()}, {now.Round(0).UTC()}}, now))
			if e = tx.Commit(); e != nil {
				t.Fatal(e)
			}
			time.Sleep(3 * time.Millisecond)
			if _, e = param.ExecContext(ctx, "now"); e != nil {
				t.Fatal(e)
			}
			var refreshed time.Time
			if e = conn.QueryRowContext(ctx, "SELECT v FROM t ORDER BY v DESC").Scan(&refreshed); e != nil || !refreshed.After(now) {
				t.Fatal("parameter froze", refreshed, e)
			}
		})
	}
}
func TestReferenceTimestampPrecisionNoticePhase(t *testing.T) {
	p := newTimestampReferencePair(t)
	for _, postgres := range []bool{false, true} {
		t.Run(fmt.Sprint(postgres), func(t *testing.T) {
			var notices []referenceDiagnostic
			_, conn, record := noticeReferencePool(t, p, postgres, &notices)
			ctx := context.WithValue(p.ctx, referenceRecorderKey{}, record)
			referenceRunStep(t, ctx, conn, record, referenceExec("CREATE TABLE anchor(n INTEGER)", 0))
			referenceRunStep(t, ctx, conn, record, referenceExec("INSERT INTO anchor VALUES(1)", 1))
			stmt, e := conn.PrepareContext(ctx, "SELECT TIMESTAMP(7) '2000-01-01 00:00:00.1234567' FROM anchor")
			if e != nil {
				t.Fatal(e)
			}
			defer stmt.Close()
			want := []referenceDiagnostic{{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: "22023", Message: "TIMESTAMP(7) precision reduced to maximum allowed, 6", Position: "8"}}
			if !reflect.DeepEqual(notices, want) {
				t.Fatalf("prepare warning got %#v want %#v", notices, want)
			}
			notices = nil
			for range 2 {
				var got time.Time
				if e = stmt.QueryRowContext(ctx).Scan(&got); e != nil || !got.Equal(time.Date(2000, 1, 1, 0, 0, 0, 123457000, time.UTC)) {
					t.Fatal(got, e)
				}
			}
			if len(notices) != 0 {
				t.Fatal("execution repeated warning", notices)
			}
			utilitySQL := "CREATE TABLE t(v TIMESTAMP(7))"
			utility, e := conn.PrepareContext(ctx, utilitySQL)
			if e != nil {
				t.Fatal(e)
			}
			if len(notices) != 0 {
				t.Fatal("utility warned at prepare", notices)
			}
			if _, e = utility.ExecContext(ctx); e != nil {
				t.Fatal(e)
			}
			utility.Close()
			want = append(want, want[0])
			want[0].Position = strconv.Itoa(strings.Index(utilitySQL, "TIMESTAMP") + 1)
			want[1].Position = ""
			if !reflect.DeepEqual(notices, want) {
				t.Fatalf("utility execution warning got %#v want %#v", notices, want)
			}
			notices = nil
			referenceRunStep(t, ctx, conn, record, referenceExec("CREATE TABLE IF NOT EXISTS t(v TIMESTAMP(100))", 0))
			skip := []referenceDiagnostic{{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "42P07", Message: `relation "t" already exists, skipping`}}
			if !reflect.DeepEqual(notices, skip) {
				t.Fatal(notices, skip)
			}
			notices = nil
			referenceRunStep(t, ctx, conn, record, referenceExec("SET client_min_messages TO error", 0))
			referenceRunStep(t, ctx, conn, record, referenceExec("CREATE TABLE silent(v TIMESTAMP(100))", 0))
			if len(notices) != 0 {
				t.Fatal(notices)
			}
			referenceRunStep(t, ctx, conn, record, referenceExec("SET client_min_messages TO notice", 0))
			loudSQL := "CREATE TABLE loud(v TIMESTAMP(100))"
			referenceRunStep(t, ctx, conn, record, referenceExec(loudSQL, 0))
			for i := range want {
				want[i].Message = "TIMESTAMP(100) precision reduced to maximum allowed, 6"
			}
			want[0].Position = strconv.Itoa(strings.Index(loudSQL, "TIMESTAMP") + 1)
			if !reflect.DeepEqual(notices, want) {
				t.Fatal(notices, want)
			}
			notices = nil
			manySQL := "CREATE TABLE many(a TIMESTAMP(7),b TIMESTAMP(8))"
			referenceRunStep(t, ctx, conn, record, referenceExec(manySQL, 0))
			phaseWant := []referenceDiagnostic{}
			for _, phase := range []bool{true, false} {
				for i, prec := range []int{7, 8} {
					n := referenceDiagnostic{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: "22023", Message: "TIMESTAMP(" + strconv.Itoa(prec) + ") precision reduced to maximum allowed, 6"}
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
		})
	}
}
func TestReferenceTimestampOrderingAndNativeDump(t *testing.T) {
	p := newTimestampReferencePair(t)
	p.step(t, referenceExec("CREATE TABLE t(id INTEGER,v TIMESTAMP,p TIMESTAMP(0))", 0))
	inputs := []any{"-infinity", "0001-01-01 BC", "epoch", "1999-12-31 23:59:59.999999", "2000-01-01", "2000-01-01", "2000-01-01 00:00:00.000001", "infinity", nil}
	for i, input := range inputs {
		p.step(t, referencePrepared(referenceExec("INSERT INTO t VALUES($1,$2,$2)", 1, i+1, input)))
	}
	// Query only the key: equal-key duplicates assert bag multiplicity without
	// inventing a secondary order for distinct row identifiers.
	expected := [][]any{{"-infinity"}, {time.Date(0, 1, 1, 0, 0, 0, 0, time.UTC)}, {time.Date(1970, 1, 1, 0, 0, 0, 0, time.UTC)}, {time.Date(1999, 12, 31, 23, 59, 59, 999999000, time.UTC)}, {time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)}, {time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)}, {time.Date(2000, 1, 1, 0, 0, 0, 1000, time.UTC)}, {"infinity"}, {nil}}
	p.step(t, referenceSelect("SELECT v FROM t ORDER BY v", []string{"v"}, []string{"TIMESTAMP"}, expected))
	reverse := make([][]any, len(expected))
	for i := range expected {
		reverse[i] = expected[len(expected)-1-i]
	}
	p.step(t, referencePrepared(referenceSelect("SELECT v FROM t ORDER BY v DESC", []string{"v"}, []string{"TIMESTAMP"}, reverse)))
	p.step(t, referencePrepared(referenceSelect("SELECT id FROM t WHERE v=$1 ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(5)}, {int64(6)}}, "2000-01-01")))
	p.step(t, referencePrepared(referenceSelect("SELECT id FROM t WHERE v=$1", []string{"id"}, []string{"INT4"}, nil, nil)))
	p.step(t, referenceSelect("SELECT id FROM t WHERE p='2000-01-01 00:00:00.4'", []string{"id"}, []string{"INT4"}, nil))
	// Native controls retain their own authority; use the dumped SQL as an input
	// witness on real PG, not a false claim of PostgreSQL Reset/Restore APIs.
	native := p
	if e := stuntgres.Restore(native.ctx, native.native, bytes.NewBufferString("CREATE TABLE saved(v TIMESTAMP(6)); INSERT INTO saved VALUES(TIMESTAMP(0) '294276-12-31 23:59:59.999999'),('0001-01-01 BC'),('infinity'),(NULL)")); e != nil {
		t.Fatal(e)
	}
	var dump bytes.Buffer
	if e := stuntgres.Dump(native.ctx, native.native, &dump); e != nil {
		t.Fatal(e)
	}
	if e := stuntgres.Reset(native.ctx, native.native); e != nil {
		t.Fatal(e)
	}
	if e := stuntgres.Restore(native.ctx, native.native, bytes.NewReader(dump.Bytes())); e != nil {
		t.Fatal(e)
	}
	var again bytes.Buffer
	if e := stuntgres.Dump(native.ctx, native.native, &again); e != nil || again.String() != dump.String() {
		t.Fatal(again.String(), e)
	}
	if _, e := native.postgres.ExecContext(native.ctx, dump.String()); e != nil {
		t.Fatal(e)
	}
	native.step(t, referenceSelect("SELECT v FROM saved ORDER BY v", []string{"v"}, []string{"TIMESTAMP"}, [][]any{{time.Date(0, 1, 1, 0, 0, 0, 0, time.UTC)}, {time.Date(294277, 1, 1, 0, 0, 0, 0, time.UTC)}, {"infinity"}, {nil}}))
}
