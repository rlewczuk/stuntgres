package reference_test

import (
	"context"
	"database/sql"
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func TestReferenceDatetimeCurrentTransactionClock(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			conn, err := backend.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			datetimeCurrentSeed(t, p.Ctx, conn)
			q := "SELECT " + datetimeCurrentProjection + " FROM dc_anchor ORDER BY n"
			stmt, err := conn.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := fixture.HeldStatement{Executor: conn, Statement: stmt, SQL: q}
			defer conn.ExecContext(p.Ctx, "ROLLBACK")
			var previous time.Time
			for _, end := range []string{"COMMIT", "ROLLBACK", "COMMIT AND CHAIN", "ROLLBACK AND CHAIN"} {
				datetimeCurrentExec(t, p.Ctx, conn, "BEGIN", 0)
				stamp := datetimeCurrentWitness(t, p.Ctx, conn)
				if !previous.IsZero() && !stamp.After(previous) {
					t.Fatal("new transaction retained clock", stamp, previous)
				}
				datetimeCurrentCheck(t, p.Ctx, conn, backend.PG, q, stamp, 0, 6, 2)
				fixture.TemporalAdvanceClock(t, p.Ctx, stamp)
				datetimeCurrentCheck(t, p.Ctx, held, backend.PG, q, stamp, 0, 6, 2)
				datetimeCurrentExec(t, p.Ctx, conn, "SAVEPOINT dc_mark", 0)
				datetimeCurrentExec(t, p.Ctx, conn, "SAVEPOINT dc_inner", 0)
				datetimeCurrentExec(t, p.Ctx, conn, "RELEASE SAVEPOINT dc_inner", 0)
				bad := fixture.ControlsReferenceDiagnostic("42601", `syntax error at or near "-"`)
				bad.Position = "26"
				fixture.RunStep(t, p.Ctx, conn, backend.Recorder, fixture.ErrorDiagnostic("SELECT CURRENT_TIMESTAMP(-1)", bad))
				aborted := fixture.ControlsReferenceDiagnostic("25P02", "current transaction is aborted, commands ignored until end of transaction block")
				fixture.RunStep(t, p.Ctx, conn, backend.Recorder, fixture.ErrorDiagnostic("SELECT "+datetimeCurrentProjection, aborted))
				datetimeCurrentExec(t, p.Ctx, conn, "ROLLBACK TO SAVEPOINT dc_mark", 0)
				datetimeCurrentCheck(t, p.Ctx, held, backend.PG, q, stamp, 0, 6, 2)
				datetimeCurrentExec(t, p.Ctx, conn, end, 0)
				previous = stamp
				if end == "COMMIT AND CHAIN" || end == "ROLLBACK AND CHAIN" {
					chained := datetimeCurrentWitness(t, p.Ctx, conn)
					if !chained.After(stamp) {
						t.Fatal("CHAIN did not refresh", chained, stamp)
					}
					datetimeCurrentCheck(t, p.Ctx, held, backend.PG, q, chained, 0, 6, 2)
					fixture.TemporalAdvanceClock(t, p.Ctx, chained)
					datetimeCurrentCheck(t, p.Ctx, conn, backend.PG, q, chained, 0, 6, 2)
					datetimeCurrentExec(t, p.Ctx, conn, "ROLLBACK", 0)
					previous = chained
				}
				fixture.TemporalAdvanceClock(t, p.Ctx, previous)
			}
			// The actual database/sql transaction route uses the SAME preheld statement.
			tx, err := conn.BeginTx(p.Ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			stamp := datetimeCurrentWitness(t, p.Ctx, tx)
			if !stamp.After(previous) {
				t.Fatal("BeginTx stale", stamp, previous)
			}
			txstmt := tx.StmtContext(p.Ctx, stmt)
			datetimeCurrentCheck(t, p.Ctx, fixture.HeldStatement{Executor: tx, Statement: txstmt, SQL: q}, backend.PG, q, stamp, 0, 6, 2)
			fixture.TemporalAdvanceClock(t, p.Ctx, stamp)
			datetimeCurrentCheck(t, p.Ctx, tx, backend.PG, q, stamp, 0, 6, 2)
			txstmt.Close()
			if err := tx.Commit(); err != nil {
				t.Fatal(err)
			}
			// One implicit statement contains both the independent literal witness and
			// held CURRENT_TIMESTAMP, so an intervening implicit transaction is not used.
			implicitSQL := "SELECT CURRENT_TIMESTAMP,TIMESTAMPTZ 'now' FROM dc_anchor WHERE n=1"
			implicit, err := conn.PrepareContext(p.Ctx, implicitSQL)
			if err != nil {
				t.Fatal(err)
			}
			defer implicit.Close()
			previous = stamp
			var frozen time.Time
			for range 2 {
				fixture.TemporalAdvanceClock(t, p.Ctx, previous)
				var current, witness time.Time
				if err := implicit.QueryRowContext(p.Ctx).Scan(&current, &witness); err != nil {
					t.Fatal(err)
				}
				// A prepared literal is frozen at analysis: only CURRENT must refresh.
				if !current.After(previous) {
					t.Fatal("held implicit current stale", current, previous)
				}
				if witness.After(current) {
					t.Fatal("prepared witness unexpectedly future", witness, current)
				}
				if frozen.IsZero() {
					frozen = witness
				} else if !witness.Equal(frozen) {
					t.Fatal("prepared literal was not frozen", witness, frozen)
				}
				previous = current
			}
		})
	}
}

func datetimeCurrentZoneWorkflow(t *testing.T, ctx context.Context, conn *sql.Conn, pg bool, record *fixture.Recorder, stamp time.Time) {
	t.Helper()
	q := "SELECT " + datetimeCurrentProjection
	for _, zone := range []struct {
		name string
		east int
	}{{"Pacific/Kiritimati", 14 * 3600}, {"Etc/GMT+12", -12 * 3600}} {
		datetimeCurrentExec(t, ctx, conn, "SET TIME ZONE '"+zone.name+"'", 0)
		datetimeCurrentCheck(t, ctx, conn, pg, q, stamp, zone.east, 6, 1)
		datetimeCurrentTextCheck(t, ctx, conn, record, stamp, zone.east, 6)
	}
	plus := datetimeCurrentExpected(stamp, 14*3600, 6)[0]
	minus := datetimeCurrentExpected(stamp, -12*3600, 6)[0]
	if plus == minus {
		t.Fatal("independent 26-hour boundary oracle failed", plus, minus)
	}
	datetimeCurrentExec(t, ctx, conn, "SAVEPOINT dc_zone", 0)
	datetimeCurrentExec(t, ctx, conn, "SET LOCAL TIME ZONE 'Pacific/Kiritimati'", 0)
	datetimeCurrentCheck(t, ctx, conn, pg, q, stamp, 14*3600, 6, 1)
	datetimeCurrentExec(t, ctx, conn, "ROLLBACK TO SAVEPOINT dc_zone", 0)
	datetimeCurrentCheck(t, ctx, conn, pg, q, stamp, -12*3600, 6, 1)
	datetimeCurrentExec(t, ctx, conn, "RELEASE SAVEPOINT dc_zone", 0)
}

func TestReferenceDatetimeCurrentSessionZone(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			conn, err := backend.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			datetimeCurrentSeed(t, p.Ctx, conn)
			datetimeCurrentExec(t, p.Ctx, conn, "BEGIN", 0)
			defer conn.ExecContext(p.Ctx, "ROLLBACK")
			datetimeCurrentZoneWorkflow(t, p.Ctx, conn, backend.PG, backend.Recorder, datetimeCurrentWitness(t, p.Ctx, conn))
		})
	}
}

func datetimeCurrentPeers(t *testing.T, p *fixture.Pair, db *sql.DB, pg bool) {
	t.Helper()
	a, err := db.Conn(p.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	b, err := db.Conn(p.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	defer a.ExecContext(p.Ctx, "ROLLBACK")
	defer b.ExecContext(p.Ctx, "ROLLBACK")
	datetimeCurrentExec(t, p.Ctx, a, "SET TIME ZONE 'Pacific/Kiritimati'", 0)
	datetimeCurrentExec(t, p.Ctx, b, "SET TIME ZONE 'Etc/GMT+12'", 0)
	datetimeCurrentExec(t, p.Ctx, a, "BEGIN", 0)
	first := datetimeCurrentWitness(t, p.Ctx, a)
	fixture.TemporalAdvanceClock(t, p.Ctx, first)
	datetimeCurrentExec(t, p.Ctx, b, "BEGIN", 0)
	second := datetimeCurrentWitness(t, p.Ctx, b)
	if !second.After(first) {
		t.Fatal("peer clocks not independent", first, second)
	}
	q := "SELECT " + datetimeCurrentProjection
	datetimeCurrentCheck(t, p.Ctx, a, pg, q, first, 14*3600, 6, 1)
	datetimeCurrentCheck(t, p.Ctx, b, pg, q, second, -12*3600, 6, 1)
	fixture.TemporalAdvanceClock(t, p.Ctx, second)
	datetimeCurrentExec(t, p.Ctx, b, "COMMIT", 0)
	datetimeCurrentExec(t, p.Ctx, b, "SET TIME ZONE 'UTC'", 0)
	datetimeCurrentExec(t, p.Ctx, b, "BEGIN", 0)
	third := datetimeCurrentWitness(t, p.Ctx, b)
	if !third.After(second) {
		t.Fatal("peer restart stale", third, second)
	}
	datetimeCurrentCheck(t, p.Ctx, b, pg, q, third, 0, 6, 1)
	datetimeCurrentCheck(t, p.Ctx, a, pg, q, first, 14*3600, 6, 1)
}

func TestReferenceDatetimeCurrentPeerSessions(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			conn, err := backend.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			datetimeCurrentSeed(t, p.Ctx, conn)
			conn.Close()
			datetimeCurrentPeers(t, p, backend.DB, backend.PG)
		})
	}
}

func TestReferenceDatetimeCurrentFeatureSmoke(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			conn, err := backend.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			datetimeCurrentSeed(t, p.Ctx, conn)
			q := "SELECT " + datetimeCurrentProjection
			stmt, err := conn.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := fixture.HeldStatement{Executor: conn, Statement: stmt, SQL: q}
			datetimeCurrentExec(t, p.Ctx, conn, "BEGIN", 0)
			defer conn.ExecContext(p.Ctx, "ROLLBACK")
			first := datetimeCurrentWitness(t, p.Ctx, conn)
			datetimeCurrentCheck(t, p.Ctx, conn, backend.PG, q, first, 0, 6, 1)
			fixture.TemporalAdvanceClock(t, p.Ctx, first)
			datetimeCurrentCheck(t, p.Ctx, held, backend.PG, q, first, 0, 6, 1)
			for _, precision := range []int{0, 3, 6} {
				datetimeCurrentTextCheck(t, p.Ctx, conn, backend.Recorder, first, 0, precision)
			}
			datetimeCurrentZoneWorkflow(t, p.Ctx, conn, backend.PG, backend.Recorder, first)
			datetimeCurrentExec(t, p.Ctx, conn, "COMMIT", 0)
			datetimeCurrentExec(t, p.Ctx, conn, "SET TIME ZONE 'UTC'", 0)
			datetimeCurrentExec(t, p.Ctx, conn, "BEGIN", 0)
			second := datetimeCurrentWitness(t, p.Ctx, conn)
			if !second.After(first) {
				t.Fatal(fmt.Sprintf("fresh clock %v <= %v", second, first))
			}
			datetimeCurrentCheck(t, p.Ctx, held, backend.PG, q, second, 0, 6, 1)
			datetimeCurrentExec(t, p.Ctx, conn, "ROLLBACK", 0)
			stmt.Close()
			conn.Close()
			datetimeCurrentPeers(t, p, backend.DB, backend.PG)
		})
	}
}
