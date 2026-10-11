package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"reflect"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceIntervalNoticePhases(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		postgres := backend.PG
		t.Run(map[bool]string{false: "native", true: "postgres"}[postgres], func(t *testing.T) {
			var notices []fixture.Diagnostic
			_, conn, record := fixture.NoticeReferencePool(t, p, postgres, &notices)
			record.RawBackend = fixture.RawNative
			if postgres {
				record.RawBackend = fixture.RawPostgres
			}
			fixture.RunStep(t, p.Ctx, conn, record, fixture.Exec("CREATE TABLE anchor(v INTERVAL)", 0))
			fixture.RunStep(t, p.Ctx, conn, record, fixture.Exec("INSERT INTO anchor VALUES('1 day')", 1))
			stmt, e := conn.PrepareContext(p.Ctx, "SELECT INTERVAL(7) '1.234567 seconds' FROM anchor")
			if e != nil {
				t.Fatal(e)
			}
			defer stmt.Close()
			want := fixture.Diagnostic{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: "22023", Message: "INTERVAL(7) precision reduced to maximum allowed, 6", Position: "8"}
			if !reflect.DeepEqual(notices, []fixture.Diagnostic{want}) {
				t.Fatal(notices, want)
			}
			notices = nil
			for range 2 {
				fixture.RunStep(t, p.Ctx, fixture.HeldStatement{conn, stmt, "SELECT INTERVAL(7) '1.234567 seconds' FROM anchor"}, record, fixture.Select("SELECT INTERVAL(7) '1.234567 seconds' FROM anchor", []string{"interval"}, []string{"INTERVAL"}, [][]any{{"00:00:01.234567"}}))
			}
			if len(notices) != 0 {
				t.Fatal("repeated warning", notices)
			}
			suffix, e := conn.PrepareContext(p.Ctx, "SELECT INTERVAL '1.234567 seconds' SECOND(7) FROM anchor")
			if e != nil {
				t.Fatal(e)
			}
			suffix.Close()
			if !reflect.DeepEqual(notices, []fixture.Diagnostic{want}) {
				t.Fatal(notices, want)
			}
			notices = nil
			utility, e := conn.PrepareContext(p.Ctx, "CREATE TABLE ivp(v INTERVAL(7))")
			if e != nil || len(notices) != 0 {
				t.Fatal(e, notices)
			}
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{conn, utility, "CREATE TABLE ivp(v INTERVAL(7))"}, record, fixture.Exec("CREATE TABLE ivp(v INTERVAL(7))", 0))
			if e = utility.Close(); e != nil {
				t.Fatal(e)
			}
			want.Position = "20"
			plain := want
			plain.Position = ""
			if !reflect.DeepEqual(notices, []fixture.Diagnostic{want, plain}) {
				t.Fatal(notices, want, plain)
			}
		})
	}
}

func TestReferenceIntervalNativeRoundTrip(t *testing.T) {
	p := fixture.NewScenario(t)
	queries := []string{
		"CREATE TABLE iv(id INTEGER,v INTERVAL,p INTERVAL SECOND(2) DEFAULT INTERVAL '1.235 seconds')",
		"INSERT INTO iv VALUES(1,'24 hours','1.235 seconds'),(2,'1 day',NULL),(3,'30 days',NULL),(4,'1 mon',NULL),(5,'-14 months +3 days -4:05:06',NULL),(6,'infinity',NULL),(7,NULL,NULL)",
		"CREATE VIEW ivview AS SELECT INTERVAL '1.235 seconds' SECOND(2) AS v FROM iv WHERE id=1",
	}
	p.Step(t, fixture.Exec(queries[0], 0))
	p.Step(t, fixture.Exec(queries[1], 7))
	p.Step(t, fixture.Exec(queries[2], 0))
	want := [][]any{{int64(1), "24:00:00", "00:00:01.24"}, {int64(2), "1 day", nil}, {int64(3), "30 days", nil}, {int64(4), "1 mon", nil}, {int64(5), "-1 years -2 mons +3 days -04:05:06", nil}, {int64(6), "infinity", nil}, {int64(7), nil, nil}}
	p.Step(t, fixture.Select("SELECT id,v,p FROM iv ORDER BY id", []string{"id", "v", "p"}, []string{"INT4", "INTERVAL", "INTERVAL"}, want))
	var dump bytes.Buffer
	if e := stuntgres.Dump(p.Ctx, p.Native, &dump); e != nil {
		t.Fatal(e)
	}
	for _, style := range []string{"postgres", "postgres_verbose", "sql_standard", "iso_8601"} {
		if _, e := p.Native.ExecContext(p.Ctx, "SET IntervalStyle="+style); e != nil {
			t.Fatal(e)
		}
		var got bytes.Buffer
		if e := stuntgres.Dump(p.Ctx, p.Native, &got); e != nil || got.String() != dump.String() {
			t.Fatal(style, e, "style-dependent serialization")
		}
	}
	c, e := p.Memory.Conn(p.Ctx)
	if e != nil {
		t.Fatal(e)
	}
	defer c.Close()
	fixture.RunStep(t, p.Ctx, c, p.MemoryRecord, fixture.Exec("SET IntervalStyle=sql_standard", 0))
	old, e := c.QueryContext(context.WithValue(p.Ctx, fixture.RecorderKey{}, p.MemoryRecord), "SELECT v FROM iv WHERE id=1")
	if e != nil {
		t.Fatal(e)
	}
	defer old.Close()
	if e = stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); e != nil {
		t.Fatal(e)
	}
	fixture.IntervalRawRows(t, old, [][]any{{"24:00:00"}}, p.MemoryRecord)
	fixture.RunStep(t, p.Ctx, p.Memory, p.MemoryRecord, fixture.Select("SELECT id,v,p FROM iv ORDER BY id", []string{"id", "v", "p"}, []string{"INT4", "INTERVAL", "INTERVAL"}, want))
	fixture.RunStep(t, p.Ctx, p.Memory, p.MemoryRecord, fixture.Select("SELECT v FROM ivview", []string{"v"}, []string{"INTERVAL"}, [][]any{{"00:00:01.24"}}))
	var again bytes.Buffer
	if e = stuntgres.Dump(p.Ctx, p.Native, &again); e != nil || again.String() != dump.String() {
		t.Fatal(e, "redump differs")
	}
	if e = stuntgres.Restore(p.Ctx, p.Native, strings.NewReader("CREATE TABLE bad(v INTERVAL);INSERT INTO bad VALUES('1 day'),('invalid')")); e == nil {
		t.Fatal("bad restore succeeded")
	}
	fixture.IntervalDumpMatches(t, p.Ctx, p.Native, dump.Bytes())
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if e = stuntgres.Restore(canceled, p.Native, bytes.NewReader(dump.Bytes())); !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
	fixture.IntervalDumpMatches(t, p.Ctx, p.Native, dump.Bytes())
	fixture.RunStep(t, p.Ctx, p.Memory, p.MemoryRecord, fixture.Select("SELECT id,v,p FROM iv ORDER BY id", []string{"id", "v", "p"}, []string{"INT4", "INTERVAL", "INTERVAL"}, want))
}

func TestReferenceIntervalCancellation(t *testing.T) {
	p := fixture.NewScenario(t)
	if p.Postgres != nil {
		for _, prepared := range []bool{false, true} {
			a, b := fixture.LockConnections(t, p.Ctx, p.Postgres)
			defer a.Close()
			defer b.Close()
			fixture.LockExec(t, p.Ctx, a, "CREATE TABLE ivcancel(v INTERVAL)", 0)
			fixture.LockExec(t, p.Ctx, a, "INSERT INTO ivcancel VALUES('1 day')", 1)
			var stmt *sql.Stmt
			var err error
			if prepared {
				stmt, err = b.PrepareContext(p.Ctx, "INSERT INTO ivcancel VALUES($1)")
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
			}
			var holderPID int
			if err = a.QueryRowContext(p.Ctx, "SELECT pg_backend_pid()").Scan(&holderPID); err != nil {
				t.Fatal(err)
			}
			holder, err := a.BeginTx(p.Ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer holder.Rollback()
			fixture.LockExec(t, p.Ctx, holder, "LOCK TABLE ivcancel IN ACCESS EXCLUSIVE MODE", 0)
			var pid int
			if err = b.QueryRowContext(p.Ctx, "SELECT pg_backend_pid()").Scan(&pid); err != nil {
				t.Fatal(err)
			}
			if pid <= 0 || holderPID <= 0 || pid == holderPID {
				t.Fatal("backend identities", pid, holderPID)
			}
			done := make(chan fixture.RelationOutcome, 1)
			go func() {
				o := fixture.RelationOutcome{}
				if prepared {
					o.Result, o.Err = stmt.ExecContext(p.Ctx, "1 mon")
				} else {
					o.Result, o.Err = b.ExecContext(p.Ctx, "INSERT INTO ivcancel VALUES($1)", "1 mon")
				}
				done <- o
			}()
			fixture.RowWait(t, p.Ctx, pid, holderPID)
			var canceled bool
			if err = fixture.Admin.QueryRowContext(p.Ctx, "SELECT pg_cancel_backend($1)", pid).Scan(&canceled); err != nil || !canceled {
				t.Fatal(canceled, err)
			}
			outcome := fixture.LockResult(t, done)
			fixture.AssertDiagnostic(t, outcome.Err, &fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "57014", Message: "canceling statement due to user request"})
			if stmt != nil {
				stmt.Close()
			}
			if err = holder.Rollback(); err != nil {
				t.Fatal(err)
			}
			fixture.RunStep(t, p.Ctx, b, p.PostgresRecord, fixture.Select("SELECT v FROM ivcancel", []string{"v"}, []string{"INTERVAL"}, [][]any{{"1 day"}}))
			fixture.LockExec(t, p.Ctx, b, "DROP TABLE ivcancel", 0)
			a.Close()
			b.Close()
		}
	}
	fixture.RunStep(t, p.Ctx, p.Memory, p.MemoryRecord, fixture.Exec("CREATE TABLE ivcancel(v INTERVAL)", 0))
	canceled, cancel := context.WithCancel(p.Ctx)
	cancel()
	if _, err := p.Memory.ExecContext(canceled, "INSERT INTO ivcancel VALUES($1)", "1 day"); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	fixture.RunStep(t, p.Ctx, p.Memory, p.MemoryRecord, fixture.Select("SELECT v FROM ivcancel", []string{"v"}, []string{"INTERVAL"}, nil))
}
