package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"reflect"
	"testing"

	"stuntgres"
)

func TestReferenceIntervalAvgLifecycle(t *testing.T) {
	intervalAvgScenario(t, func(t *testing.T, p *referencePair, conn *sql.Conn, recorder *referenceRecorder, pg bool) {
		run := func(s referenceStep) { referenceRunStep(t, p.ctx, conn, recorder, s) }
		run(referenceExec("SET IntervalStyle = 'postgres'", 0))
		run(referenceExec("CREATE TABLE t(v INTERVAL)", 0))
		run(referenceExec("INSERT INTO t VALUES ('1 day'),('3 days')", 2))
		q := "SELECT avg(v) FROM t"
		stmt, err := conn.PrepareContext(p.ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if err := stmt.Close(); err != nil {
				t.Error(err)
			}
		})
		held := referenceHeldStatement{referenceExecutor: conn, statement: stmt, query: q}
		check := func(want any) {
			referenceRunStep(t, p.ctx, held, recorder, referenceSelect(q, []string{"avg"}, []string{"INTERVAL"}, [][]any{{want}}))
		}
		check("2 days")
		run(referenceExec("INSERT INTO t VALUES ('8 days'),(NULL)", 2))
		check("4 days")
		run(referenceExec("DELETE FROM t WHERE v=INTERVAL '8 days'", 1))
		check("2 days")
		run(referenceExec("DELETE FROM t", 3))
		check(nil)
		run(referenceExec("INSERT INTO t VALUES ('1 day'),('3 days')", 2))
		check("2 days")
		// Destination typmod, not source typmod, rounds the final unrestricted mean.
		run(referenceExec("CREATE TABLE precise(v INTERVAL SECOND(0))", 0))
		run(referenceExec("INSERT INTO precise VALUES ('1 second'),('0')", 2))
		run(referenceExec("CREATE TABLE rounded(v INTERVAL SECOND(0))", 0))
		for _, prepared := range []bool{false, true} {
			s := referenceExec("INSERT INTO rounded SELECT avg(v) FROM precise", 1)
			s.prepared = prepared
			run(s)
		}
		run(referenceSelect("SELECT v FROM rounded", []string{"v"}, []string{"INTERVAL"}, [][]any{{"00:00:01"}, {"00:00:01"}}))
		run(referenceExec("CREATE TABLE grouped(g INTEGER,v INTERVAL)", 0))
		run(referenceExec("INSERT INTO grouped VALUES (1,'1 day'),(1,'3 days'),(2,NULL)", 3))
		run(referenceExec("CREATE TABLE target(g INTEGER,v INTERVAL)", 0))
		for _, prepared := range []bool{false, true} {
			s := referenceExec("INSERT INTO target SELECT g,avg(v) FROM grouped GROUP BY g", 2)
			s.prepared = prepared
			run(s)
		}
		s := referenceSelect("SELECT g,v FROM target", []string{"g", "v"}, []string{"INT4", "INTERVAL"}, [][]any{{int64(1), "2 days"}, {int64(1), "2 days"}, {int64(2), nil}, {int64(2), nil}})
		s.bag = true
		run(s)
		run(referenceExec("CREATE VIEW avg_view AS SELECT avg(v) AS mean FROM t", 0))
		intervalAvgBoth(t, p, conn, recorder, referenceSelect("SELECT mean FROM avg_view", []string{"mean"}, []string{"INTERVAL"}, [][]any{{"2 days"}}))
		// Literal meaning freezes at Prepare, whereas textual parameters read Execute settings.
		run(referenceExec("SET IntervalStyle = 'postgres'", 0))
		literalQuery := "SELECT avg(INTERVAL '-1 2:03:04') FROM t"
		literal, err := conn.PrepareContext(p.ctx, literalQuery)
		if err != nil {
			t.Fatal(err)
		}
		defer literal.Close()
		parameterQuery := "SELECT avg($1::interval) FROM t"
		parameter, err := conn.PrepareContext(p.ctx, parameterQuery)
		if err != nil {
			t.Fatal(err)
		}
		defer parameter.Close()
		run(referenceExec("SET IntervalStyle = 'sql_standard'", 0))
		referenceRunStep(t, p.ctx, referenceHeldStatement{referenceExecutor: conn, statement: literal, query: literalQuery}, recorder, referenceSelect(literalQuery, []string{"avg"}, []string{"INTERVAL"}, [][]any{{"+0-0 -1 +2:03:04"}}))
		referenceRunStep(t, p.ctx, referenceHeldStatement{referenceExecutor: conn, statement: parameter, query: parameterQuery}, recorder, referenceSelect(parameterQuery, []string{"avg"}, []string{"INTERVAL"}, [][]any{{"-1 2:03:04"}}, "-1 2:03:04"))
		run(referenceExec("SET IntervalStyle = 'postgres'", 0))
		// Shared renderer styles are asserted independently, not compared with a native golden.
		run(referenceExec("DELETE FROM t", 2))
		run(referenceExec("INSERT INTO t VALUES ('2 mons 4 days 6 hours'),('0')", 2))
		styles := []struct{ name, mean, zero string }{
			{"postgres", "1 mon 2 days 03:00:00", "00:00:00"},
			{"postgres_verbose", "@ 1 mon 2 days 3 hours", "@ 0"},
			{"sql_standard", "+0-1 +2 +3:00:00", "0"},
			{"iso_8601", "P1M2DT3H", "PT0S"},
		}
		for _, style := range styles {
			t.Run(style.name, func(t *testing.T) {
				run(referenceExec("SET IntervalStyle = '"+style.name+"'", 0))
				intervalAvgBoth(t, p, conn, recorder, referenceSelect(q, []string{"avg"}, []string{"INTERVAL"}, [][]any{{style.mean}}))
				intervalAvgBoth(t, p, conn, recorder, referenceSelect("SELECT avg(INTERVAL '0') FROM t", []string{"avg"}, []string{"INTERVAL"}, [][]any{{style.zero}}))
				intervalAvgBoth(t, p, conn, recorder, referenceSelect("SELECT avg(INTERVAL 'infinity') FROM t", []string{"avg"}, []string{"INTERVAL"}, [][]any{{"infinity"}}))
			})
		}
		run(referenceExec("SET IntervalStyle = 'postgres'", 0))
		rows, err := conn.QueryContext(p.ctx, q)
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
		if _, err := peer.ExecContext(p.ctx, "SET IntervalStyle = 'sql_standard'"); err != nil {
			t.Fatal(err)
		}
		if !pg {
			// Native eager rows permit same-session changes while held. lib/pq
			// owns its session until rows close, so PostgreSQL uses the peer only.
			run(referenceExec("SET IntervalStyle = 'iso_8601'", 0))
			var snapshot bytes.Buffer
			if err := stuntgres.Dump(p.ctx, p.native, &snapshot); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Reset(p.ctx, p.native); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(p.ctx, p.native, bytes.NewReader(snapshot.Bytes())); err != nil {
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
		if got := referenceNormalize(value); got != "1 mon 2 days 03:00:00" {
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
		run(referenceExec("SET IntervalStyle = 'postgres'", 0))
		check("1 mon 2 days 03:00:00")
		intervalAvgBoth(t, p, conn, recorder, referenceSelect("SELECT mean FROM avg_view", []string{"mean"}, []string{"INTERVAL"}, [][]any{{"1 mon 2 days 03:00:00"}}))
		intervalAvgBoth(t, p, conn, recorder, referenceSelect("SELECT avg(v) FROM precise", []string{"avg"}, []string{"INTERVAL"}, [][]any{{"00:00:00.5"}}))
		if !pg {
			// Native eager publication checks cancellation before returning rows.
			// lib/pq cancellation belongs to its streaming adapter contract.
			canceled, cancel := context.WithCancel(p.ctx)
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

func connPeerIntervalAvg(p *referencePair, pg bool) (*sql.Conn, error) {
	if pg {
		return p.postgres.Conn(p.ctx)
	}
	return p.memory.Conn(p.ctx)
}
