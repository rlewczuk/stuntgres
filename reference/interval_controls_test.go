//go:build reference

package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"reflect"
	"strings"
	"stuntgres"
	"testing"
)

func TestReferenceIntervalNoticePhases(t *testing.T) {
	p := newReferencePair(t)
	for _, postgres := range []bool{false, true} {
		t.Run(map[bool]string{false: "native", true: "postgres"}[postgres], func(t *testing.T) {
			var notices []referenceDiagnostic
			_, conn, record := noticeReferencePool(t, p, postgres, &notices)
			referenceRunStep(t, p.ctx, conn, record, referenceExec("CREATE TABLE anchor(v INTERVAL)", 0))
			referenceRunStep(t, p.ctx, conn, record, referenceExec("INSERT INTO anchor VALUES('1 day')", 1))
			stmt, e := conn.PrepareContext(p.ctx, "SELECT INTERVAL(7) '1.234567 seconds' FROM anchor")
			if e != nil {
				t.Fatal(e)
			}
			defer stmt.Close()
			want := referenceDiagnostic{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: "22023", Message: "INTERVAL(7) precision reduced to maximum allowed, 6", Position: "8"}
			if !reflect.DeepEqual(notices, []referenceDiagnostic{want}) {
				t.Fatal(notices, want)
			}
			notices = nil
			for range 2 {
				var got string
				if e = stmt.QueryRowContext(p.ctx).Scan(&got); e != nil || got != "00:00:01.234567" {
					t.Fatal(got, e)
				}
			}
			if len(notices) != 0 {
				t.Fatal("repeated warning", notices)
			}
			suffix, e := conn.PrepareContext(p.ctx, "SELECT INTERVAL '1.234567 seconds' SECOND(7) FROM anchor")
			if e != nil {
				t.Fatal(e)
			}
			suffix.Close()
			if !reflect.DeepEqual(notices, []referenceDiagnostic{want}) {
				t.Fatal(notices, want)
			}
			notices = nil
			utility, e := conn.PrepareContext(p.ctx, "CREATE TABLE ivp(v INTERVAL(7))")
			if e != nil || len(notices) != 0 {
				t.Fatal(e, notices)
			}
			_, e = utility.ExecContext(p.ctx)
			utility.Close()
			if e != nil {
				t.Fatal(e)
			}
			want.Position = "20"
			plain := want
			plain.Position = ""
			if !reflect.DeepEqual(notices, []referenceDiagnostic{want, plain}) {
				t.Fatal(notices, want, plain)
			}
		})
	}
}

func TestReferenceIntervalNativeRoundTrip(t *testing.T) {
	p := newReferencePair(t)
	queries := []string{
		"CREATE TABLE iv(id INTEGER,v INTERVAL,p INTERVAL SECOND(2) DEFAULT INTERVAL '1.235 seconds')",
		"INSERT INTO iv VALUES(1,'24 hours','1.235 seconds'),(2,'1 day',NULL),(3,'30 days',NULL),(4,'1 mon',NULL),(5,'-14 months +3 days -4:05:06',NULL),(6,'infinity',NULL),(7,NULL,NULL)",
		"CREATE VIEW ivview AS SELECT INTERVAL '1.235 seconds' SECOND(2) AS v FROM iv WHERE id=1",
	}
	for _, db := range []referenceExecutor{p.memory, p.postgres} {
		for _, q := range queries {
			if _, e := db.ExecContext(p.ctx, q); e != nil {
				t.Fatal(e)
			}
		}
	}
	want := [][]any{{int64(1), "24:00:00", "00:00:01.24"}, {int64(2), "1 day", nil}, {int64(3), "30 days", nil}, {int64(4), "1 mon", nil}, {int64(5), "-1 years -2 mons +3 days -04:05:06", nil}, {int64(6), "infinity", nil}, {int64(7), nil, nil}}
	referenceRunStep(t, p.ctx, p.postgres, p.postgresRecord, referenceSelect("SELECT id,v,p FROM iv ORDER BY id", []string{"id", "v", "p"}, []string{"INT4", "INTERVAL", "INTERVAL"}, want))
	var dump bytes.Buffer
	if e := stuntgres.Dump(p.ctx, p.native, &dump); e != nil {
		t.Fatal(e)
	}
	for _, style := range []string{"postgres", "postgres_verbose", "sql_standard", "iso_8601"} {
		if _, e := p.native.ExecContext(p.ctx, "SET IntervalStyle="+style); e != nil {
			t.Fatal(e)
		}
		var got bytes.Buffer
		if e := stuntgres.Dump(p.ctx, p.native, &got); e != nil || got.String() != dump.String() {
			t.Fatal(style, e, "style-dependent serialization")
		}
	}
	old, e := p.memory.QueryContext(p.ctx, "SELECT v FROM iv WHERE id=1")
	if e != nil {
		t.Fatal(e)
	}
	defer old.Close()
	if e = stuntgres.Restore(p.ctx, p.native, bytes.NewReader(dump.Bytes())); e != nil {
		t.Fatal(e)
	}
	referenceRunStep(t, p.ctx, p.memory, p.memoryRecord, referenceSelect("SELECT id,v,p FROM iv ORDER BY id", []string{"id", "v", "p"}, []string{"INT4", "INTERVAL", "INTERVAL"}, want))
	referenceRunStep(t, p.ctx, p.memory, p.memoryRecord, referenceSelect("SELECT v FROM ivview", []string{"v"}, []string{"INTERVAL"}, [][]any{{"00:00:01.24"}}))
	if !old.Next() {
		t.Fatal(old.Err())
	}
	var value string
	if e = old.Scan(&value); e != nil || value != "24:00:00" {
		t.Fatal(value, e)
	}
	var again bytes.Buffer
	if e = stuntgres.Dump(p.ctx, p.native, &again); e != nil || again.String() != dump.String() {
		t.Fatal(e, "redump differs")
	}
	if e = stuntgres.Restore(p.ctx, p.native, strings.NewReader("CREATE TABLE bad(v INTERVAL);INSERT INTO bad VALUES('1 day'),('invalid')")); e == nil {
		t.Fatal("bad restore succeeded")
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if e = stuntgres.Restore(canceled, p.native, bytes.NewReader(dump.Bytes())); !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
	referenceRunStep(t, p.ctx, p.memory, p.memoryRecord, referenceSelect("SELECT id,v,p FROM iv ORDER BY id", []string{"id", "v", "p"}, []string{"INT4", "INTERVAL", "INTERVAL"}, want))
}

func TestReferenceIntervalCancellation(t *testing.T) {
	p := newReferencePair(t)
	for _, prepared := range []bool{false, true} {
		a, b := referenceLockConnections(t, p.ctx, p.postgres)
		referenceLockExec(t, p.ctx, a, "CREATE TABLE ivcancel(v INTERVAL)", 0)
		referenceLockExec(t, p.ctx, a, "INSERT INTO ivcancel VALUES('1 day')", 1)
		var stmt *sql.Stmt
		var err error
		if prepared {
			stmt, err = b.PrepareContext(p.ctx, "INSERT INTO ivcancel VALUES($1)")
			if err != nil {
				t.Fatal(err)
			}
		}
		holder, err := a.BeginTx(p.ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		referenceLockExec(t, p.ctx, holder, "LOCK TABLE ivcancel IN ACCESS EXCLUSIVE MODE", 0)
		var pid int
		if err = b.QueryRowContext(p.ctx, "SELECT pg_backend_pid()").Scan(&pid); err != nil {
			t.Fatal(err)
		}
		done := make(chan relationOutcome, 1)
		go func() {
			o := relationOutcome{}
			if prepared {
				o.result, o.err = stmt.ExecContext(p.ctx, "1 mon")
			} else {
				o.result, o.err = b.ExecContext(p.ctx, "INSERT INTO ivcancel VALUES($1)", "1 mon")
			}
			done <- o
		}()
		referenceLockWait(t, p.ctx, pid)
		var canceled bool
		if err = referenceAdmin.QueryRowContext(p.ctx, "SELECT pg_cancel_backend($1)", pid).Scan(&canceled); err != nil || !canceled {
			t.Fatal(canceled, err)
		}
		outcome := referenceLockResult(t, done)
		referenceAssertDiagnostic(t, outcome.err, &referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "57014", Message: "canceling statement due to user request"})
		if stmt != nil {
			stmt.Close()
		}
		if err = holder.Rollback(); err != nil {
			t.Fatal(err)
		}
		referenceRunStep(t, p.ctx, b, p.postgresRecord, referenceSelect("SELECT v FROM ivcancel", []string{"v"}, []string{"INTERVAL"}, [][]any{{"1 day"}}))
		referenceLockExec(t, p.ctx, b, "DROP TABLE ivcancel", 0)
		a.Close()
		b.Close()
	}
	referenceRunStep(t, p.ctx, p.memory, p.memoryRecord, referenceExec("CREATE TABLE ivcancel(v INTERVAL)", 0))
	canceled, cancel := context.WithCancel(p.ctx)
	cancel()
	if _, err := p.memory.ExecContext(canceled, "INSERT INTO ivcancel VALUES($1)", "1 day"); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	referenceRunStep(t, p.ctx, p.memory, p.memoryRecord, referenceSelect("SELECT v FROM ivcancel", []string{"v"}, []string{"INTERVAL"}, nil))
}
