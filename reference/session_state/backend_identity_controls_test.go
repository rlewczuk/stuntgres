package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"reflect"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func identityResetStable(t *testing.T, p *fixture.Pair, b fixture.Backend, o, admin *sql.Conn, before []any) {
	t.Helper()
	pid := before[1].(int64)
	for _, q := range []string{"SELECT pg_stat_reset()", "SELECT pg_stat_reset_shared('io')", "SELECT pg_stat_reset_shared('wal')"} {
		activityExec(t, p, admin, q)
		identityEqual(t, identityLookup(t, p, b, o, before[0]), before)
		statActivityClear(t, p, o)
		identityEqual(t, identityForPID(t, identitySet(t, p, b, o), pid), before)
	}
	backendReset(t, p, admin, pid)
	identityEqual(t, identityLookup(t, p, b, o, before[0]), before)
	statActivityClear(t, p, o)
	identityEqual(t, identityForPID(t, identitySet(t, p, b, o), pid), before)
}

func TestReferenceBackendIdentityControls(t *testing.T) {
	activityScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, o *sql.Conn) {
		admin := statActivityConn(t, p, b)
		peer := statActivityConn(t, p, b)
		activityExec(t, p, peer, "SET application_name='bi_control_peer'")
		before := identityMapped(t, p, b, o, peer)
		ownerBefore := identityMapped(t, p, b, o, o)
		s, err := o.PrepareContext(p.Ctx, identityLookupSQL)
		if err != nil {
			t.Fatal(err)
		}
		defer s.Close()
		e := fixture.HeldStatement{Executor: o, Statement: s, SQL: identityLookupSQL}
		identityResetStable(t, p, b, o, admin, before)
		activityExec(t, p, o, "BEGIN")
		defer o.ExecContext(context.Background(), "ROLLBACK")
		identityEqual(t, identityLookup(t, p, b, e, before[0]), before)
		backendReset(t, p, admin, before[1].(int64))
		activityExec(t, p, admin, "SELECT pg_stat_reset()")
		identityEqual(t, identityLookup(t, p, b, e, before[0]), before)
		identityEqual(t, identityForPID(t, identitySet(t, p, b, o), before[1].(int64)), before)
		activityExec(t, p, o, "ROLLBACK")
		identityEqual(t, identityLookup(t, p, b, e, before[0]), before)
		activityExec(t, p, o, "CREATE TABLE bi_control_sentinel(v integer)")
		activityExec(t, p, o, "INSERT INTO bi_control_sentinel VALUES(37)")
		// PostgreSQL has no native Dump/Reset/Restore API. SQL phases above are
		// still shared; this explicit control phase requires completed native proof.
		if b.PG {
			return
		}
		retained, err := s.QueryContext(p.Ctx, before[0])
		if err != nil {
			t.Fatal(err)
		}
		defer retained.Close()
		var dump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		old := identityRows(t, retained, false, identityNames, identityTupleTypes)
		if len(old) != 1 {
			t.Fatal("retained eager scalar rows", old)
		}
		identityEqual(t, old[0], before)
		identityEqual(t, identityLookup(t, p, b, e, before[0]), before)
		identityEqual(t, identityMapped(t, p, b, o, peer), before)
		identityEqual(t, identityMapped(t, p, b, o, o), ownerBefore)
		if n := backendCount(t, p, o, "SELECT count(*) FROM pg_class WHERE relname='bi_control_sentinel'"); n != 0 {
			t.Fatal("Reset did not clear user catalog", n)
		}
		retainedRestore, err := s.QueryContext(p.Ctx, before[0])
		if err != nil {
			t.Fatal(err)
		}
		defer retainedRestore.Close()
		if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}
		oldRestore := identityRows(t, retainedRestore, false, identityNames, identityTupleTypes)
		if len(oldRestore) != 1 {
			t.Fatal("Restore lost retained scalar rows", oldRestore)
		}
		identityEqual(t, oldRestore[0], before)
		if n := backendCount(t, p, o, "SELECT v FROM bi_control_sentinel"); n != 37 {
			t.Fatal("real data roundtrip", n)
		}
		identityEqual(t, identityLookup(t, p, b, e, before[0]), before)
		identityEqual(t, identityMapped(t, p, b, o, peer), before)
		identityEqual(t, identityMapped(t, p, b, o, o), ownerBefore)
		// Restore in an independent Driver creates genuine destination births;
		// neither source peer activity nor provider identity is serialized.
		d := stuntgres.NewDriver()
		connector, err := d.OpenConnector("backend-identity-control-destination")
		if err != nil {
			t.Fatal(err)
		}
		destination := sql.OpenDB(connector)
		defer destination.Close()
		target, err := destination.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer target.Close()
		destBefore := identityMapped(t, p, b, target, target)
		if err := stuntgres.Restore(p.Ctx, destination, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}
		identityEqual(t, identityMapped(t, p, b, target, target), destBefore)
		if backendCount(t, p, target, "SELECT v FROM bi_control_sentinel") != 37 || backendCount(t, p, target, "SELECT count(*) FROM pg_stat_activity WHERE application_name='bi_control_peer'") != 0 {
			t.Fatal("destination imported monitoring or lost data")
		}
		if !reflect.DeepEqual(identityLookup(t, p, b, e, before[0]), before) {
			t.Fatal("independent Driver affected source identity")
		}
	})
}
