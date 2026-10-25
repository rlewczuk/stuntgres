package reference_test

import (
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func TestReferenceTimestamptzRangeEnvironments(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		conn, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, b.Recorder, s) }
		run(fixture.Exec("SET TIME ZONE 'UTC'", 0))
		run(fixture.Exec("SET DateStyle = 'ISO, MDY'", 0))
		run(fixture.Exec("CREATE TABLE r(k integer NOT NULL,v tstzrange)", 0))
		q := "INSERT INTO r VALUES ($1,'[01/02/2003,01/03/2003)')"
		stmt, err := conn.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		bindQ := "INSERT INTO r VALUES ($1,$2)"
		bind, err := conn.PrepareContext(p.Ctx, bindQ)
		if err != nil {
			t.Fatal(err)
		}
		defer bind.Close()
		run(fixture.Exec("SET TIME ZONE 'America/New_York'", 0))
		run(fixture.Exec("SET DateStyle = 'ISO, DMY'", 0))
		fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: conn, Statement: stmt, SQL: q}, b.Recorder, fixture.Exec(q, 1, 1))
		fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: conn, Statement: bind, SQL: bindQ}, b.Recorder, fixture.Exec(bindQ, 1, 2, "[01/02/2003,01/03/2003)"))
		check := rangeSelect(b, "SELECT k,v FROM r ORDER BY k", []string{"k", "v"}, []string{"INT4", "TSTZRANGE"}, [][]any{
			{int64(1), `["2003-01-01 19:00:00-05","2003-01-02 19:00:00-05")`},
			{int64(2), `["2003-02-01 00:00:00-05","2003-03-01 00:00:00-05")`},
		}, []bool{false, true})
		run(check)
		run(fixture.Prepared(check))
		rows, err := conn.QueryContext(p.Ctx, check.Query)
		if err != nil {
			t.Fatal(err)
		}
		if !b.PG {
			run(fixture.Exec("SET TIME ZONE 'UTC'", 0))
			run(fixture.Exec("SET DateStyle = 'German, DMY'", 0))
		}
		fixture.RangeConsumeHeld(t, rows, check)
		run(fixture.Exec("SET TIME ZONE 'UTC'", 0))
		run(fixture.Exec("SET DateStyle = 'ISO, MDY'", 0))
		for _, tc := range []struct{ input, output string }{
			{"[2018-03-11 02:30 America/New_York,2018-03-11 03:30 America/New_York)", "empty"},
			{"[2018-03-11 02:30 America/New_York,2018-03-11 03:30 America/New_York]", `["2018-03-11 07:30:00+00","2018-03-11 07:30:00+00"]`},
			{"[2018-11-04 01:30 EDT,2018-11-04 01:30 EST)", `["2018-11-04 05:30:00+00","2018-11-04 06:30:00+00")`},
			{"[2020-07-01 12:00 America/New_York,2020-07-01 12:00 EST)", `["2020-07-01 16:00:00+00","2020-07-01 17:00:00+00")`},
			{"[2000-01-01 00:00:00+00:00:30,2000-01-01 00:00:00+00]", `["1999-12-31 23:59:30+00","2000-01-01 00:00:00+00"]`},
		} {
			run(fixture.Exec("DELETE FROM r", 2))
			run(fixture.Exec("INSERT INTO r VALUES (1,"+rangeLiteral(tc.input)+"),(2,"+rangeLiteral(tc.input)+")", 2))
			run(rangeSelect(b, "SELECT v FROM r ORDER BY k", []string{"v"}, []string{"TSTZRANGE"}, [][]any{{tc.output}, {tc.output}}, []bool{true}))
		}
		run(fixture.Exec("DELETE FROM r", 2))
		run(fixture.Exec("INSERT INTO r VALUES (1,'[2003-01-02 00:00+00,2003-01-03 00:00+00)')", 1))
		for _, tc := range []struct{ style, output string }{
			{"ISO, DMY", `["2003-01-02 00:00:00+00","2003-01-03 00:00:00+00")`},
			{"SQL, DMY", `["02/01/2003 00:00:00 UTC","03/01/2003 00:00:00 UTC")`},
			{"German, DMY", `["02.01.2003 00:00:00 UTC","03.01.2003 00:00:00 UTC")`},
			{"Postgres, DMY", `["Thu 02 Jan 00:00:00 2003 UTC","Fri 03 Jan 00:00:00 2003 UTC")`},
		} {
			run(fixture.Exec("SET DateStyle = '"+tc.style+"'", 0))
			run(rangeSelect(b, "SELECT v FROM r", []string{"v"}, []string{"TSTZRANGE"}, [][]any{{tc.output}}, []bool{true}))
		}
	}
}

func TestReferenceTimestamptzRangeRelativeCapture(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		conn, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, b.Recorder, s) }
		run(fixture.Exec("SET TIME ZONE 'UTC'", 0))
		run(fixture.Exec("SET DateStyle = 'ISO, MDY'", 0))
		run(fixture.Exec("CREATE TABLE r(k integer NOT NULL,v tstzrange)", 0))
		run(fixture.Exec("CREATE TABLE scalar_days(k integer,v timestamptz)", 0))
		tx, err := conn.BeginTx(p.Ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		// A scalar transaction clock is the independently owned baseline, never a native range golden.
		var clock time.Time
		if err := tx.QueryRowContext(p.Ctx, "SELECT CURRENT_TIMESTAMP").Scan(&clock); err != nil {
			t.Fatal(err)
		}
		instant := clock.UTC().Format("2006-01-02 15:04:05.999999") + "+00"
		day := clock.UTC().Format("2006-01-02") + " 00:00:00+00"
		next := clock.UTC().AddDate(0, 0, 1).Format("2006-01-02") + " 00:00:00+00"
		yesterday := clock.UTC().AddDate(0, 0, -1).Format("2006-01-02") + " 00:00:00+00"
		q := "INSERT INTO r VALUES ($1,'[now,now]')"
		stmt, err := tx.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: tx, Statement: stmt, SQL: q}
		fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Exec(q, 1, 1))
		fixture.RunStep(t, p.Ctx, tx, b.Recorder, fixture.Prepared(fixture.Exec("INSERT INTO r VALUES ($1,$2)", 1, 2, "[today,tomorrow)")))
		fixture.RunStep(t, p.Ctx, tx, b.Recorder, fixture.Exec("INSERT INTO r VALUES (5,'[yesterday,today]')", 1))
		fixture.RunStep(t, p.Ctx, tx, b.Recorder, fixture.Exec("SET TIME ZONE 'America/New_York'", 0))
		fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Exec(q, 1, 3))
		fixture.RunStep(t, p.Ctx, tx, b.Recorder, fixture.Prepared(fixture.Exec("INSERT INTO r VALUES ($1,$2)", 1, 4, "[now,now]")))
		// The scalar provider owns local-midnight/DST semantics independently of ranges.
		fixture.RunStep(t, p.Ctx, tx, b.Recorder, fixture.Exec("INSERT INTO scalar_days VALUES (1,'today'),(2,'tomorrow')", 2))
		var localDay, localNext time.Time
		if err := tx.QueryRowContext(p.Ctx, "SELECT v FROM scalar_days WHERE k=1").Scan(&localDay); err != nil {
			t.Fatal(err)
		}
		if err := tx.QueryRowContext(p.Ctx, "SELECT v FROM scalar_days WHERE k=2").Scan(&localNext); err != nil {
			t.Fatal(err)
		}
		fixture.RunStep(t, p.Ctx, tx, b.Recorder, fixture.Prepared(fixture.Exec("INSERT INTO r VALUES ($1,$2)", 1, 6, "[today,tomorrow)")))
		fixture.RunStep(t, p.Ctx, tx, b.Recorder, fixture.Exec("SET TIME ZONE 'UTC'", 0))
		singleton := fmt.Sprintf(`["%s","%s"]`, instant, instant)
		fixture.RunStep(t, p.Ctx, tx, b.Recorder, rangeSelect(b, "SELECT k,v FROM r ORDER BY k", []string{"k", "v"}, []string{"INT4", "TSTZRANGE"}, [][]any{{int64(1), singleton}, {int64(2), fmt.Sprintf(`["%s","%s")`, day, next)}, {int64(3), singleton}, {int64(4), singleton}, {int64(5), fmt.Sprintf(`["%s","%s"]`, yesterday, day)}, {int64(6), fmt.Sprintf(`["%s+00","%s+00")`, localDay.UTC().Format("2006-01-02 15:04:05"), localNext.UTC().Format("2006-01-02 15:04:05"))}}, []bool{false, true}))
		if err := stmt.Close(); err != nil {
			t.Fatal(err)
		}
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
	}
}
