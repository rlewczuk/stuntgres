package reference_test

import (
	"context"
	"testing"
	"time"
)

func TestReferenceTimestampClockAndPreparedLifetime(t *testing.T) {
	p := newTimestampReferenceScenario(t)
	for _, backend := range p.backends() {
		db := backend.db
		t.Run(backend.name, func(t *testing.T) {
			record := backend.recorder
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
			referenceTemporalAdvanceClock(t, ctx, captured)
			for range 2 {
				var again time.Time
				if e = typed.QueryRowContext(ctx).Scan(&again); e != nil || !again.Equal(frozen) {
					t.Fatal("typed now did not freeze", again, e)
				}
				if _, e = contextual.ExecContext(ctx); e != nil {
					t.Fatal(e)
				}
			}
			referenceTemporalRunStep(t, ctx, conn, record, referenceSelect("SELECT v FROM t", []string{"v"}, []string{"TIMESTAMP"}, [][]any{{captured.Round(0).UTC()}, {captured.Round(0).UTC()}, {captured.Round(0).UTC()}}))
			before = time.Now().UTC()
			tx, e := conn.BeginTx(ctx, nil)
			if e != nil {
				t.Fatal(e)
			}
			defer tx.Rollback()
			after = time.Now().UTC()
			referenceTemporalAdvanceClock(t, ctx, after)
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
			referenceTemporalRunStep(t, ctx, tx, record, referenceSelect("SELECT v FROM t WHERE v=$1", []string{"v"}, []string{"TIMESTAMP"}, [][]any{{now.Round(0).UTC()}, {now.Round(0).UTC()}, {now.Round(0).UTC()}, {now.Round(0).UTC()}}, now))
			if e = tx.Commit(); e != nil {
				t.Fatal(e)
			}
			referenceTemporalAdvanceClock(t, ctx, now)
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

func TestReferenceTimestampContextualPreparedConstants(t *testing.T) {
	p := newTimestampReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			db := backend.db
			ctx := context.WithValue(p.ctx, referenceRecorderKey{}, backend.recorder)
			referenceTimestampExec(t, ctx, db, "CREATE TABLE anchor(n INTEGER)")
			referenceTimestampExec(t, ctx, db, "INSERT INTO anchor VALUES(1)")
			referenceTimestampExec(t, ctx, db, "CREATE TABLE t(v TIMESTAMP)")
			conn, e := db.Conn(ctx)
			if e != nil {
				t.Fatal(e)
			}
			defer conn.Close()
			before := time.Now().UTC()
			literal, e := conn.PrepareContext(ctx, "SELECT TIMESTAMP 'now' FROM anchor")
			if e != nil {
				t.Fatal(e)
			}
			defer literal.Close()
			after := time.Now().UTC()
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
			var frozen time.Time
			if e = literal.QueryRowContext(ctx).Scan(&frozen); e != nil {
				t.Fatal(e)
			}
			if frozen.Before(before.Add(-time.Microsecond)) || frozen.After(after.Add(time.Microsecond)) {
				t.Fatal("constant not analysis clock", frozen, before, after)
			}
			if _, e = contextual.ExecContext(ctx); e != nil {
				t.Fatal(e)
			}
			var first time.Time
			if e = conn.QueryRowContext(ctx, "SELECT v FROM t").Scan(&first); e != nil {
				t.Fatal(e)
			}
			if _, offset := first.Zone(); offset != 0 {
				t.Fatal("raw TIMESTAMP offset", first)
			}
			first = first.Round(0).UTC()
			referenceTemporalAdvanceClock(t, ctx, first)
			for range 2 {
				var again time.Time
				if e = literal.QueryRowContext(ctx).Scan(&again); e != nil || !again.Equal(frozen) {
					t.Fatal("prepared constant changed", again, e)
				}
				if _, e = contextual.ExecContext(ctx); e != nil {
					t.Fatal(e)
				}
			}
			rows, e := conn.QueryContext(ctx, "SELECT v FROM t")
			if e != nil {
				t.Fatal(e)
			}
			for rows.Next() {
				var got time.Time
				if e = rows.Scan(&got); e != nil || !got.Equal(first) {
					t.Fatal("contextual constant changed", got, e)
				}
			}
			if e = rows.Err(); e != nil {
				t.Fatal(e)
			}
			rows.Close()
			txBefore := time.Now().UTC()
			tx, e := conn.BeginTx(ctx, nil)
			if e != nil {
				t.Fatal(e)
			}
			defer tx.Rollback()
			txAfter := time.Now().UTC()
			referenceTemporalAdvanceClock(t, ctx, txAfter)
			if _, e = tx.ExecContext(ctx, "INSERT INTO t VALUES('now'),('now')"); e != nil {
				t.Fatal(e)
			}
			var now, today, tomorrow, yesterday time.Time
			if e = tx.QueryRowContext(ctx, "SELECT TIMESTAMP 'now',TIMESTAMP 'today',TIMESTAMP 'tomorrow',TIMESTAMP 'yesterday' FROM anchor").Scan(&now, &today, &tomorrow, &yesterday); e != nil {
				t.Fatal(e)
			}
			if now.Before(txBefore.Add(-time.Microsecond)) || now.After(txAfter.Add(time.Microsecond)) {
				t.Fatal("statement arrival replaced transaction clock", now, txBefore, txAfter)
			}
			midnight := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
			if !today.Equal(midnight) || !tomorrow.Equal(midnight.AddDate(0, 0, 1)) || !yesterday.Equal(midnight.AddDate(0, 0, -1)) {
				t.Fatal(today, tomorrow, yesterday)
			}
			var during time.Time
			if e = literal.QueryRowContext(ctx).Scan(&during); e != nil || !during.Equal(frozen) {
				t.Fatal("original physical literal changed in transaction", during, frozen, e)
			}
			if _, e = contextual.ExecContext(ctx); e != nil {
				t.Fatal(e)
			}
			txParam := tx.StmtContext(ctx, param)
			if _, e = txParam.ExecContext(ctx, "now"); e != nil {
				t.Fatal(e)
			}
			if _, e = txParam.ExecContext(ctx, "now"); e != nil {
				t.Fatal(e)
			}
			txParam.Close()
			rows, e = tx.QueryContext(ctx, "SELECT v FROM t WHERE v=$1", now)
			if e != nil {
				t.Fatal(e)
			}
			count := 0
			for rows.Next() {
				count++
			}
			e = rows.Err()
			rows.Close()
			if e != nil || count != 4 {
				t.Fatal("transaction clock unequal", count, e)
			}
			if e = tx.Commit(); e != nil {
				t.Fatal(e)
			}
			var afterCommit time.Time
			if e = literal.QueryRowContext(ctx).Scan(&afterCommit); e != nil || !afterCommit.Equal(frozen) {
				t.Fatal("original physical literal changed after commit", afterCommit, frozen, e)
			}
			rows, e = conn.QueryContext(ctx, "SELECT v FROM t WHERE v=$1", first)
			if e != nil {
				t.Fatal(e)
			}
			referenceTimestampCheckRows(t, rows, backend.pg, []string{"v"}, []string{"TIMESTAMP"}, [][]any{{first}, {first}, {first}, {first}})
			referenceTemporalAdvanceClock(t, ctx, now)
			if _, e = param.ExecContext(ctx, "now"); e != nil {
				t.Fatal(e)
			}
			var refreshed time.Time
			if e = conn.QueryRowContext(ctx, "SELECT v FROM t ORDER BY v DESC").Scan(&refreshed); e != nil || !refreshed.After(now) {
				t.Fatal("parameter clock frozen", refreshed, e)
			}
		})
	}
}

func TestReferenceTimestampCoreSpecialZoneAdjacency(t *testing.T) {
	p := newTimestampReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			db := backend.db
			ctx := context.WithValue(p.ctx, referenceRecorderKey{}, backend.recorder)
			referenceTimestampExec(t, ctx, db, "CREATE TABLE anchor(v INTEGER)")
			referenceTimestampExec(t, ctx, db, "INSERT INTO anchor VALUES(1)")
			before := time.Now().UTC()
			tx, err := db.BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			after := time.Now().UTC()
			var now, today time.Time
			if err := tx.QueryRowContext(ctx, "SELECT TIMESTAMP 'now',TIMESTAMP 'today+2' FROM anchor").Scan(&now, &today); err != nil {
				t.Fatal(err)
			}
			if now.Before(before.Add(-time.Microsecond)) || now.After(after.Add(time.Microsecond)) {
				t.Fatal("transaction start interval", now, before, after)
			}
			want := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
			_, nowOffset := now.Zone()
			_, todayOffset := today.Zone()
			if nowOffset != 0 || todayOffset != 0 || !today.Equal(want) {
				t.Fatal("raw civil clock or midnight mismatch", now, today, want)
			}
			stmt, err := tx.PrepareContext(ctx, "SELECT TIMESTAMP 'today+2' FROM anchor")
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			err = stmt.QueryRowContext(ctx).Scan(&today)
			_, todayOffset = today.Zone()
			if err != nil || todayOffset != 0 || !today.Equal(want) {
				t.Fatal("prepared raw civil midnight mismatch", today, want, err)
			}
		})
	}
}
