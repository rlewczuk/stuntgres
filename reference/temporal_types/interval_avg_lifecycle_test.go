package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"reflect"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceIntervalAvgLifecycle(t *testing.T) {
	intervalAvgScenario(t, func(t *testing.T, p *fixture.Pair, conn *sql.Conn, recorder *fixture.Recorder, pg bool) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, recorder, s) }
		run(fixture.Exec("SET IntervalStyle = 'postgres'", 0))
		run(fixture.Exec("CREATE TABLE t(v INTERVAL)", 0))
		run(fixture.Exec("INSERT INTO t VALUES ('1 day'),('3 days')", 2))
		q := "SELECT avg(v) FROM t"
		stmt, err := conn.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if err := stmt.Close(); err != nil {
				t.Error(err)
			}
		})
		held := fixture.HeldStatement{Executor: conn, Statement: stmt, SQL: q}
		check := func(want any) {
			fixture.RunStep(t, p.Ctx, held, recorder, fixture.Select(q, []string{"avg"}, []string{"INTERVAL"}, [][]any{{want}}))
		}
		check("2 days")
		run(fixture.Exec("INSERT INTO t VALUES ('8 days'),(NULL)", 2))
		check("4 days")
		run(fixture.Exec("DELETE FROM t WHERE v=INTERVAL '8 days'", 1))
		check("2 days")
		run(fixture.Exec("DELETE FROM t", 3))
		check(nil)
		run(fixture.Exec("INSERT INTO t VALUES ('1 day'),('3 days')", 2))
		check("2 days")
		// Destination typmod, not source typmod, rounds the final unrestricted mean.
		run(fixture.Exec("CREATE TABLE precise(v INTERVAL SECOND(0))", 0))
		run(fixture.Exec("INSERT INTO precise VALUES ('1 second'),('0')", 2))
		run(fixture.Exec("CREATE TABLE rounded(v INTERVAL SECOND(0))", 0))
		for _, prepared := range []bool{false, true} {
			s := fixture.Exec("INSERT INTO rounded SELECT avg(v) FROM precise", 1)
			s.Prepared = prepared
			run(s)
		}
		run(fixture.Select("SELECT v FROM rounded", []string{"v"}, []string{"INTERVAL"}, [][]any{{"00:00:01"}, {"00:00:01"}}))
		run(fixture.Exec("CREATE TABLE grouped(g INTEGER,v INTERVAL)", 0))
		run(fixture.Exec("INSERT INTO grouped VALUES (1,'1 day'),(1,'3 days'),(2,NULL)", 3))
		run(fixture.Exec("CREATE TABLE target(g INTEGER,v INTERVAL)", 0))
		for _, prepared := range []bool{false, true} {
			s := fixture.Exec("INSERT INTO target SELECT g,avg(v) FROM grouped GROUP BY g", 2)
			s.Prepared = prepared
			run(s)
		}
		s := fixture.Select("SELECT g,v FROM target", []string{"g", "v"}, []string{"INT4", "INTERVAL"}, [][]any{{int64(1), "2 days"}, {int64(1), "2 days"}, {int64(2), nil}, {int64(2), nil}})
		s.Bag = true
		run(s)
		run(fixture.Exec("CREATE VIEW avg_view AS SELECT avg(v) AS mean FROM t", 0))
		intervalAvgBoth(t, p, conn, recorder, fixture.Select("SELECT mean FROM avg_view", []string{"mean"}, []string{"INTERVAL"}, [][]any{{"2 days"}}))
		// Literal meaning freezes at Prepare, whereas textual parameters read Execute settings.
		run(fixture.Exec("SET IntervalStyle = 'postgres'", 0))
		literalQuery := "SELECT avg(INTERVAL '-1 2:03:04') FROM t"
		literal, err := conn.PrepareContext(p.Ctx, literalQuery)
		if err != nil {
			t.Fatal(err)
		}
		defer literal.Close()
		parameterQuery := "SELECT avg($1::interval) FROM t"
		parameter, err := conn.PrepareContext(p.Ctx, parameterQuery)
		if err != nil {
			t.Fatal(err)
		}
		defer parameter.Close()
		run(fixture.Exec("SET IntervalStyle = 'sql_standard'", 0))
		fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: conn, Statement: literal, SQL: literalQuery}, recorder, fixture.Select(literalQuery, []string{"avg"}, []string{"INTERVAL"}, [][]any{{"+0-0 -1 +2:03:04"}}))
		fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: conn, Statement: parameter, SQL: parameterQuery}, recorder, fixture.Select(parameterQuery, []string{"avg"}, []string{"INTERVAL"}, [][]any{{"-1 2:03:04"}}, "-1 2:03:04"))
		run(fixture.Exec("SET IntervalStyle = 'postgres'", 0))
		// Shared renderer styles are asserted independently, not compared with a native golden.
		run(fixture.Exec("DELETE FROM t", 2))
		run(fixture.Exec("INSERT INTO t VALUES ('2 mons 4 days 6 hours'),('0')", 2))
		styles := []struct{ name, mean, zero string }{
			{"postgres", "1 mon 2 days 03:00:00", "00:00:00"},
			{"postgres_verbose", "@ 1 mon 2 days 3 hours", "@ 0"},
			{"sql_standard", "+0-1 +2 +3:00:00", "0"},
			{"iso_8601", "P1M2DT3H", "PT0S"},
		}
		for _, style := range styles {
			t.Run(style.name, func(t *testing.T) {
				run(fixture.Exec("SET IntervalStyle = '"+style.name+"'", 0))
				intervalAvgBoth(t, p, conn, recorder, fixture.Select(q, []string{"avg"}, []string{"INTERVAL"}, [][]any{{style.mean}}))
				intervalAvgBoth(t, p, conn, recorder, fixture.Select("SELECT avg(INTERVAL '0') FROM t", []string{"avg"}, []string{"INTERVAL"}, [][]any{{style.zero}}))
				intervalAvgBoth(t, p, conn, recorder, fixture.Select("SELECT avg(INTERVAL 'infinity') FROM t", []string{"avg"}, []string{"INTERVAL"}, [][]any{{"infinity"}}))
			})
		}
		run(fixture.Exec("SET IntervalStyle = 'postgres'", 0))
		rows, err := conn.QueryContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if err := rows.Close(); err != nil {
				t.Error(err)
			}
		})
		columns, err := rows.Columns()
		if err != nil {
			t.Fatal(err)
		}
		peer, err := connPeerIntervalAvg(p, pg)
		if err != nil {
			t.Fatal(err)
		}
		defer peer.Close()
		if _, err := peer.ExecContext(p.Ctx, "SET IntervalStyle = 'sql_standard'"); err != nil {
			t.Fatal(err)
		}
		if !pg {
			// Native eager rows permit same-session changes while held. lib/pq
			// owns its session until rows close, so PostgreSQL uses the peer only.
			run(fixture.Exec("SET IntervalStyle = 'iso_8601'", 0))
			var snapshot bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &snapshot); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(snapshot.Bytes())); err != nil {
				t.Fatal(err)
			}
		}
		if !rows.Next() {
			t.Fatalf("retained AVG row missing: %v", rows.Err())
		}
		var value any
		if err := rows.Scan(&value); err != nil {
			t.Fatal(err)
		}
		if got := fixture.Normalize(value); got != "1 mon 2 days 03:00:00" {
			t.Errorf("retained output: %v", got)
		}
		if got, err := rows.Columns(); err != nil || !reflect.DeepEqual(got, columns) {
			t.Errorf("retained columns: %v/%v", got, err)
		}
		if rows.Next() {
			t.Error("extra retained row")
		}
		if err := rows.Err(); err != nil {
			t.Error(err)
		}
		if err := rows.Close(); err != nil {
			t.Fatal(err)
		}
		run(fixture.Exec("SET IntervalStyle = 'postgres'", 0))
		check("1 mon 2 days 03:00:00")
		intervalAvgBoth(t, p, conn, recorder, fixture.Select("SELECT mean FROM avg_view", []string{"mean"}, []string{"INTERVAL"}, [][]any{{"1 mon 2 days 03:00:00"}}))
		intervalAvgBoth(t, p, conn, recorder, fixture.Select("SELECT avg(v) FROM precise", []string{"avg"}, []string{"INTERVAL"}, [][]any{{"00:00:00.5"}}))
		if !pg {
			// Native eager publication checks cancellation before returning rows.
			// lib/pq cancellation belongs to its streaming adapter contract.
			canceled, cancel := context.WithCancel(p.Ctx)
			cancel()
			canceledRows, err := conn.QueryContext(canceled, q)
			if canceledRows != nil {
				canceledRows.Close()
				t.Error("pre-canceled query published rows")
			}
			if !errors.Is(err, context.Canceled) {
				t.Errorf("pre-canceled error: %v", err)
			}
			if _, ok := err.(interface{ SQLState() string }); ok {
				t.Errorf("ordinary cancellation gained SQLSTATE: %v", err)
			}
		}
	})
}

func connPeerIntervalAvg(p *fixture.Pair, pg bool) (*sql.Conn, error) {
	if pg {
		return p.Postgres.Conn(p.Ctx)
	}
	return p.Memory.Conn(p.Ctx)
}
