package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferencePgStatStatementsControls(t *testing.T) {
	pgssCase(t, true, func(s *pgssScenario) {
		t := s.t
		pgssCounterSetup(s)
		s.exec("CREATE TABLE pgss_part.pgss_control_data(k integer)")
		s.run(fixture.Exec("INSERT INTO pgss_part.pgss_control_data VALUES(17)", 1))
		s.exec("CREATE VIEW pgss_part.pgss_control_consumer AS SELECT query,calls FROM pgss_part.pg_stat_statements(false)")
		q := "SELECT k AS pgss_control_measured FROM pgss_part.pgss_control_data"
		s.work(fixture.Select(q, []string{"pgss_control_measured"}, []string{"INT4"}, [][]any{{int64(17)}}))
		s.counts(q, 1, 1, 1)
		pgssCounterCheck(s, q, 1, 1, pgssCounterWant{read: 1, pgShared: true, serial: true})
		mutation := "UPDATE pgss_part.pgss_control_data SET k=k"
		s.work(fixture.Prepared(fixture.Exec(mutation, 1)))
		pgssCounterCheck(s, mutation, 1, 1, pgssCounterWant{anySharedSplit: true, sharedRequests: 1, dirty: 1, records: 1, bytes: 80, pgShared: true, pgWAL: true, serial: true})
		pgssProviderRelationships(s)
		s.both(fixture.Select("SELECT * FROM pgss_part.pgss_control_consumer WHERE false", []string{"query", "calls"}, []string{"TEXT", "INT8"}, nil))
		if s.b.PG {
			return
		} // Host replacement has no SQL/PostgreSQL counterpart.

		// Replacing the local database must not clear the cluster's peer db.
		s.exec("CREATE DATABASE pgss_control_peer")
		pool, _, recorder := fixture.DatabaseScenarioPool(t, s.p, false, "pgss_control_peer", "")
		peer, err := pool.Conn(s.p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer peer.Close()
		peerStep := func(step fixture.Step) { fixture.RunStep(t, s.p.Ctx, peer, recorder, step) }
		peerStep(fixture.Exec("SET pg_stat_statements.track_utility = off", 0))
		peerStep(fixture.Exec("CREATE TABLE public.pgss_control_peer_data(k integer)", 0))
		peerStep(fixture.Exec("INSERT INTO public.pgss_control_peer_data VALUES(31)", 1))
		peerQ := "SELECT k AS pgss_control_peer_value FROM public.pgss_control_peer_data"
		peerStep(fixture.Select(peerQ, []string{"pgss_control_peer_value"}, []string{"INT4"}, [][]any{{int64(31)}}))
		peerCount := func() {
			s.both(fixture.Select("SELECT calls,rows FROM pgss_part.pg_stat_statements WHERE query=$1", []string{"calls", "rows"}, []string{"INT8", "INT8"}, [][]any{{int64(1), int64(1)}}, peerQ))
		}
		peerCount()
		history, info := pgssResetSnapshot(s), pgssResetInfo(s)
		var extension, function int64
		identity := "SELECT e.oid::bigint,p.oid::bigint FROM pg_extension e JOIN pg_namespace n ON n.oid=e.extnamespace JOIN pg_proc p ON p.pronamespace=n.oid WHERE e.extname='pg_stat_statements' AND p.proname='pg_stat_statements'"
		if err := s.observer.QueryRowContext(s.p.Ctx, identity).Scan(&extension, &function); err != nil {
			t.Fatal(err)
		}
		var dump bytes.Buffer
		if err := stuntgres.Dump(s.p.Ctx, s.p.Native, &dump); err != nil {
			t.Fatal(err)
		}
		unchanged := func() {
			s.counts(q, 1, 1, 1)
			pgssResetEqual(t, pgssResetSnapshot(s), history)
			pgssResetEqual(t, pgssResetInfo(s), info)
			s.both(fixture.Select(identity, []string{"oid", "oid"}, []string{"INT8", "INT8"}, [][]any{{extension, function}}))
			s.both(fixture.Select("SELECT k FROM pgss_part.pgss_control_data", []string{"k"}, []string{"INT4"}, [][]any{{int64(17)}}))
			pgssProviderRelationships(s)
			peerCount()
		}
		for _, c := range []struct {
			input      string
			diagnostic fixture.Diagnostic
		}{
			{"CREATE SCHEMA pgss_part; CREATE EXTENSION pg_stat_statements SCHEMA pgss_part VERSION 'missing';", fixture.ControlsReferenceDiagnostic("22023", `extension "pg_stat_statements" has no installation script nor update path for version "missing"`)},
			{"SET SESSION AUTHORIZATION pgss_control_missing;", fixture.ControlsReferenceDiagnostic("22023", `role "pgss_control_missing" does not exist`)},
			{dump.String() + "\nCREATE TABLE pgss_part.pgss_control_late(k integer); INSERT INTO pgss_part.pgss_control_late VALUES(1/0);", fixture.ControlsReferenceDiagnostic("22012", "division by zero")},
		} {
			err := stuntgres.Restore(s.p.Ctx, s.p.Native, strings.NewReader(c.input))
			fixture.AssertDiagnostic(t, err, &c.diagnostic)
			unchanged()
		}
		ctx, cancel := context.WithCancel(s.p.Ctx)
		err = stuntgres.Restore(ctx, s.p.Native, &fixture.CatalogControlCancelReader{Cancel: cancel})
		cancel()
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled staged restore: %v", err)
		}
		unchanged()
		failure := errors.New("pgss restore reader failure")
		if err := stuntgres.Restore(s.p.Ctx, s.p.Native, fixture.CatalogControlFailingReader{Err: failure}); !errors.Is(err, failure) {
			t.Fatal(err)
		}
		unchanged()
		// Compatible live replay keeps provider identity, but clears runtime
		// history only for the replaced database.
		if err := stuntgres.Restore(s.p.Ctx, s.p.Native, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}
		s.both(fixture.Select(identity, []string{"oid", "oid"}, []string{"INT8", "INT8"}, [][]any{{extension, function}}))
		s.counts(q, 0, 0, 0)
		peerCount()
		pgssControlRoundtrip(s, "pgss_part.pgss_control_data", []int64{17})
		peerCount()
		s.both(fixture.Select("SELECT * FROM pgss_part.pgss_control_consumer WHERE false", []string{"query", "calls"}, []string{"TEXT", "INT8"}, nil))
	})
	t.Run("symbolic_owner", pgssNamedOwnerControls)
}

func pgssProviderRelationships(s *pgssScenario) {
	s.t.Helper()
	q := "SELECT e.extversion,n.nspname::text AS schema,p.proowner=e.extowner AS owned,d.deptype='e' AS member,p.oid>0 AND e.oid>0 AS identified FROM pg_extension e JOIN pg_namespace n ON n.oid=e.extnamespace JOIN pg_proc p ON p.pronamespace=n.oid JOIN pg_depend d ON d.classid='pg_proc'::regclass AND d.objid=p.oid AND d.refclassid='pg_extension'::regclass AND d.refobjid=e.oid WHERE e.extname='pg_stat_statements' AND p.proname='pg_stat_statements'"
	s.both(fixture.Select(q, []string{"extversion", "schema", "owned", "member", "identified"}, []string{"TEXT", "TEXT", "BOOL", "BOOL", "BOOL"}, [][]any{{"1.12", "pgss_part", true, true, true}}))
}

// Used by Controls and FeatureSmoke. Persistent SQL state is replayed through
// public controls; the collector, active sessions and held results are not.
func pgssControlRoundtrip(s *pgssScenario, table string, values []int64) {
	s.t.Helper()
	if s.b.PG {
		return
	}
	ctx := context.WithValue(s.p.Ctx, fixture.RecorderKey{}, s.b.Recorder)
	q := "SELECT k FROM " + table + " ORDER BY k"
	want := make([][]any, len(values))
	for i, v := range values {
		want[i] = []any{v}
	}
	s.work(fixture.Select(q, []string{"k"}, []string{"INT4"}, want))
	s.counts(q, 1, 1, int64(len(values)))
	held, err := s.observer.QueryContext(ctx, q)
	if err != nil {
		s.t.Fatal(err)
	}
	defer held.Close()
	s.work(fixture.Exec("BEGIN", 0))
	defer s.worker.ExecContext(s.p.Ctx, "ROLLBACK")
	s.work(fixture.Exec("CREATE TABLE pgss_part.pgss_control_private(k integer)", 0))
	var dump bytes.Buffer
	if err := stuntgres.Dump(s.p.Ctx, s.p.Native, &dump); err != nil {
		s.t.Fatal(err)
	}
	for _, forbidden := range []string{"pgss_control_private", "pgss_control_measured", "pgss_smoke_measured", "pgss_smoke_held"} {
		if strings.Contains(dump.String(), forbidden) {
			s.t.Fatalf("dump serialized private/runtime state %q", forbidden)
		}
	}
	s.work(fixture.Exec("ROLLBACK", 0))
	if err := stuntgres.Reset(s.p.Ctx, s.p.Native); err != nil {
		s.t.Fatal(err)
	}
	s.both(fixture.Select("SELECT count(*) AS n FROM pg_extension WHERE extname='pg_stat_statements'", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
	if err := stuntgres.Restore(s.p.Ctx, s.p.Native, bytes.NewReader(dump.Bytes())); err != nil {
		s.t.Fatal(err)
	}
	pgssProviderRelationships(s)
	s.both(fixture.Select("SELECT count(*) AS n FROM pgss_part.pg_stat_statements WHERE query LIKE $1 AND dbid=(SELECT oid FROM pg_database WHERE datname=current_database())", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}, "%"+table+"%"))
	s.both(fixture.Select(q, []string{"k"}, []string{"INT4"}, want))
	fixture.RunStep(s.t, s.p.Ctx, fixture.SequenceFrozenRows{Executor: s.observer, Rows: held, Query: q}, s.b.Recorder, fixture.Select(q, []string{"k"}, []string{"INT4"}, want))
	s.both(fixture.Select("SELECT * FROM pgss_part.pg_stat_statements(NULL::boolean)", pgssNames, pgssTypes, nil))
	// Fresh direct and prepared routes execute the restored provider.
	step := fixture.Select(q, []string{"k"}, []string{"INT4"}, want)
	s.work(step)
	s.work(fixture.Prepared(step))
	s.counts(q, 1, 2, int64(2*len(values)))
	pgssCounterCheck(s, q, 2, int64(2*len(values)), pgssCounterWant{anySharedSplit: true, sharedRequests: 2, pgShared: true, serial: true})
	mutation := "UPDATE " + table + " SET k=k"
	s.work(fixture.Prepared(fixture.Exec(mutation, int64(len(values)))))
	pgssCounterCheck(s, mutation, 1, int64(len(values)), pgssCounterWant{anySharedSplit: true, sharedRequests: 1, dirty: int64(len(values)), records: int64(len(values)), bytes: int64(80 * len(values)), pgShared: true, pgWAL: true, serial: true})
	connector, err := stuntgres.NewDriver().OpenConnector("pgss_control_independent")
	if err != nil {
		s.t.Fatal(err)
	}
	fresh := sql.OpenDB(connector)
	defer fresh.Close()
	recorded, recorder := fixture.RecordedNativePool(s.t, connector)
	c, err := recorded.Conn(s.p.Ctx)
	if err != nil {
		s.t.Fatal(err)
	}
	defer c.Close()
	fixture.RunStep(s.t, s.p.Ctx, c, recorder, fixture.Select("SELECT count(*) AS n FROM pg_extension WHERE extname='pg_stat_statements'", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
	if err := stuntgres.Restore(s.p.Ctx, fresh, bytes.NewReader(dump.Bytes())); err != nil {
		s.t.Fatal(err)
	}
	fixture.RunStep(s.t, s.p.Ctx, c, recorder, fixture.Exec("SET pg_stat_statements.track = none", 0))
	fixture.RunStep(s.t, s.p.Ctx, c, recorder, fixture.Select(q, []string{"k"}, []string{"INT4"}, want))
	fixture.RunStep(s.t, s.p.Ctx, c, recorder, fixture.Select("SELECT count(*) AS n FROM pgss_part.pg_stat_statements WHERE query LIKE $1", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}, "%"+table+"%"))
}
