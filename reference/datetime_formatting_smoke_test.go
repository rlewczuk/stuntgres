package reference_test

import (
	"bytes"
	"database/sql"
	"stuntgres"
	"testing"
	"time"
)

func TestReferenceDatetimeFormattingFeatureSmoke(t *testing.T) {
	datetimeFormattingScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, conn *sql.Conn) {
		run := func(s referenceStep) { referenceRunStep(t, p.ctx, conn, b.recorder, s) }
		run(referenceExec("CREATE TABLE dt_format_smoke(id INTEGER,ts TIMESTAMP,z TIMESTAMPTZ,iv INTERVAL,d DATE)", 0))
		insertSQL := "INSERT INTO dt_format_smoke VALUES($1::integer,TIMESTAMP '2006-10-19 15:12:02.021230',TIMESTAMPTZ '2006-10-19 15:12:02.021230+00',INTERVAL '36 hours 2 minutes 12 seconds',to_date($2::text,$3::text))"
		insert, err := conn.PrepareContext(p.ctx, insertSQL)
		if err != nil {
			t.Fatal(err)
		}
		defer insert.Close()
		run(referenceExec(insertSQL, 1, int64(1), "05 Dec 2000", "DD Mon YYYY"))
		referenceRunStep(t, p.ctx, referenceHeldStatement{referenceExecutor: conn, statement: insert, query: insertSQL}, b.recorder, referenceExec(insertSQL, 1, int64(2), "05 Dec 2000", "DD Mon YYYY"))
		q := "SELECT id,to_char(ts,$1::text) AS ts,to_char(z,$1::text) AS z,to_char(iv,'HH24:MI:SS') AS iv,to_date('05 Dec 2000','DD Mon YYYY') AS d,to_timestamp($2::text,$3::text) AS instant,to_char(ts,'FMMonth DDTH') AS name,to_char(ts,'IYYY-IW-ID') AS iso FROM dt_format_smoke ORDER BY id"
		selectStmt, err := conn.PrepareContext(p.ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer selectStmt.Close()
		date := any("2000-12-05")
		if b.pg {
			date = time.Date(2000, 12, 5, 0, 0, 0, 0, time.UTC)
		}
		instant := time.Date(2006, 10, 19, 9, 27, 2, 21230000, time.UTC)
		columns := []string{"id", "ts", "z", "iv", "d", "instant", "name", "iso"}
		types := []string{"INT4", "TEXT", "TEXT", "TEXT", "DATE", "TIMESTAMPTZ", "TEXT", "TEXT"}
		want := [][]any{{int64(1), "2006-10-19T15:12:02.021230 +00", "2006-10-19T15:12:02.021230 +00", "36:02:12", date, instant, "October 19TH", "2006-42-4"}, {int64(2), "2006-10-19T15:12:02.021230 +00", "2006-10-19T15:12:02.021230 +00", "36:02:12", date, instant, "October 19TH", "2006-42-4"}}
		args := []any{`YYYY-MM-DD"T"HH24:MI:SS.US OF`, "2006-10-19 15:12:02.021230 +05:45", "YYYY-MM-DD HH24:MI:SS.US TZH:TZM"}
		for _, held := range []bool{false, true} {
			executor := referenceExecutor(conn)
			if held {
				executor = referenceHeldStatement{referenceExecutor: conn, statement: selectStmt, query: q}
			}
			referenceRunStep(t, p.ctx, executor, b.recorder, referenceSelect(q, columns, types, want, args...))
		}
		run(referenceExec("SET TIME ZONE 'America/New_York'", 0))
		ny := [][]any{{int64(1), "2006-10-19T15:12:02.021230 +00", "2006-10-19T11:12:02.021230 -04", "36:02:12", date, instant, "October 19TH", "2006-42-4"}, {int64(2), "2006-10-19T15:12:02.021230 +00", "2006-10-19T11:12:02.021230 -04", "36:02:12", date, instant, "October 19TH", "2006-42-4"}}
		referenceRunStep(t, p.ctx, referenceHeldStatement{referenceExecutor: conn, statement: selectStmt, query: q}, b.recorder, referenceSelect(q, columns, types, ny, args...))
		run(referenceSelect("SELECT to_char(z,'HH24:MI:SS.US TZ OF') AS result FROM dt_format_smoke WHERE id=1", []string{"result"}, []string{"TEXT"}, [][]any{{"11:12:02.021230 EDT -04"}}))
		run(referenceExec("SET TIME ZONE 'UTC'", 0))
		for _, prepared := range []bool{false, true} {
			s := referenceSelect("SELECT to_date('bad',NULL::text) AS d,to_timestamp(NULL::text,'bad') AS z,to_char(NULL::interval,'MONTH') AS iv", []string{"d", "z", "iv"}, []string{"DATE", "TIMESTAMPTZ", "TEXT"}, [][]any{{nil, nil, nil}})
			if prepared {
				s = referencePrepared(s)
			}
			run(s)
			d := *datetimeFormattingDiagnostic("22007", "invalid combination of date conventions", "", "Do not mix Gregorian and ISO week date conventions in a formatting template.")
			s = referenceErrorDiagnostic("SELECT to_date('2006 42','YYYY IW')", d)
			if prepared {
				s = referencePrepared(s)
			}
			run(s)
			d = *datetimeFormattingDiagnostic("22008", `date/time field value out of range: "2006-02-29"`, "", "")
			s = referenceErrorDiagnostic("INSERT INTO dt_format_smoke(id,d) VALUES(3,to_date('2000-02-29','YYYY-MM-DD')),(4,to_date('2006-02-29','YYYY-MM-DD'))", d)
			if prepared {
				s = referencePrepared(s)
			}
			run(s)
		}
		run(referenceExec("CREATE VIEW dt_format_smoke_view AS SELECT id,to_char(ts,'IYYY-IW-ID') AS iso FROM dt_format_smoke", 0))
		view := referenceSelect("SELECT id,iso FROM dt_format_smoke_view ORDER BY id", []string{"id", "iso"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "2006-42-4"}, {int64(2), "2006-42-4"}})
		run(view)
		if b.pg {
			return
		}
		var dump bytes.Buffer
		if err := stuntgres.Dump(p.ctx, p.native, &dump); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Reset(p.ctx, p.native); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Restore(p.ctx, p.native, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}
		run(view)
		referenceRunStep(t, p.ctx, referenceHeldStatement{referenceExecutor: conn, statement: selectStmt, query: q}, b.recorder, referenceSelect(q, columns, types, want, args...))
	})
}
