package core_test

import (
	"bytes"
	"fmt"
	"testing"
	"time"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/statistics"
	"stuntgres/internal/sqltype"
)

func subscriptionStatusCapture(t *testing.T, e *core.Engine, s *session.Session, oid uint32) core.Result {
	t.Helper()
	op, err := s.Acquire(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer op.Done()
	result, err := e.Execute(op, enginetest.Statement(t, fmt.Sprintf("SELECT * FROM pg_stat_subscription WHERE subid=%d::oid", oid)), 0, nil)
	if err != nil || result.Rows == nil {
		t.Fatal("admitted subscription source", err)
	}
	return result
}

func subscriptionStatusOwned(t *testing.T, result core.Result, name string) {
	t.Helper()
	defer result.Rows.Close()
	columns := result.Rows.Schema()
	if len(columns) != 11 {
		t.Fatal("eleven owned source columns", columns)
	}
	var row [11]sqltype.Value
	present, err := result.Rows.Next(t.Context(), row[:])
	if err != nil || present != (name != "") {
		t.Fatal("actual descriptor cardinality", present, name, err)
	}
	if present {
		for i, value := range row {
			if value.Type() != columns[i].Usage.Type || value.IsNull() != (i >= 2) {
				t.Fatal("typed source ownership/absence", i)
			}
		}
		got, err := row[1].Type().Output(t.Context(), row[1])
		if err != nil || got != name {
			t.Fatal("retained NAME", got, name, err)
		}
	}
	if more, err := result.Rows.Next(t.Context(), row[:]); more || err != nil {
		t.Fatal("source suffix", more, err)
	}
}

func TestPGStatSubscriptionProviderWitness(t *testing.T) {
	e := engine.New()
	s := enginetest.StartSession(t, e, nil)
	key := resetStatsCreate(t, e, s, "status_actual")
	before := e.Statistics.Read(key)
	if before != (statistics.Fact{Generation: before.Generation}) {
		t.Fatal("creation's real nine-counter zero baseline", before)
	}
	var retained statistics.Reader
	retained.Configure("snapshot", 1)
	retained.Read(&e.Statistics, key, time.Now())
	old := subscriptionStatusCapture(t, e, s, uint32(key.Object.Address.ObjectID))
	oldType := old.Rows.Schema()[1].Usage.Type
	for range 2 {
		subscriptionStatusOwned(t, subscriptionStatusCapture(t, e, s, uint32(key.Object.Address.ObjectID)), "status_actual")
	}
	statsFunctionExec(t, s, "CREATE TABLE status_dml(v int)", "INSERT INTO status_dml VALUES(37)", "SELECT * FROM status_dml", "ALTER SUBSCRIPTION status_actual SET (binary=true)", "ALTER SUBSCRIPTION status_actual RENAME TO status_renamed")
	if resetStatsKey(t, e, s, "status_renamed") != key || e.Statistics.Read(key) != before {
		t.Fatal("reads/DML/metadata changed subscription event authority")
	}
	// Enabled is desired catalog state, never an actual worker event.
	statsFunctionExec(t, s, "BEGIN", "ALTER SUBSCRIPTION status_renamed SET (slot_name=status_actual)", "ALTER SUBSCRIPTION status_renamed ENABLE")
	subscriptionStatusOwned(t, subscriptionStatusCapture(t, e, s, uint32(key.Object.Address.ObjectID)), "status_renamed")
	statsFunctionExec(t, s, "ROLLBACK")
	for _, query := range []string{
		"CREATE SUBSCRIPTION status_external CONNECTION '' PUBLICATION p",
		"ALTER SUBSCRIPTION status_renamed REFRESH PUBLICATION",
		"ALTER SUBSCRIPTION status_renamed SET PUBLICATION other WITH (refresh=true)",
	} {
		if err := statsFunctionSQL(t, s, query); err == nil {
			t.Fatal("external apply boundary unexpectedly succeeded", query)
		}
		subscriptionStatusOwned(t, subscriptionStatusCapture(t, e, s, uint32(key.Object.Address.ObjectID)), "status_renamed")
		if e.Statistics.Read(key) != before {
			t.Fatal("unsupported external work invented apply/error/conflict events")
		}
	}
	// Test-only nonzero cumulative facts distinguish counters from live workers.
	// Neither a real descriptor's history nor an orphan provider key grants PID,
	// worker-type, LSN/time or row-membership authority.
	nonzero := resetStatsSeed(e, key, 11)
	subscriptionStatusOwned(t, subscriptionStatusCapture(t, e, s, uint32(key.Object.Address.ObjectID)), "status_renamed")
	orphan := key
	orphan.Object.Address.ObjectID += 1000000
	e.Statistics.EnsureSubscription(orphan)
	orphanFact := resetStatsSeed(e, orphan, 31)
	subscriptionStatusOwned(t, subscriptionStatusCapture(t, e, s, uint32(orphan.Object.Address.ObjectID)), "")
	if e.Statistics.Read(key) != nonzero || e.Statistics.Read(orphan) != orphanFact {
		t.Fatal("inactive projection consumed/reset counter authority")
	}
	resetStatsExec(t, s, fmt.Sprintf("SELECT pg_stat_reset_subscription_stats(%d::oid)", key.Object.Address.ObjectID))
	resetStatsZero(t, e, key)
	afterReset := e.Statistics.Read(key)
	for range 2 {
		subscriptionStatusOwned(t, subscriptionStatusCapture(t, e, s, uint32(key.Object.Address.ObjectID)), "status_renamed")
	}
	if e.Statistics.Read(key) != afterReset || retained.Read(&e.Statistics, key, time.Now()) != before {
		t.Fatal("status reads mutated current/reset or retained provider fact")
	}
	statsFunctionExec(t, s, "BEGIN", "DROP SUBSCRIPTION status_renamed")
	subscriptionStatusOwned(t, subscriptionStatusCapture(t, e, s, uint32(key.Object.Address.ObjectID)), "")
	statsFunctionExec(t, s, "ROLLBACK")
	subscriptionStatusOwned(t, subscriptionStatusCapture(t, e, s, uint32(key.Object.Address.ObjectID)), "status_renamed")
	if e.Statistics.Read(key) != afterReset {
		t.Fatal("rollback retired live provider key")
	}
	var late statistics.Writer
	late.Add(&e.Statistics, key, resetStatsVector(21))
	statsFunctionExec(t, s, "DROP SUBSCRIPTION status_renamed")
	late.Flush(&e.Statistics, time.Now(), true)
	resetStatsAbsent(t, e, key)
	subscriptionStatusOwned(t, subscriptionStatusCapture(t, e, s, uint32(key.Object.Address.ObjectID)), "")
	fresh := resetStatsCreate(t, e, s, "status_actual")
	if fresh == key {
		t.Fatal("recreation reused retired subscription identity")
	}
	// Retain actual source schema/backing across rename, drop and replacement.
	var dump bytes.Buffer
	if err := engine.Dump(t.Context(), e, &dump); err != nil {
		t.Fatal(err)
	}
	if err := engine.Restore(t.Context(), e, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	restored := resetStatsKey(t, e, s, "status_actual")
	if restored == fresh || restored.Object.Epoch == fresh.Object.Epoch || old.Rows.Schema()[1].Usage.Type != oldType {
		t.Fatal("replacement failed to separate live and retained generations")
	}
	subscriptionStatusOwned(t, old, "status_actual")
	subscriptionStatusOwned(t, subscriptionStatusCapture(t, e, s, uint32(restored.Object.Address.ObjectID)), "status_actual")
	resetStatsAbsent(t, e, fresh)
	if got := e.Statistics.Read(restored); got != (statistics.Fact{Generation: got.Generation}) {
		t.Fatal("replacement inherited fabricated event history", got)
	}
	statsFunctionExec(t, s, "BEGIN")
	aborted := resetStatsCreate(t, e, s, "status_aborted")
	subscriptionStatusOwned(t, subscriptionStatusCapture(t, e, s, uint32(aborted.Object.Address.ObjectID)), "status_aborted")
	statsFunctionExec(t, s, "ROLLBACK")
	resetStatsAbsent(t, e, aborted)
	subscriptionStatusOwned(t, subscriptionStatusCapture(t, e, s, uint32(aborted.Object.Address.ObjectID)), "")
	if err := engine.Reset(t.Context(), e); err != nil {
		t.Fatal(err)
	}
	resetStatsAbsent(t, e, restored)
	subscriptionStatusOwned(t, subscriptionStatusCapture(t, e, s, uint32(restored.Object.Address.ObjectID)), "")
}
