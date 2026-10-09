package reference_test

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"
)

func TestReferenceDatetimeCurrentTransactionClock(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			conn, err := backend.db.Conn(p.ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			datetimeCurrentSeed(t, p.ctx, conn)
			q := "SELECT " + datetimeCurrentProjection + " FROM dc_anchor ORDER BY n"
			stmt, err := conn.PrepareContext(p.ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := referenceHeldStatement{referenceExecutor: conn, statement: stmt, query: q}
			defer conn.ExecContext(p.ctx, "ROLLBACK")
			var previous time.Time
			for _, end := range []string{"COMMIT", "ROLLBACK", "COMMIT AND CHAIN", "ROLLBACK AND CHAIN"} {
				datetimeCurrentExec(t, p.ctx, conn, "BEGIN", 0)
				stamp := datetimeCurrentWitness(t, p.ctx, conn)
				if !previous.IsZero() && !stamp.After(previous) {
					t.Fatal("new transaction retained clock", stamp, previous)
				}
				datetimeCurrentCheck(t, p.ctx, conn, backend.pg, q, stamp, 0, 6, 2)
				referenceTemporalAdvanceClock(t, p.ctx, stamp)
				datetimeCurrentCheck(t, p.ctx, held, backend.pg, q, stamp, 0, 6, 2)
				datetimeCurrentExec(t, p.ctx, conn, "SAVEPOINT dc_mark", 0)
				datetimeCurrentExec(t, p.ctx, conn, "SAVEPOINT dc_inner", 0)
				datetimeCurrentExec(t, p.ctx, conn, "RELEASE SAVEPOINT dc_inner", 0)
				bad := controlsReferenceDiagnostic("42601", `syntax error at or near "-"`)
				bad.Position = "26"
				referenceRunStep(t, p.ctx, conn, backend.recorder, referenceErrorDiagnostic("SELECT CURRENT_TIMESTAMP(-1)", bad))
				aborted := controlsReferenceDiagnostic("25P02", "current transaction is aborted, commands ignored until end of transaction block")
				referenceRunStep(t, p.ctx, conn, backend.recorder, referenceErrorDiagnostic("SELECT "+datetimeCurrentProjection, aborted))
				datetimeCurrentExec(t, p.ctx, conn, "ROLLBACK TO SAVEPOINT dc_mark", 0)
				datetimeCurrentCheck(t, p.ctx, held, backend.pg, q, stamp, 0, 6, 2)
				datetimeCurrentExec(t, p.ctx, conn, end, 0)
				previous = stamp
				if end == "COMMIT AND CHAIN" || end == "ROLLBACK AND CHAIN" {
					chained := datetimeCurrentWitness(t, p.ctx, conn)
					if !chained.After(stamp) {
						t.Fatal("CHAIN did not refresh", chained, stamp)
					}
					datetimeCurrentCheck(t, p.ctx, held, backend.pg, q, chained, 0, 6, 2)
					referenceTemporalAdvanceClock(t, p.ctx, chained)
					datetimeCurrentCheck(t, p.ctx, conn, backend.pg, q, chained, 0, 6, 2)
					datetimeCurrentExec(t, p.ctx, conn, "ROLLBACK", 0)
					previous = chained
				}
				referenceTemporalAdvanceClock(t, p.ctx, previous)
			}
			// The actual database/sql transaction route uses the SAME preheld statement.
			tx, err := conn.BeginTx(p.ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			stamp := datetimeCurrentWitness(t, p.ctx, tx)
			if !stamp.After(previous) {
				t.Fatal("BeginTx stale", stamp, previous)
			}
			txstmt := tx.StmtContext(p.ctx, stmt)
			datetimeCurrentCheck(t, p.ctx, referenceHeldStatement{referenceExecutor: tx, statement: txstmt, query: q}, backend.pg, q, stamp, 0, 6, 2)
			referenceTemporalAdvanceClock(t, p.ctx, stamp)
			datetimeCurrentCheck(t, p.ctx, tx, backend.pg, q, stamp, 0, 6, 2)
			txstmt.Close()
			if err := tx.Commit(); err != nil {
				t.Fatal(err)
			}
			// One implicit statement contains both the independent literal witness and
			// held CURRENT_TIMESTAMP, so an intervening implicit transaction is not used.
			implicitSQL := "SELECT CURRENT_TIMESTAMP,TIMESTAMPTZ 'now' FROM dc_anchor WHERE n=1"
			implicit, err := conn.PrepareContext(p.ctx, implicitSQL)
			if err != nil {
				t.Fatal(err)
			}
			defer implicit.Close()
			previous = stamp
			var frozen time.Time
			for range 2 {
				referenceTemporalAdvanceClock(t, p.ctx, previous)
				var current, witness time.Time
				if err := implicit.QueryRowContext(p.ctx).Scan(&current, &witness); err != nil {
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

func datetimeCurrentZoneWorkflow(t *testing.T, ctx context.Context, conn *sql.Conn, pg bool, record *referenceRecorder, stamp time.Time) {
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
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			conn, err := backend.db.Conn(p.ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			datetimeCurrentSeed(t, p.ctx, conn)
			datetimeCurrentExec(t, p.ctx, conn, "BEGIN", 0)
			defer conn.ExecContext(p.ctx, "ROLLBACK")
			datetimeCurrentZoneWorkflow(t, p.ctx, conn, backend.pg, backend.recorder, datetimeCurrentWitness(t, p.ctx, conn))
		})
	}
}

func datetimeCurrentPeers(t *testing.T, p *referencePair, db *sql.DB, pg bool) {
	t.Helper()
	a, err := db.Conn(p.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	b, err := db.Conn(p.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	defer a.ExecContext(p.ctx, "ROLLBACK")
	defer b.ExecContext(p.ctx, "ROLLBACK")
	datetimeCurrentExec(t, p.ctx, a, "SET TIME ZONE 'Pacific/Kiritimati'", 0)
	datetimeCurrentExec(t, p.ctx, b, "SET TIME ZONE 'Etc/GMT+12'", 0)
	datetimeCurrentExec(t, p.ctx, a, "BEGIN", 0)
	first := datetimeCurrentWitness(t, p.ctx, a)
	referenceTemporalAdvanceClock(t, p.ctx, first)
	datetimeCurrentExec(t, p.ctx, b, "BEGIN", 0)
	second := datetimeCurrentWitness(t, p.ctx, b)
	if !second.After(first) {
		t.Fatal("peer clocks not independent", first, second)
	}
	q := "SELECT " + datetimeCurrentProjection
	datetimeCurrentCheck(t, p.ctx, a, pg, q, first, 14*3600, 6, 1)
	datetimeCurrentCheck(t, p.ctx, b, pg, q, second, -12*3600, 6, 1)
	referenceTemporalAdvanceClock(t, p.ctx, second)
	datetimeCurrentExec(t, p.ctx, b, "COMMIT", 0)
	datetimeCurrentExec(t, p.ctx, b, "SET TIME ZONE 'UTC'", 0)
	datetimeCurrentExec(t, p.ctx, b, "BEGIN", 0)
	third := datetimeCurrentWitness(t, p.ctx, b)
	if !third.After(second) {
		t.Fatal("peer restart stale", third, second)
	}
	datetimeCurrentCheck(t, p.ctx, b, pg, q, third, 0, 6, 1)
	datetimeCurrentCheck(t, p.ctx, a, pg, q, first, 14*3600, 6, 1)
}

func TestReferenceDatetimeCurrentPeerSessions(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			conn, err := backend.db.Conn(p.ctx)
			if err != nil {
				t.Fatal(err)
			}
			datetimeCurrentSeed(t, p.ctx, conn)
			conn.Close()
			datetimeCurrentPeers(t, p, backend.db, backend.pg)
		})
	}
}

func TestReferenceDatetimeCurrentFeatureSmoke(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			conn, err := backend.db.Conn(p.ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			datetimeCurrentSeed(t, p.ctx, conn)
			q := "SELECT " + datetimeCurrentProjection
			stmt, err := conn.PrepareContext(p.ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := referenceHeldStatement{referenceExecutor: conn, statement: stmt, query: q}
			datetimeCurrentExec(t, p.ctx, conn, "BEGIN", 0)
			defer conn.ExecContext(p.ctx, "ROLLBACK")
			first := datetimeCurrentWitness(t, p.ctx, conn)
			datetimeCurrentCheck(t, p.ctx, conn, backend.pg, q, first, 0, 6, 1)
			referenceTemporalAdvanceClock(t, p.ctx, first)
			datetimeCurrentCheck(t, p.ctx, held, backend.pg, q, first, 0, 6, 1)
			for _, precision := range []int{0, 3, 6} {
				datetimeCurrentTextCheck(t, p.ctx, conn, backend.recorder, first, 0, precision)
			}
			datetimeCurrentZoneWorkflow(t, p.ctx, conn, backend.pg, backend.recorder, first)
			datetimeCurrentExec(t, p.ctx, conn, "COMMIT", 0)
			datetimeCurrentExec(t, p.ctx, conn, "SET TIME ZONE 'UTC'", 0)
			datetimeCurrentExec(t, p.ctx, conn, "BEGIN", 0)
			second := datetimeCurrentWitness(t, p.ctx, conn)
			if !second.After(first) {
				t.Fatal(fmt.Sprintf("fresh clock %v <= %v", second, first))
			}
			datetimeCurrentCheck(t, p.ctx, held, backend.pg, q, second, 0, 6, 1)
			datetimeCurrentExec(t, p.ctx, conn, "ROLLBACK", 0)
			stmt.Close()
			conn.Close()
			datetimeCurrentPeers(t, p, backend.db, backend.pg)
		})
	}
}
