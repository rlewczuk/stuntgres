package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"testing"
	"time"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferencePgStatStatementsCountersControls(t *testing.T) {
	pgssCase(t, true, func(s *pgssScenario) {
		pgssCounterSetup(s)
		table := "pgss_part.pgss_counter_controls"
		s.exec("CREATE TABLE " + table + "(k integer,v text)")
		insert := "INSERT INTO " + table + " VALUES($1::integer,$2::text),($3::integer,$4::text)"
		s.work(fixture.Exec(insert, 2, int64(1), "a", int64(2), "b"))
		pgssCounterCheck(s, insert, 1, 2, pgssCounterWant{dirty: 2, records: 2, bytes: 144, pgWAL: true, serial: true})
		q := "SELECT k,v FROM " + table + " ORDER BY k"
		step := fixture.Select(q, []string{"k", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "a"}, {int64(2), "b"}})
		s.work(fixture.Prepared(step))
		pgssCounterCheck(s, q, 1, 2, pgssCounterWant{read: 1, pgShared: true, serial: true})
		if !s.b.PG { // Host controls do not have a PostgreSQL SQL analogue.
			s.exec("CREATE DATABASE pgss_counter_control_peer")
			pool, _, recorder := fixture.DatabaseScenarioPool(s.t, s.p, false, "pgss_counter_control_peer", "")
			peer, err := pool.Conn(s.p.Ctx)
			if err != nil {
				s.t.Fatal(err)
			}
			defer peer.Close()
			run := func(step fixture.Step) { fixture.RunStep(s.t, s.p.Ctx, peer, recorder, step) }
			run(fixture.Exec("SET pg_stat_statements.track_utility=off", 0))
			run(fixture.Exec("CREATE TABLE public.pgss_counter_peer(k integer,v text)", 0))
			run(fixture.Exec("INSERT INTO public.pgss_counter_peer VALUES(1,'peer')", 1))
			peerQ := "SELECT k FROM public.pgss_counter_peer"
			run(fixture.Select(peerQ, []string{"k"}, []string{"INT4"}, [][]any{{int64(1)}}))
			peerSnapshot := pgssResetRead(s, "SELECT * FROM pgss_part.pg_stat_statements WHERE dbid=(SELECT oid FROM pg_database WHERE datname='pgss_counter_control_peer') ORDER BY queryid,toplevel")
			if len(peerSnapshot) < 2 {
				s.t.Fatal("peer has no genuine read/mutation history")
			}
			pgssCounterRoundtrip(s, table, step, 288)
			pgssResetEqual(s.t, pgssResetRead(s, "SELECT * FROM pgss_part.pg_stat_statements WHERE dbid=(SELECT oid FROM pg_database WHERE datname='pgss_counter_control_peer') ORDER BY queryid,toplevel"), peerSnapshot)
		}
	})
}

// Keep native controls inside the public dual-mode scenario. Persistent rows and
// installed identities replay; collected vectors and held live handles do not.
func pgssCounterRoundtrip(s *pgssScenario, table string, data fixture.Step, updateBytes int64) {
	s.t.Helper()
	if s.b.PG {
		return
	}
	ctx := context.WithValue(s.p.Ctx, fixture.RecorderKey{}, s.b.Recorder)
	held, err := s.observer.QueryContext(ctx, data.Query)
	if err != nil {
		s.t.Fatal(err)
	}
	defer held.Close()
	var dump bytes.Buffer
	if err := stuntgres.Dump(s.p.Ctx, s.p.Native, &dump); err != nil {
		s.t.Fatal(err)
	}
	if err := stuntgres.Restore(s.p.Ctx, s.p.Native, bytes.NewReader(dump.Bytes())); err != nil {
		s.t.Fatal(err)
	}
	s.counts("%"+table+"%", 0, 0, 0)
	pgssProviderRelationships(s)
	fixture.RunStep(s.t, s.p.Ctx, fixture.SequenceFrozenRows{Executor: s.observer, Rows: held, Query: data.Query}, s.b.Recorder, data)
	// Fresh direct and prepared worker execution, not observer-only replay.
	s.work(data)
	s.work(fixture.Prepared(data))
	pgssCounterCheck(s, data.Query, 2, int64(2*len(data.Rows)), pgssCounterWant{anySharedSplit: true, sharedRequests: 2, pgShared: true, serial: true})
	mutation := "UPDATE " + table + " SET k=k"
	s.work(fixture.Prepared(fixture.Exec(mutation, int64(len(data.Rows)))))
	pgssCounterCheck(s, mutation, 1, int64(len(data.Rows)), pgssCounterWant{anySharedSplit: true, sharedRequests: 1, dirty: int64(len(data.Rows)), records: int64(len(data.Rows)), bytes: updateBytes, pgShared: true, pgWAL: true, serial: true})
	if err := stuntgres.Reset(s.p.Ctx, s.p.Native); err != nil {
		s.t.Fatal(err)
	}
	s.both(fixture.Select("SELECT count(*) AS n FROM pg_extension WHERE extname='pg_stat_statements'", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
	if err := stuntgres.Restore(s.p.Ctx, s.p.Native, bytes.NewReader(dump.Bytes())); err != nil {
		s.t.Fatal(err)
	}
	s.both(data)
	s.counts("%"+table+"%", 0, 0, 0)
	s.work(fixture.Prepared(data))
	pgssCounterCheck(s, data.Query, 1, int64(len(data.Rows)), pgssCounterWant{anySharedSplit: true, sharedRequests: 1, pgShared: true, serial: true})
	connector, err := stuntgres.NewDriver().OpenConnector("pgss_counter_fresh_driver")
	if err != nil {
		s.t.Fatal(err)
	}
	fresh := sql.OpenDB(connector)
	defer fresh.Close()
	pool, recorder := fixture.RecordedNativePool(s.t, connector)
	c, err := pool.Conn(s.p.Ctx)
	if err != nil {
		s.t.Fatal(err)
	}
	defer c.Close()
	if err := stuntgres.Restore(s.p.Ctx, fresh, bytes.NewReader(dump.Bytes())); err != nil {
		s.t.Fatal(err)
	}
	run := func(step fixture.Step) { fixture.RunStep(s.t, s.p.Ctx, c, recorder, step) }
	// Utility collection decides enablement before SET changes track. That
	// genuine SET row is unrelated to the target's pre-Dump history.
	run(fixture.Exec("SET pg_stat_statements.track='none'", 0))
	var freshReset time.Time
	if err := c.QueryRowContext(s.p.Ctx, "SELECT stats_reset FROM pgss_part.pg_stat_statements_info").Scan(&freshReset); err != nil {
		s.t.Fatal(err)
	}
	run(data)
	run(fixture.Prepared(data))
	run(fixture.Select("SELECT count(*) AS n FROM pgss_part.pg_stat_statements WHERE query LIKE $1 AND userid=(SELECT oid FROM pg_roles WHERE rolname=current_user) AND dbid=(SELECT oid FROM pg_database WHERE datname=current_database())", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}, "%"+table+"%"))
	run(fixture.Select("SELECT dealloc,stats_reset=$1::timestamptz AS unchanged FROM pgss_part.pg_stat_statements_info", []string{"dealloc", "unchanged"}, []string{"INT8", "BOOL"}, [][]any{{int64(0), true}}, freshReset))
	run(fixture.Select("SELECT p.proowner=e.extowner AS owned,d.deptype='e' AS member FROM pg_extension e JOIN pg_namespace n ON n.oid=e.extnamespace JOIN pg_proc p ON p.pronamespace=n.oid JOIN pg_depend d ON d.classid='pg_proc'::regclass AND d.objid=p.oid AND d.refclassid='pg_extension'::regclass AND d.refobjid=e.oid WHERE e.extname='pg_stat_statements' AND p.proname='pg_stat_statements'", []string{"owned", "member"}, []string{"BOOL", "BOOL"}, [][]any{{true, true}}))
}
