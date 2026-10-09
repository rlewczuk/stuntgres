package reference_test

import (
	"context"
	"reflect"
	"testing"
	"time"
)

func TestReferenceTimeClocksAndAtomicity(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			referenceTemporalRunStep(t, p.ctx, backend.db, backend.recorder, referenceExec("CREATE TABLE t(v TIME)", 0))
			referenceTemporalRunStep(t, p.ctx, backend.db, backend.recorder, referenceExec("INSERT INTO t VALUES('00:00')", 1))
			conn, err := backend.db.Conn(p.ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			before := time.Now().UTC()
			held, err := conn.PrepareContext(p.ctx, "SELECT TIME 'now',TIMESTAMP 'now' FROM t")
			if err != nil {
				t.Fatal(err)
			}
			defer held.Close()
			after := time.Now().UTC()
			var clock any
			var stamp time.Time
			if err = held.QueryRowContext(p.ctx).Scan(&clock, &stamp); err != nil {
				t.Fatal(err)
			}
			if stamp.Before(before.Add(-time.Microsecond)) || stamp.After(after.Add(time.Microsecond)) {
				t.Fatal(stamp, before, after)
			}
			want := referenceTimeRows(t, backend.pg, []string{"TIME"}, [][]any{{stamp.UTC().Format("15:04:05.999999")}})[0][0]
			if !reflect.DeepEqual(referenceNormalize(clock), want) {
				t.Fatal(clock, want)
			}
			tx, err := conn.BeginTx(p.ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			for range 2 {
				r, err := tx.ExecContext(p.ctx, "INSERT INTO t VALUES($1)", "now")
				if err != nil {
					t.Fatal(err)
				}
				n, err := r.RowsAffected()
				if err != nil || n != 1 {
					t.Fatal(n, err)
				}
			}
			var a, b any
			var txStamp time.Time
			if err = tx.QueryRowContext(p.ctx, "SELECT TIME 'now',TIMESTAMP 'now' FROM t WHERE v='00:00'").Scan(&a, &txStamp); err != nil {
				t.Fatal(err)
			}
			rows, err := tx.QueryContext(p.ctx, "SELECT v FROM t WHERE v=$1", txStamp.UTC().Format("15:04:05.999999"))
			if err != nil {
				t.Fatal(err)
			}
			count := 0
			for rows.Next() {
				if err = rows.Scan(&b); err != nil || !reflect.DeepEqual(referenceNormalize(a), referenceNormalize(b)) {
					t.Fatal(a, b, err)
				}
				count++
			}
			if err = rows.Err(); err != nil || count < 2 {
				t.Fatal(count, err)
			}
			rows.Close()
			var frozen any
			var frozenStamp time.Time
			if err = held.QueryRowContext(p.ctx).Scan(&frozen, &frozenStamp); err != nil || !reflect.DeepEqual(referenceNormalize(frozen), referenceNormalize(clock)) || !frozenStamp.Equal(stamp) {
				t.Fatal(frozen, frozenStamp, err)
			}
			if err = tx.Rollback(); err != nil {
				t.Fatal(err)
			}
			canceled, cancel := context.WithCancel(p.ctx)
			cancel()
			// Native admission checks an already-canceled context on a held Conn.
			// pq's fast Conn execution can win its transport-cancellation race;
			// exercise PostgreSQL through database/sql's pre-admission DB check.
			var canceledErr error
			if backend.pg {
				_, canceledErr = backend.db.ExecContext(canceled, "INSERT INTO t VALUES('24:00')")
			} else {
				_, canceledErr = conn.ExecContext(canceled, "INSERT INTO t VALUES('24:00')")
			}
			if canceledErr != context.Canceled {
				t.Fatal(canceledErr)
			}
			referenceTemporalRunStep(t, p.ctx, conn, backend.recorder, referenceSelect("SELECT v FROM t", []string{"v"}, []string{"TIME"}, referenceTimeRows(t, backend.pg, []string{"TIME"}, [][]any{{"00:00:00"}})))
		})
	}
}

func TestReferenceTimeContextualPreparedClocksAndCancellation(t *testing.T) {
	p := newReferenceScenario(t)
	for _, b := range p.backends() {
		t.Run(b.name, func(t *testing.T) {
			run := func(s referenceStep) { referenceTemporalRunStep(t, p.ctx, b.db, b.recorder, s) }
			run(referenceExec("CREATE TABLE anchor(v TIME)", 0))
			run(referenceExec("INSERT INTO anchor VALUES('00:00')", 1))
			conn, err := b.db.Conn(p.ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := conn.Close(); err != nil {
					t.Error(err)
				}
			}()
			before := time.Now().UTC()
			heldQuery := "SELECT TIME 'now',TIMESTAMP 'now' FROM anchor"
			held, err := conn.PrepareContext(p.ctx, heldQuery)
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := held.Close(); err != nil {
					t.Error(err)
				}
			}()
			after := time.Now().UTC()
			var clock any
			var stamp time.Time
			if err := held.QueryRowContext(p.ctx).Scan(&clock, &stamp); err != nil {
				t.Fatal(err)
			}
			referenceAssertRawValue(t, b.pg, "TIME", clock)
			if stamp.Before(before.Add(-time.Microsecond)) || stamp.After(after.Add(time.Microsecond)) {
				t.Fatal(stamp, before, after)
			}
			if _, offset := stamp.Zone(); offset != 0 {
				t.Fatal("raw TIMESTAMP offset", stamp)
			}
			stamp = stamp.Round(0).UTC()
			wantClock := referenceTimeRows(t, b.pg, []string{"TIME"}, [][]any{{stamp.Format("15:04:05.999999")}})[0][0]
			if !reflect.DeepEqual(referenceNormalize(clock), wantClock) {
				t.Fatal(clock, stamp)
			}
			contextualQuery := "INSERT INTO anchor VALUES('now')"
			contextual, err := conn.PrepareContext(p.ctx, contextualQuery)
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := contextual.Close(); err != nil {
					t.Error(err)
				}
			}()
			x := referenceHeldStatement{referenceExecutor: conn, statement: contextual, query: contextualQuery}
			for range 2 {
				referenceTemporalRunStep(t, p.ctx, x, b.recorder, referenceExec(contextualQuery, 1))
			}
			rows, err := conn.QueryContext(p.ctx, "SELECT v FROM anchor ORDER BY v")
			if err != nil {
				t.Fatal(err)
			}
			var clocks []any
			for rows.Next() {
				var v any
				if err := rows.Scan(&v); err != nil {
					rows.Close()
					t.Fatal(err)
				}
				referenceAssertRawValue(t, b.pg, "TIME", v)
				clocks = append(clocks, referenceNormalize(v))
			}
			if err := rows.Err(); err != nil {
				rows.Close()
				t.Fatal(err)
			}
			if err := rows.Close(); err != nil {
				t.Fatal(err)
			}
			if len(clocks) != 3 || !reflect.DeepEqual(clocks[1], clocks[2]) {
				t.Fatal("contextual now not frozen", clocks)
			}
			tx, err := conn.BeginTx(p.ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			for range 2 {
				referenceTemporalRunStep(t, p.ctx, tx, b.recorder, referenceExec("INSERT INTO anchor VALUES($1)", 1, "now"))
			}
			var current any
			var txStamp time.Time
			if err := tx.QueryRowContext(p.ctx, "SELECT TIME 'now',TIMESTAMP 'now' FROM anchor WHERE v='00:00'").Scan(&current, &txStamp); err != nil {
				t.Fatal(err)
			}
			wantCurrent := referenceTimeRows(t, b.pg, []string{"TIME"}, [][]any{{txStamp.Format("15:04:05.999999")}})[0][0]
			if !reflect.DeepEqual(referenceNormalize(current), wantCurrent) {
				t.Fatal(current, txStamp)
			}
			q := "SELECT v FROM anchor WHERE v=$1"
			rows, err = tx.QueryContext(context.WithValue(p.ctx, referenceRecorderKey{}, b.recorder), q, txStamp.Format("15:04:05.999999"))
			if err != nil {
				t.Fatal(err)
			}
			count := 0
			for rows.Next() {
				var v any
				if err := rows.Scan(&v); err != nil {
					rows.Close()
					t.Fatal(err)
				}
				referenceAssertRawValue(t, b.pg, "TIME", v)
				if !reflect.DeepEqual(referenceNormalize(v), wantCurrent) {
					rows.Close()
					t.Fatal(v, wantCurrent)
				}
				count++
			}
			if err := rows.Err(); err != nil || count < 2 {
				rows.Close()
				t.Fatal(count, err)
			}
			if err := rows.Close(); err != nil {
				t.Fatal(err)
			}
			frozen := referenceHeldStatement{referenceExecutor: conn, statement: held, query: heldQuery}
			frozenStep := referenceSelect(heldQuery, []string{"time", "timestamp"}, []string{"TIME", "TIMESTAMP"}, referenceTimeRows(t, b.pg, []string{"TIME", "TIMESTAMP"}, [][]any{{stamp.Format("15:04:05.999999"), stamp}, {stamp.Format("15:04:05.999999"), stamp}, {stamp.Format("15:04:05.999999"), stamp}, {stamp.Format("15:04:05.999999"), stamp}, {stamp.Format("15:04:05.999999"), stamp}}))
			referenceTemporalRunStep(t, p.ctx, frozen, b.recorder, frozenStep)
			if err := tx.Rollback(); err != nil {
				t.Fatal(err)
			}
			referenceTemporalRunStep(t, p.ctx, x, b.recorder, referenceExec(contextualQuery, 1))
		})
	}
}

func TestReferenceTimePreparedCancellationAndArgumentCount(t *testing.T) {
	p := newReferenceScenario(t)
	for _, b := range p.backends() {
		t.Run(b.name, func(t *testing.T) {
			run := func(s referenceStep) { referenceTemporalRunStep(t, p.ctx, b.db, b.recorder, s) }
			run(referenceExec("CREATE TABLE anchor(v TIME)", 0))
			run(referenceExec("INSERT INTO anchor VALUES('00:00')", 1))
			conn, err := b.db.Conn(p.ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := conn.Close(); err != nil {
					t.Error(err)
				}
			}()
			contextualQuery := "INSERT INTO anchor VALUES('now')"
			contextual, err := conn.PrepareContext(p.ctx, contextualQuery)
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := contextual.Close(); err != nil {
					t.Error(err)
				}
			}()
			x := referenceHeldStatement{referenceExecutor: conn, statement: contextual, query: contextualQuery}
			referenceTemporalRunStep(t, p.ctx, x, b.recorder, referenceExec(contextualQuery, 1))
			canceled, cancel := context.WithCancel(p.ctx)
			cancel()
			if b.pg {
				_, err = b.db.ExecContext(canceled, contextualQuery)
			} else {
				_, err = contextual.ExecContext(canceled)
			}
			if err != context.Canceled {
				t.Fatal(err)
			}
			param, err := conn.PrepareContext(p.ctx, "SELECT v FROM anchor WHERE v=$1")
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := param.Close(); err != nil {
					t.Error(err)
				}
			}()
			rows, err := param.QueryContext(p.ctx)
			if rows != nil {
				rows.Close()
				t.Fatal("missing argument exposed rows")
			}
			if err == nil {
				t.Fatal("missing argument succeeded")
			}
		})
	}
}
