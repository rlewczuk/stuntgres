package reference_test

import (
	"context"
	"reflect"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func TestReferenceTimeClocksAndAtomicity(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			fixture.TemporalRunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Exec("CREATE TABLE t(v TIME)", 0))
			fixture.TemporalRunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Exec("INSERT INTO t VALUES('00:00')", 1))
			conn, err := backend.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			before := time.Now().UTC()
			held, err := conn.PrepareContext(p.Ctx, "SELECT TIME 'now',TIMESTAMP 'now' FROM t")
			if err != nil {
				t.Fatal(err)
			}
			defer held.Close()
			after := time.Now().UTC()
			var clock any
			var stamp time.Time
			if err = held.QueryRowContext(p.Ctx).Scan(&clock, &stamp); err != nil {
				t.Fatal(err)
			}
			if stamp.Before(before.Add(-time.Microsecond)) || stamp.After(after.Add(time.Microsecond)) {
				t.Fatal(stamp, before, after)
			}
			want := fixture.TimeRows(t, backend.PG, []string{"TIME"}, [][]any{{stamp.UTC().Format("15:04:05.999999")}})[0][0]
			if !reflect.DeepEqual(fixture.Normalize(clock), want) {
				t.Fatal(clock, want)
			}
			tx, err := conn.BeginTx(p.Ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			for range 2 {
				r, err := tx.ExecContext(p.Ctx, "INSERT INTO t VALUES($1)", "now")
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
			if err = tx.QueryRowContext(p.Ctx, "SELECT TIME 'now',TIMESTAMP 'now' FROM t WHERE v='00:00'").Scan(&a, &txStamp); err != nil {
				t.Fatal(err)
			}
			rows, err := tx.QueryContext(p.Ctx, "SELECT v FROM t WHERE v=$1", txStamp.UTC().Format("15:04:05.999999"))
			if err != nil {
				t.Fatal(err)
			}
			count := 0
			for rows.Next() {
				if err = rows.Scan(&b); err != nil || !reflect.DeepEqual(fixture.Normalize(a), fixture.Normalize(b)) {
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
			if err = held.QueryRowContext(p.Ctx).Scan(&frozen, &frozenStamp); err != nil || !reflect.DeepEqual(fixture.Normalize(frozen), fixture.Normalize(clock)) || !frozenStamp.Equal(stamp) {
				t.Fatal(frozen, frozenStamp, err)
			}
			if err = tx.Rollback(); err != nil {
				t.Fatal(err)
			}
			canceled, cancel := context.WithCancel(p.Ctx)
			cancel()
			// Native admission checks an already-canceled context on a held Conn.
			// pq's fast Conn execution can win its transport-cancellation race;
			// exercise PostgreSQL through database/sql's pre-admission DB check.
			var canceledErr error
			if backend.PG {
				_, canceledErr = backend.DB.ExecContext(canceled, "INSERT INTO t VALUES('24:00')")
			} else {
				_, canceledErr = conn.ExecContext(canceled, "INSERT INTO t VALUES('24:00')")
			}
			if canceledErr != context.Canceled {
				t.Fatal(canceledErr)
			}
			fixture.TemporalRunStep(t, p.Ctx, conn, backend.Recorder, fixture.Select("SELECT v FROM t", []string{"v"}, []string{"TIME"}, fixture.TimeRows(t, backend.PG, []string{"TIME"}, [][]any{{"00:00:00"}})))
		})
	}
}

func TestReferenceTimeContextualPreparedClocksAndCancellation(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			run := func(s fixture.Step) { fixture.TemporalRunStep(t, p.Ctx, b.DB, b.Recorder, s) }
			run(fixture.Exec("CREATE TABLE anchor(v TIME)", 0))
			run(fixture.Exec("INSERT INTO anchor VALUES('00:00')", 1))
			conn, err := b.DB.Conn(p.Ctx)
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
			held, err := conn.PrepareContext(p.Ctx, heldQuery)
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
			if err := held.QueryRowContext(p.Ctx).Scan(&clock, &stamp); err != nil {
				t.Fatal(err)
			}
			fixture.AssertRawValue(t, b.PG, "TIME", clock)
			if stamp.Before(before.Add(-time.Microsecond)) || stamp.After(after.Add(time.Microsecond)) {
				t.Fatal(stamp, before, after)
			}
			if _, offset := stamp.Zone(); offset != 0 {
				t.Fatal("raw TIMESTAMP offset", stamp)
			}
			stamp = stamp.Round(0).UTC()
			wantClock := fixture.TimeRows(t, b.PG, []string{"TIME"}, [][]any{{stamp.Format("15:04:05.999999")}})[0][0]
			if !reflect.DeepEqual(fixture.Normalize(clock), wantClock) {
				t.Fatal(clock, stamp)
			}
			contextualQuery := "INSERT INTO anchor VALUES('now')"
			contextual, err := conn.PrepareContext(p.Ctx, contextualQuery)
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := contextual.Close(); err != nil {
					t.Error(err)
				}
			}()
			x := fixture.HeldStatement{Executor: conn, Statement: contextual, SQL: contextualQuery}
			for range 2 {
				fixture.TemporalRunStep(t, p.Ctx, x, b.Recorder, fixture.Exec(contextualQuery, 1))
			}
			rows, err := conn.QueryContext(p.Ctx, "SELECT v FROM anchor ORDER BY v")
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
				fixture.AssertRawValue(t, b.PG, "TIME", v)
				clocks = append(clocks, fixture.Normalize(v))
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
			tx, err := conn.BeginTx(p.Ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			for range 2 {
				fixture.TemporalRunStep(t, p.Ctx, tx, b.Recorder, fixture.Exec("INSERT INTO anchor VALUES($1)", 1, "now"))
			}
			var current any
			var txStamp time.Time
			if err := tx.QueryRowContext(p.Ctx, "SELECT TIME 'now',TIMESTAMP 'now' FROM anchor WHERE v='00:00'").Scan(&current, &txStamp); err != nil {
				t.Fatal(err)
			}
			wantCurrent := fixture.TimeRows(t, b.PG, []string{"TIME"}, [][]any{{txStamp.Format("15:04:05.999999")}})[0][0]
			if !reflect.DeepEqual(fixture.Normalize(current), wantCurrent) {
				t.Fatal(current, txStamp)
			}
			q := "SELECT v FROM anchor WHERE v=$1"
			rows, err = tx.QueryContext(context.WithValue(p.Ctx, fixture.RecorderKey{}, b.Recorder), q, txStamp.Format("15:04:05.999999"))
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
				fixture.AssertRawValue(t, b.PG, "TIME", v)
				if !reflect.DeepEqual(fixture.Normalize(v), wantCurrent) {
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
			frozen := fixture.HeldStatement{Executor: conn, Statement: held, SQL: heldQuery}
			frozenStep := fixture.Select(heldQuery, []string{"time", "timestamp"}, []string{"TIME", "TIMESTAMP"}, fixture.TimeRows(t, b.PG, []string{"TIME", "TIMESTAMP"}, [][]any{{stamp.Format("15:04:05.999999"), stamp}, {stamp.Format("15:04:05.999999"), stamp}, {stamp.Format("15:04:05.999999"), stamp}, {stamp.Format("15:04:05.999999"), stamp}, {stamp.Format("15:04:05.999999"), stamp}}))
			fixture.TemporalRunStep(t, p.Ctx, frozen, b.Recorder, frozenStep)
			if err := tx.Rollback(); err != nil {
				t.Fatal(err)
			}
			fixture.TemporalRunStep(t, p.Ctx, x, b.Recorder, fixture.Exec(contextualQuery, 1))
		})
	}
}

func TestReferenceTimePreparedCancellationAndArgumentCount(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			run := func(s fixture.Step) { fixture.TemporalRunStep(t, p.Ctx, b.DB, b.Recorder, s) }
			run(fixture.Exec("CREATE TABLE anchor(v TIME)", 0))
			run(fixture.Exec("INSERT INTO anchor VALUES('00:00')", 1))
			conn, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := conn.Close(); err != nil {
					t.Error(err)
				}
			}()
			contextualQuery := "INSERT INTO anchor VALUES('now')"
			contextual, err := conn.PrepareContext(p.Ctx, contextualQuery)
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := contextual.Close(); err != nil {
					t.Error(err)
				}
			}()
			x := fixture.HeldStatement{Executor: conn, Statement: contextual, SQL: contextualQuery}
			fixture.TemporalRunStep(t, p.Ctx, x, b.Recorder, fixture.Exec(contextualQuery, 1))
			canceled, cancel := context.WithCancel(p.Ctx)
			cancel()
			if b.PG {
				_, err = b.DB.ExecContext(canceled, contextualQuery)
			} else {
				_, err = contextual.ExecContext(canceled)
			}
			if err != context.Canceled {
				t.Fatal(err)
			}
			param, err := conn.PrepareContext(p.Ctx, "SELECT v FROM anchor WHERE v=$1")
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := param.Close(); err != nil {
					t.Error(err)
				}
			}()
			rows, err := param.QueryContext(p.Ctx)
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
