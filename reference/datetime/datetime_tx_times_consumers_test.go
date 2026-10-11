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
	"time"
)

const txTimesStoredDDL = "CREATE TABLE tx_time_stored(n INTEGER NOT NULL,a TIMESTAMPTZ DEFAULT now(),b TIMESTAMPTZ DEFAULT transaction_timestamp(),s TIMESTAMPTZ DEFAULT statement_timestamp(),w TIMESTAMPTZ DEFAULT clock_timestamp(),t TEXT DEFAULT timeofday(),CHECK(a<=clock_timestamp()))"
const txTimesStoredProjection = "a AS now,b AS transaction_timestamp,s AS statement_timestamp,w AS clock_timestamp,t AS timeofday"

func txTimesSymbols(t *testing.T, p *fixture.Pair, c *sql.Conn, pg bool) {
	t.Helper()
	var definition string
	if err := c.QueryRowContext(p.Ctx, "SELECT pg_get_viewdef('tx_time_view'::TEXT)").Scan(&definition); err != nil {
		t.Fatal(err)
	}
	for _, name := range txTimesNames {
		if !strings.Contains(strings.ToLower(definition), name+"()") {
			t.Errorf("view lost %s: %q", name, definition)
		}
	}
	datetimeCurrentExec(t, p.Ctx, c, "CREATE VIEW tx_time_replayed AS "+strings.TrimSuffix(strings.TrimSpace(definition), ";"), 0)
	rows, err := c.QueryContext(p.Ctx, "SELECT d.adnum,pg_get_expr(d.adbin,d.adrelid) FROM pg_attrdef d JOIN pg_class c ON c.oid=d.adrelid WHERE c.relname='tx_time_stored' ORDER BY d.adnum")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	expected := []string{"now()", "transaction_timestamp()", "statement_timestamp()", "clock_timestamp()", "timeofday()"}
	projection := make([]string, 0, len(expected))
	n := 0
	for rows.Next() {
		var ordinal int
		var expr string
		if err := rows.Scan(&ordinal, &expr); err != nil {
			t.Fatal(err)
		}
		if n >= len(expected) || ordinal != n+2 || strings.TrimSpace(expr) != expected[n] {
			t.Fatalf("default[%d] ordinal=%d expression=%q", n, ordinal, expr)
		}
		projection = append(projection, expr+" AS "+txTimesNames[n])
		n++
	}
	if rows.Err() != nil || n != 5 {
		t.Fatal("defaults", n, rows.Err())
	}
	rows.Close()
	txTimesRead(t, p.Ctx, c, pg, "SELECT "+strings.Join(projection, ","), 1, 0, true)
}

func TestReferenceDatetimeTxTimesStoredConsumers(t *testing.T) {
	txTimesScenario(t, func(t *testing.T, p *fixture.Pair, c *sql.Conn, pg bool, r *fixture.Recorder) {
		datetimeCurrentExec(t, p.Ctx, c, txTimesStoredDDL, 0)
		datetimeCurrentExec(t, p.Ctx, c, "CREATE VIEW tx_time_view AS SELECT "+txTimesProjection, 0)
		txTimesSymbols(t, p, c, pg)
		datetimeCurrentExec(t, p.Ctx, c, "BEGIN", 0)
		birth := txTimesRead(t, p.Ctx, c, pg, "SELECT "+txTimesProjection, 1, 0, false)
		before := time.Now()
		datetimeCurrentExec(t, p.Ctx, c, "INSERT INTO tx_time_stored(n) VALUES(1),(2)", 2)
		after := time.Now()
		rows, err := c.QueryContext(p.Ctx, "SELECT a,b,s,w,t FROM tx_time_stored ORDER BY n")
		if err != nil {
			t.Fatal(err)
		}
		var firstStatement time.Time
		n := 0
		for rows.Next() {
			var a, b, s, w time.Time
			var text string
			if err := rows.Scan(&a, &b, &s, &w, &text); err != nil {
				t.Fatal(err)
			}
			if n == 0 {
				firstStatement = s
			}
			if !a.Equal(birth.transaction) || !b.Equal(a) || !s.Equal(firstStatement) || s.Before(before.Add(-time.Microsecond)) || s.After(after.Add(time.Microsecond)) || w.Before(before.Add(-time.Microsecond)) || w.After(after.Add(time.Microsecond)) {
				t.Error("default timing", a, b, s, w, before, after)
			}
			txTimesText(t, text, 0, before, after)
			n++
		}
		if rows.Err() != nil || n != 2 {
			t.Fatal(n, rows.Err())
		}
		rows.Close()
		for _, q := range []string{"SELECT * FROM tx_time_view", "SELECT * FROM tx_time_replayed"} {
			v := txTimesReadStored(t, p.Ctx, c, pg, q, 1, 0, false)
			if !v.transaction.Equal(birth.transaction) {
				t.Error("view frozen at definition")
			}
		}
		datetimeCurrentExec(t, p.Ctx, c, "COMMIT", 0)
		for i, column := range []string{"a", "b", "s", "w", "t"} {
			datetimeCurrentExec(t, p.Ctx, c, "ALTER TABLE tx_time_stored ALTER COLUMN "+column+" SET DEFAULT "+txTimesNames[i]+"()", 0)
		}
		checkInserted := func(n int, before, after time.Time) {
			var a, b, s, w time.Time
			var text string
			if err := c.QueryRowContext(p.Ctx, "SELECT a,b,s,w,t FROM tx_time_stored WHERE n=$1", n).Scan(&a, &b, &s, &w, &text); err != nil {
				t.Fatal(err)
			}
			if !a.Equal(b) || s.Before(a) || (!pg && !s.Equal(a)) {
				t.Error("autocommit default ownership", a, b, s)
			}
			for _, stamp := range []time.Time{a, b, s, w} {
				if stamp.Before(before.Add(-time.Microsecond)) || stamp.After(after.Add(time.Microsecond)) {
					t.Error("stored default outside INSERT command", stamp, before, after)
				}
			}
			txTimesText(t, text, 0, before, after)
		}
		cellBefore := time.Now()
		datetimeCurrentExec(t, p.Ctx, c, "INSERT INTO tx_time_stored VALUES(3,DEFAULT,DEFAULT,DEFAULT,DEFAULT,DEFAULT)", 1)
		cellAfter := time.Now()
		checkInserted(3, cellBefore, cellAfter)
		datetimeCurrentExec(t, p.Ctx, c, "INSERT INTO tx_time_stored(n,a,b,s,w,t) VALUES(4,NULL,NULL,NULL,NULL,NULL)", 1)
		fixture.RunStep(t, p.Ctx, c, r, fixture.Select("SELECT a IS NULL AND b IS NULL AND s IS NULL AND w IS NULL AND t IS NULL AS bypass FROM tx_time_stored WHERE n=4", []string{"bypass"}, []string{"BOOL"}, [][]any{{true}}))
		selectBefore := time.Now()
		datetimeCurrentExec(t, p.Ctx, c, "INSERT INTO tx_time_stored(n) SELECT 5", 1)
		selectAfter := time.Now()
		checkInserted(5, selectBefore, selectAfter)
		updateBefore := time.Now()
		datetimeCurrentExec(t, p.Ctx, c, "UPDATE tx_time_stored SET w=clock_timestamp(),t=timeofday() WHERE n=3", 1)
		updateAfter := time.Now()
		var updatedWall time.Time
		var updatedText string
		if err := c.QueryRowContext(p.Ctx, "SELECT w,t FROM tx_time_stored WHERE n=3").Scan(&updatedWall, &updatedText); err != nil {
			t.Fatal(err)
		}
		if updatedWall.Before(updateBefore.Add(-time.Microsecond)) || updatedWall.After(updateAfter.Add(time.Microsecond)) {
			t.Error("UPDATE wall captured before demand", updatedWall, updateBefore, updateAfter)
		}
		txTimesText(t, updatedText, 0, updateBefore, updateAfter)
		datetimeCurrentExec(t, p.Ctx, c, "ALTER TABLE tx_time_stored ADD COLUMN added TIMESTAMPTZ DEFAULT statement_timestamp()", 0)
		fixture.RunStep(t, p.Ctx, c, r, fixture.Select("SELECT count(DISTINCT added) AS n FROM tx_time_stored", []string{"n"}, []string{"INT8"}, [][]any{{int64(1)}}))
		datetimeCurrentExec(t, p.Ctx, c, "ALTER TABLE tx_time_stored ADD COLUMN wall TEXT DEFAULT lower(timeofday())", 0)
		fixture.RunStep(t, p.Ctx, c, r, fixture.Select("SELECT count(*) AS n FROM tx_time_stored WHERE wall IS NOT NULL", []string{"n"}, []string{"INT8"}, [][]any{{int64(5)}}))
		fixture.State(t, execTxTimesError(p.Ctx, c, "INSERT INTO tx_time_stored(n) VALUES(5),(NULL)"), "23502")
		fixture.RunStep(t, p.Ctx, c, r, fixture.Select("SELECT n FROM tx_time_stored ORDER BY n", []string{"n"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}, {int64(3)}, {int64(4)}, {int64(5)}}))
		datetimeCurrentExec(t, p.Ctx, c, "CREATE TABLE tx_time_wrapped(w TEXT DEFAULT clock_timestamp()::TEXT,same BOOLEAN DEFAULT (clock_timestamp()>=now()),t TEXT DEFAULT CASE WHEN true THEN timeofday() ELSE 'never' END)", 0)
		wrappedBefore := time.Now()
		datetimeCurrentExec(t, p.Ctx, c, "INSERT INTO tx_time_wrapped DEFAULT VALUES", 1)
		wrappedAfter := time.Now()
		var castWall time.Time
		var same bool
		var caseText string
		if err := c.QueryRowContext(p.Ctx, "SELECT w::TIMESTAMPTZ,same,t FROM tx_time_wrapped").Scan(&castWall, &same, &caseText); err != nil {
			t.Fatal(err)
		}
		if !same || castWall.Before(wrappedBefore.Add(-time.Microsecond)) || castWall.After(wrappedAfter.Add(time.Microsecond)) {
			t.Error("closed selected DEFAULT wrappers", same, castWall, wrappedBefore, wrappedAfter)
		}
		txTimesText(t, caseText, 0, wrappedBefore, wrappedAfter)
	})
}

func TestReferenceDatetimeTxTimesImmutability(t *testing.T) {
	txTimesScenario(t, func(t *testing.T, p *fixture.Pair, c *sql.Conn, pg bool, r *fixture.Recorder) {
		datetimeCurrentExec(t, p.Ctx, c, "CREATE TABLE tx_time_index(n INTEGER)", 0)
		for _, name := range txTimesNames {
			typ := "TIMESTAMPTZ"
			if name == "timeofday" {
				typ = "TEXT"
			}
			fixture.RunStep(t, p.Ctx, c, r, fixture.ErrorDiagnostic("CREATE TABLE tx_time_generated(v "+typ+" GENERATED ALWAYS AS ("+name+"()) STORED)", fixture.ControlsReferenceDiagnostic("42P17", "generation expression is not immutable")))
			fixture.RunStep(t, p.Ctx, c, r, fixture.ErrorDiagnostic("CREATE INDEX tx_time_bad_index ON tx_time_index (("+name+"()))", fixture.ControlsReferenceDiagnostic("42P17", "functions in index expression must be marked IMMUTABLE")))
		}
	})
}

func TestReferenceDatetimeTxTimesNativeReplay(t *testing.T) {
	txTimesScenario(t, func(t *testing.T, p *fixture.Pair, c *sql.Conn, pg bool, r *fixture.Recorder) {
		datetimeCurrentExec(t, p.Ctx, c, txTimesStoredDDL, 0)
		datetimeCurrentExec(t, p.Ctx, c, "CREATE VIEW tx_time_view AS SELECT "+txTimesProjection, 0)
		txTimesSymbols(t, p, c, pg)
		datetimeCurrentExec(t, p.Ctx, c, "INSERT INTO tx_time_stored(n) VALUES(1)", 1)
		var original time.Time
		if err := c.QueryRowContext(p.Ctx, "SELECT a FROM tx_time_stored").Scan(&original); err != nil {
			t.Fatal(err)
		}
		if !pg {
			q := "SELECT " + txTimesProjection
			stmt, err := c.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			retainedBefore := time.Now()
			retained, err := c.QueryContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer retained.Close()
			retainedAfter := time.Now()
			retainedMetadata, err := retained.ColumnTypes()
			if err != nil {
				t.Fatal(err)
			}
			datetimeCurrentExec(t, p.Ctx, c, "SET TIME ZONE 5.5", 0)
			var dump bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
				t.Fatal(err)
			}
			for _, name := range txTimesNames {
				if !strings.Contains(dump.String(), name+"()") {
					t.Errorf("dump lost %s", name)
				}
			}
			if p.Postgres != nil {
				replayContext := context.WithValue(p.Ctx, fixture.RecorderKey{}, p.PostgresRecord)
				replay, err := p.Postgres.Conn(replayContext)
				if err != nil {
					t.Fatal(err)
				}
				defer replay.Close()
				defer replay.ExecContext(replayContext, "DROP SCHEMA public CASCADE; CREATE SCHEMA public")
				if _, err := replay.ExecContext(replayContext, dump.String()); err != nil {
					t.Fatal("actual native dump PostgreSQL replay", err, dump.String())
				}
				datetimeCurrentExec(t, replayContext, replay, "SET TIME ZONE 'UTC'", 0)
				var value time.Time
				if err := replay.QueryRowContext(replayContext, "SELECT a FROM tx_time_stored WHERE n=1").Scan(&value); err != nil {
					t.Fatal(err)
				}
				if !value.Equal(original) {
					t.Error("actual dump changed stored instant on PostgreSQL")
				}
				datetimeCurrentExec(t, replayContext, replay, "INSERT INTO tx_time_stored(n) VALUES(2)", 1)
				if err := replay.QueryRowContext(replayContext, "SELECT a FROM tx_time_stored WHERE n=2").Scan(&value); err != nil {
					t.Fatal(err)
				}
				if !value.After(original) {
					t.Error("actual dump froze PostgreSQL future default")
				}
				txTimesReadStored(t, replayContext, replay, true, "SELECT * FROM tx_time_replayed", 1, 0, true)
			}
			if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
				t.Fatal(err)
			}
			afterMetadata, err := retained.ColumnTypes()
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(retainedMetadata, afterMetadata) {
				t.Error("controls changed retained metadata", retainedMetadata, afterMetadata)
			}
			if !retained.Next() {
				t.Fatal("retained row lost", retained.Err())
			}
			var a, b, s, w time.Time
			var text string
			if err := retained.Scan(&a, &b, &s, &w, &text); err != nil {
				t.Fatal(err)
			}
			if !a.Equal(b) || !a.Equal(s) || w.Before(retainedBefore.Add(-time.Microsecond)) || w.After(retainedAfter.Add(time.Microsecond)) {
				t.Error("retained clock row changed")
			}
			for _, stamp := range []time.Time{a, b, s, w} {
				if _, east := stamp.Zone(); east != 0 {
					t.Error("retained timezone changed", stamp)
				}
			}
			txTimesText(t, text, 0, retainedBefore, retainedAfter)
			if retained.Next() || retained.Err() != nil {
				t.Fatal("retained cardinality", retained.Err())
			}
			retained.Close()
			datetimeCurrentExec(t, p.Ctx, c, "SET TIME ZONE 'UTC'", 0)
			before := time.Now()
			txTimesRead(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}, false, q, 1, 0, true)
			fixture.TemporalAdvanceClock(t, p.Ctx, before)
			bad := append(append([]byte(nil), dump.Bytes()...), []byte("\nINSERT INTO tx_time_stored(n) VALUES(NULL);\n")...)
			if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(bad)); err == nil {
				t.Fatal("invalid restore succeeded")
			}
			canceled, cancel := context.WithCancel(p.Ctx)
			cancel()
			for _, x := range []fixture.Executor{c, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}} {
				rows, err := x.QueryContext(canceled, q)
				if rows != nil || !errors.Is(err, context.Canceled) {
					if rows != nil {
						rows.Close()
					}
					t.Fatal("precancel", rows, err)
				}
			}
		}
		var stored time.Time
		if err := c.QueryRowContext(p.Ctx, "SELECT a FROM tx_time_stored WHERE n=1").Scan(&stored); err != nil {
			t.Fatal(err)
		}
		if !stored.Equal(original) {
			t.Error("replay changed retained value")
		}
		fixture.TemporalAdvanceClock(t, p.Ctx, time.Now())
		datetimeCurrentExec(t, p.Ctx, c, "INSERT INTO tx_time_stored(n) VALUES(2)", 1)
		if err := c.QueryRowContext(p.Ctx, "SELECT a FROM tx_time_stored WHERE n=2").Scan(&stored); err != nil {
			t.Fatal(err)
		}
		if !stored.After(original) {
			t.Error("replay froze future default")
		}
		txTimesReadStored(t, p.Ctx, c, pg, "SELECT * FROM tx_time_replayed", 1, 0, true)
	})
}

func TestReferenceDatetimeTxTimesFeatureSmoke(t *testing.T) {
	txTimesScenario(t, func(t *testing.T, p *fixture.Pair, c *sql.Conn, pg bool, r *fixture.Recorder) {
		datetimeCurrentExec(t, p.Ctx, c, txTimesStoredDDL, 0)
		datetimeCurrentExec(t, p.Ctx, c, "CREATE VIEW tx_time_view AS SELECT "+txTimesProjection, 0)
		txTimesSymbols(t, p, c, pg)
		q := "SELECT " + txTimesProjection
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		datetimeCurrentExec(t, p.Ctx, c, "BEGIN", 0)
		a := txTimesRead(t, p.Ctx, c, pg, q, 1, 0, false)
		fixture.TemporalAdvanceClock(t, p.Ctx, time.Now())
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		b := txTimesRead(t, p.Ctx, held, pg, q, 1, 0, false)
		if !a.transaction.Equal(b.transaction) || !b.statement.After(a.statement) || !b.wall.After(a.wall) {
			t.Error("smoke clocks", a, b)
		}
		var db *sql.DB = p.Memory
		if pg {
			db = p.Postgres
		}
		peer, err := db.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		datetimeCurrentExec(t, p.Ctx, peer, "SET TIME ZONE 'UTC'", 0)
		other := txTimesRead(t, p.Ctx, peer, pg, q, 1, 0, true)
		peer.Close()
		if !other.transaction.After(a.transaction) {
			t.Error("smoke peers")
		}
		datetimeCurrentExec(t, p.Ctx, c, "SET TIME ZONE 5.5", 0)
		zoned := txTimesRead(t, p.Ctx, held, pg, q, 1, 19800, false)
		if !zoned.transaction.Equal(a.transaction) {
			t.Error("zone changed instant")
		}
		datetimeCurrentExec(t, p.Ctx, c, "INSERT INTO tx_time_stored(n) VALUES(1)", 1)
		datetimeCurrentExec(t, p.Ctx, c, "COMMIT", 0)
		datetimeCurrentExec(t, p.Ctx, c, "SET TIME ZONE 'UTC'", 0)
		fresh := txTimesRead(t, p.Ctx, held, pg, q, 1, 0, !pg)
		if !fresh.transaction.After(a.transaction) {
			t.Error("smoke autocommit freshness")
		}
		if !pg {
			var dump bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
				t.Fatal(err)
			}
			datetimeCurrentExec(t, p.Ctx, c, "INSERT INTO tx_time_stored(n) VALUES(2)", 1)
		}
		want := fixture.ControlsReferenceDiagnostic("42883", "function now(integer) does not exist")
		want.Position = "8"
		want.Hint = txTimesHint
		fixture.RunStep(t, p.Ctx, c, r, fixture.ErrorDiagnostic("SELECT now(1)", want))
		txTimesRead(t, p.Ctx, c, pg, q, 1, 0, true)
	})
}
