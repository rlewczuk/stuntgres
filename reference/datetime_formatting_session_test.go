package reference_test

import (
	"database/sql"
	"testing"
	"time"
)

func TestReferenceDatetimeFormattingTimezoneCapture(t *testing.T) {
	datetimeFormattingScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, conn *sql.Conn) {
		run := func(s referenceStep) { referenceRunStep(t, p.ctx, conn, b.recorder, s) }
		formatQuery := "SELECT to_char($1::timestamptz,$2::text) AS result"
		parseQuery := "SELECT to_timestamp($1::text,$2::text) AS result"
		format, err := conn.PrepareContext(p.ctx, formatQuery)
		if err != nil {
			t.Fatal(err)
		}
		defer format.Close()
		parse, err := conn.PrepareContext(p.ctx, parseQuery)
		if err != nil {
			t.Fatal(err)
		}
		defer parse.Close()
		for _, zone := range []struct {
			name, want string
			instant    time.Time
		}{
			{"UTC", "2004-07-01 16:00 UTC +00", time.Date(2004, 7, 1, 12, 0, 0, 0, time.UTC)},
			{"America/New_York", "2004-07-01 12:00 EDT -04", time.Date(2004, 7, 1, 16, 0, 0, 0, time.UTC)},
			{"Asia/Kathmandu", "2004-07-01 21:45 +0545 +05:45", time.Date(2004, 7, 1, 6, 15, 0, 0, time.UTC)},
		} {
			run(referenceExec("SET TIME ZONE "+datetimeFormattingQuote(zone.name), 0))
			for _, held := range []bool{false, true} {
				executor := referenceExecutor(conn)
				if held {
					executor = referenceHeldStatement{referenceExecutor: conn, statement: format, query: formatQuery}
				}
				referenceRunStep(t, p.ctx, executor, b.recorder, referenceSelect(formatQuery, []string{"result"}, []string{"TEXT"}, [][]any{{zone.want}}, "2004-07-01 16:00+00", "YYYY-MM-DD HH24:MI TZ OF"))
				executor = conn
				if held {
					executor = referenceHeldStatement{referenceExecutor: conn, statement: parse, query: parseQuery}
				}
				referenceRunStep(t, p.ctx, executor, b.recorder, referenceSelect(parseQuery, []string{"result"}, []string{"TIMESTAMPTZ"}, [][]any{{zone.instant}}, "2004-07-01 12:00", "YYYY-MM-DD HH24:MI"))
				referenceRunStep(t, p.ctx, executor, b.recorder, referenceSelect(parseQuery, []string{"result"}, []string{"TIMESTAMPTZ"}, [][]any{{time.Date(2004, 7, 1, 6, 15, 0, 0, time.UTC)}}, "2004-07-01 12:00 +05:45", "YYYY-MM-DD HH24:MI TZH:TZM"))
			}
		}
		run(referenceExec("SET TIME ZONE 'America/New_York'", 0))
		for _, c := range []struct {
			input string
			want  time.Time
		}{
			{"2018-03-11 02:30", time.Date(2018, 3, 11, 7, 30, 0, 0, time.UTC)},
			{"2018-11-04 01:30", time.Date(2018, 11, 4, 6, 30, 0, 0, time.UTC)},
		} {
			referenceRunStep(t, p.ctx, referenceHeldStatement{referenceExecutor: conn, statement: parse, query: parseQuery}, b.recorder, referenceSelect(parseQuery, []string{"result"}, []string{"TIMESTAMPTZ"}, [][]any{{c.want}}, c.input, "YYYY-MM-DD HH24:MI"))
		}
		run(referenceExec("SET TIME ZONE 'UTC'", 0))
		tx, err := conn.BeginTx(p.ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		referenceRunStep(t, p.ctx, tx, b.recorder, referenceExec("SET LOCAL TIME ZONE 'America/New_York'", 0))
		referenceRunStep(t, p.ctx, tx, b.recorder, referenceExec("SAVEPOINT dt_zone", 0))
		referenceRunStep(t, p.ctx, tx, b.recorder, referenceExec("SET LOCAL TIME ZONE 'Asia/Kathmandu'", 0))
		q := "SELECT to_char(TIMESTAMPTZ '2004-07-01 16:00+00','HH24:MI OF') AS result"
		referenceRunStep(t, p.ctx, tx, b.recorder, referenceSelect(q, []string{"result"}, []string{"TEXT"}, [][]any{{"21:45 +05:45"}}))
		referenceRunStep(t, p.ctx, tx, b.recorder, referenceExec("ROLLBACK TO dt_zone", 0))
		referenceRunStep(t, p.ctx, tx, b.recorder, referenceSelect(q, []string{"result"}, []string{"TEXT"}, [][]any{{"12:00 -04"}}))
		peer, err := b.db.Conn(p.ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer peer.Close()
		referenceRunStep(t, p.ctx, peer, b.recorder, referenceExec("SET TIME ZONE 'UTC'", 0))
		referenceRunStep(t, p.ctx, peer, b.recorder, referenceSelect(q, []string{"result"}, []string{"TEXT"}, [][]any{{"16:00 +00"}}))
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
		run(referenceSelect(q, []string{"result"}, []string{"TEXT"}, [][]any{{"16:00 +00"}}))
		for _, style := range []string{"SET DateStyle='SQL,DMY'", "SET DateStyle='German,DMY'", "SET IntervalStyle='sql_standard'", "SET IntervalStyle='iso_8601'"} {
			run(referenceExec(style, 0))
			run(referenceSelect("SELECT to_char(TIMESTAMP '2006-10-19 15:12:02.021230','YYYY-MM-DD HH24:MI:SS.US') AS result", []string{"result"}, []string{"TEXT"}, [][]any{{"2006-10-19 15:12:02.021230"}}))
		}
	})
}

func TestReferenceDatetimeFormattingZoneInput(t *testing.T) {
	var cases []datetimeFormattingCase
	for _, c := range []struct{ input, template string }{
		{"2006-10-19 15:12 EST", "YYYY-MM-DD HH24:MI TZ"}, {"2006-10-19 15:12 est", "YYYY-MM-DD HH24:MI tz"},
		{"2010-01-01 12:00 MSK", "YYYY-MM-DD HH24:MI TZ"}, {"2015-01-01 12:00 MSK", "YYYY-MM-DD HH24:MI TZ"},
		{"1900-01-01 12:00 LMT", "YYYY-MM-DD HH24:MI TZ"}, {"2006-10-19 15:12 bogus", "YYYY-MM-DD HH24:MI TZ"},
		{"2006-10-19 15:12 +05:20", "YYYY-MM-DD HH24:MI OF"}, {"2006-10-19 15:12 -05:20", "YYYY-MM-DD HH24:MI TZH:TZM"},
		{"2006-10-19 15:12 45", "YYYY-MM-DD HH24:MI TZM"}, {"2006-10-19 15:12 +0530", "YYYY-MM-DD HH24:MI TZ"},
		{"2006-10-19 15:12 +16", "YYYY-MM-DD HH24:MI TZH"}, {"2006-10-19 15:12 +05:60", "YYYY-MM-DD HH24:MI OF"},
	} {
		cases = append(cases, datetimeFormattingCase{name: c.input + "/" + c.template, expression: "to_timestamp(" + datetimeFormattingQuote(c.input) + "," + datetimeFormattingQuote(c.template) + ")::text"})
	}
	datetimeFormattingCases(t, cases)
}

func TestReferenceDatetimeFormattingLocaleBound(t *testing.T) {
	datetimeFormattingCases(t, []datetimeFormattingCase{
		{name: "C names", expression: "to_char(TIMESTAMP '2006-10-19','TMMonth TMDay')", want: "October Thursday"},
		{name: "C input", expression: "to_date('05 dEc 2000' COLLATE \"C\",'DD TMMon YYYY')::text", want: "2000-12-05"},
		{name: "POSIX input", expression: "to_date('05 DEC 2000' COLLATE \"POSIX\",'DD TMMon YYYY')::text", want: "2000-12-05"},
		{name: "unicode input", expression: "to_date('05 dec 2000' COLLATE pg_catalog.pg_unicode_fast,'DD TMMon YYYY')::text", want: "2000-12-05"},
	})
}

func TestReferenceDatetimeFormattingHistoricalZoneOutput(t *testing.T) {
	datetimeFormattingScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, conn *sql.Conn) {
		for _, zone := range []struct{ name, want string }{
			{"Europe/Paris", "1900-01-01 12:09:21 PMT pmt +00 09 +00:09"},
			{"America/New_York", "1900-01-01 07:00:00 EST est -05 00 -05"},
		} {
			referenceRunStep(t, p.ctx, conn, b.recorder, referenceExec("SET TIME ZONE "+datetimeFormattingQuote(zone.name), 0))
			for _, prepared := range []bool{false, true} {
				s := referenceSelect("SELECT to_char(TIMESTAMPTZ '1900-01-01 12:00:00+00','YYYY-MM-DD HH24:MI:SS TZ tz TZH TZM OF') AS result", []string{"result"}, []string{"TEXT"}, [][]any{{zone.want}})
				if prepared {
					s = referencePrepared(s)
				}
				referenceRunStep(t, p.ctx, conn, b.recorder, s)
			}
		}
	})
}

func TestReferenceDatetimeFormattingExecutionSnapshot(t *testing.T) {
	datetimeFormattingScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, conn *sql.Conn) {
		q := "SELECT to_char(TIMESTAMPTZ '2004-07-01 16:00+00',$1::text) AS result"
		referenceRunStep(t, p.ctx, conn, b.recorder, referenceSelect(q, []string{"result"}, []string{"TEXT"}, [][]any{{"16:00 UTC +00"}}, "HH24:MI TZ OF"))
		if b.pg {
			return
		} // Native retained cursor contract; pq is streaming.
		stmt, err := conn.PrepareContext(p.ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		for _, held := range []bool{false, true} {
			referenceRunStep(t, p.ctx, conn, b.recorder, referenceExec("SET TIME ZONE 'UTC'", 0))
			var rows *sql.Rows
			if held {
				rows, err = stmt.QueryContext(p.ctx, "HH24:MI TZ OF")
			} else {
				rows, err = conn.QueryContext(p.ctx, q, "HH24:MI TZ OF")
			}
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			referenceRunStep(t, p.ctx, conn, b.recorder, referenceExec("SET TIME ZONE 'America/New_York'", 0))
			if !rows.Next() {
				t.Fatal("missing retained row", rows.Err())
			}
			var value string
			if err := rows.Scan(&value); err != nil {
				t.Fatal(err)
			}
			if value != "16:00 UTC +00" {
				t.Fatal("late live settings read", value)
			}
			if rows.Next() || rows.Err() != nil {
				t.Fatal("retained completion", rows.Err())
			}
			rows.Close()
			referenceRunStep(t, p.ctx, referenceHeldStatement{referenceExecutor: conn, statement: stmt, query: q}, b.recorder, referenceSelect(q, []string{"result"}, []string{"TEXT"}, [][]any{{"12:00 EDT -04"}}, "HH24:MI TZ OF"))
		}
	})
}
