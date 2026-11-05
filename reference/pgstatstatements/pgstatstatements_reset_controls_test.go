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

func TestReferencePgStatStatementsResetControls(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"pgss_reset_control_owner", "pgss_reset_control_user"})
	fixture.SchemaReferenceCleanup(t, "pgss_reset_moved")
	pgssCase(t, true, func(s *pgssScenario) {
		pgssResetSeed(s)
		pgssResetStoredControls(s)
	})
}

func pgssResetStoredControls(s *pgssScenario) {
	s.t.Helper()
	s.exec("CREATE ROLE pgss_reset_control_owner")
	s.exec("CREATE ROLE pgss_reset_control_user")
	s.exec("CREATE SCHEMA pgss_reset_moved")
	s.exec("GRANT USAGE ON SCHEMA pgss_part,pgss_reset_moved TO pgss_reset_control_owner,pgss_reset_control_user")
	// A real executable non-NULL call, not a strict/folded-away placeholder.
	before, info := pgssResetSnapshot(s), pgssResetInfo(s)
	s.exec("CREATE VIEW pgss_part.pgss_reset_stored AS SELECT pgss_part.pg_stat_statements_reset(userid=>4294967295::bigint) AS pg_stat_statements_reset")
	pgssResetEqual(s.t, pgssResetSnapshot(s), before)
	pgssResetEqual(s.t, pgssResetInfo(s), info)
	stored := "SELECT pg_stat_statements_reset FROM pgss_part.pgss_reset_stored"
	pgssResetScalar(s, s.observer, stored, false)
	pgssResetScalar(s, pgssResetHeld(s, stored), stored, false)
	s.exec("ALTER FUNCTION " + pgssResetSignature + " OWNER TO pgss_reset_control_owner")
	s.exec("ALTER FUNCTION " + pgssResetSignature + " RENAME TO reset_again")
	s.exec("ALTER FUNCTION pgss_part.reset_again(oid,oid,bigint,boolean) SET SCHEMA pgss_reset_moved")
	current := "SELECT pgss_reset_moved.reset_again(userid=>4294967295::bigint) AS pg_stat_statements_reset"
	s.exec("GRANT EXECUTE ON FUNCTION pgss_reset_moved.reset_again(oid,oid,bigint,boolean) TO pgss_reset_control_user")
	pgssResetScalar(s, s.observer, current, false)
	pgssResetScalar(s, pgssResetHeld(s, current), current, false)
	pgssResetScalar(s, s.observer, stored, false)
	pgssResetEqual(s.t, pgssResetSnapshot(s), before)
	pgssResetEqual(s.t, pgssResetInfo(s), info)
	var definition string
	if err := s.observer.QueryRowContext(s.p.Ctx, "SELECT pg_get_viewdef('pgss_part.pgss_reset_stored'::regclass)").Scan(&definition); err != nil {
		s.t.Fatal(err)
	}
	if !strings.Contains(definition, "pgss_reset_moved.reset_again") || !strings.Contains(definition, "userid") {
		s.t.Fatalf("stored reset lost current genuine name/written named argument: %s", definition)
	}
	// An ordinary canonical-name shadow is not the provider capability. Its
	// independent return proves the stored expression did not re-resolve by name.
	s.exec("CREATE FUNCTION pgss_part.pg_stat_statements_reset() RETURNS timestamptz LANGUAGE sql AS $$SELECT '2001-01-01 00:00:00+00'::timestamptz$$")
	s.both(fixture.Select("SELECT pgss_part.pg_stat_statements_reset()='2001-01-01 00:00:00+00'::timestamptz AS shadow", []string{"shadow"}, []string{"BOOL"}, [][]any{{true}}))
	pgssResetScalar(s, s.observer, stored, false)
	metadata := fixture.Select("SELECT p.pronargs::integer AS nargs,p.pronargdefaults::integer AS defaults,p.proargtypes::text AS args,p.proargnames::text AS names,r.rolname::text AS owner,has_function_privilege('pgss_reset_control_owner',p.oid,'EXECUTE') AS owner_execute,has_function_privilege('pgss_reset_control_user',p.oid,'EXECUTE') AS granted_execute,EXISTS(SELECT 1 FROM pg_init_privs i WHERE i.objoid=p.oid AND i.classoid='pg_proc'::regclass AND i.privtype='e') AS initial_acl FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace JOIN pg_roles r ON r.oid=p.proowner WHERE n.nspname='pgss_reset_moved' AND p.proname='reset_again'", []string{"nargs", "defaults", "args", "names", "owner", "owner_execute", "granted_execute", "initial_acl"}, []string{"INT4", "INT4", "TEXT", "TEXT", "TEXT", "BOOL", "BOOL", "BOOL"}, [][]any{{int64(4), int64(4), "26 26 20 16", "{userid,dbid,queryid,minmax_only}", "pgss_reset_control_owner", true, true, true}})
	s.both(metadata)
	pgssResetControlAdmission(s, current)
	if s.b.PG {
		return
	}
	pgssResetNativeControls(s, stored, current, metadata)
}

func pgssResetNativeControls(s *pgssScenario, stored, current string, metadata fixture.Step) {
	s.t.Helper()
	s.exec("CREATE DATABASE pgss_reset_control_peer")
	pool, _, recorder := fixture.DatabaseScenarioPool(s.t, s.p, false, "pgss_reset_control_peer", "")
	peer, err := pool.Conn(s.p.Ctx)
	if err != nil {
		s.t.Fatal(err)
	}
	defer peer.Close()
	for _, q := range []string{"SET pg_stat_statements.track_utility=off", "SET pg_stat_statements.track_planning=off", "SET pg_stat_statements.track=top"} {
		fixture.RunStep(s.t, s.p.Ctx, peer, recorder, fixture.Exec(q, 0))
	}
	const peerQ = "SELECT 79::bigint AS pgss_reset_peer"
	fixture.RunStep(s.t, s.p.Ctx, peer, recorder, fixture.Select(peerQ, []string{"pgss_reset_peer"}, []string{"INT8"}, [][]any{{int64(79)}}))
	peerCount := func() {
		s.both(fixture.Select("SELECT calls,rows FROM pgss_part.pg_stat_statements WHERE query LIKE 'SELECT % AS pgss_reset_peer'", []string{"calls", "rows"}, []string{"INT8", "INT8"}, [][]any{{int64(1), int64(1)}}))
	}
	peerCount()
	var extension, procedure int64
	identity := "SELECT e.oid::bigint,p.oid::bigint FROM pg_extension e JOIN pg_depend d ON d.refclassid='pg_extension'::regclass AND d.refobjid=e.oid JOIN pg_proc p ON d.classid='pg_proc'::regclass AND d.objid=p.oid JOIN pg_namespace n ON n.oid=p.pronamespace WHERE e.extname='pg_stat_statements' AND n.nspname='pgss_reset_moved' AND p.proname='reset_again'"
	if err := s.observer.QueryRowContext(s.p.Ctx, identity).Scan(&extension, &procedure); err != nil {
		s.t.Fatal(err)
	}
	before, info := pgssResetSnapshot(s), pgssResetInfo(s)
	heldCurrent, heldStored := pgssResetHeld(s, current), pgssResetHeld(s, stored)
	var dump bytes.Buffer
	if err := stuntgres.Dump(s.p.Ctx, s.p.Native, &dump); err != nil {
		s.t.Fatal(err)
	}
	unchanged := func() {
		pgssResetEqual(s.t, pgssResetSnapshot(s), before)
		pgssResetEqual(s.t, pgssResetInfo(s), info)
		s.both(metadata)
		pgssResetScalar(s, s.observer, stored, false)
		peerCount()
	}
	unsupported := "CREATE SCHEMA pgss_part; CREATE EXTENSION pg_stat_statements SCHEMA pgss_part VERSION 'missing';"
	unsupportedDiagnostic := fixture.ControlsReferenceDiagnostic("22023", `extension "pg_stat_statements" has no installation script nor update path for version "missing"`)
	fixture.AssertDiagnostic(s.t, stuntgres.Restore(s.p.Ctx, s.p.Native, strings.NewReader(unsupported)), &unsupportedDiagnostic)
	unchanged()
	readerFailure := errors.New("pgss reset restore reader failure")
	if err := stuntgres.Restore(s.p.Ctx, s.p.Native, fixture.CatalogControlFailingReader{Err: readerFailure}); !errors.Is(err, readerFailure) {
		s.t.Fatal("reset-provider reader failure:", err)
	}
	unchanged()
	bad := dump.String() + "\nCREATE TABLE public.pgss_reset_replay_failure(k integer); INSERT INTO public.pgss_reset_replay_failure VALUES(1/0);"
	want := fixture.ControlsReferenceDiagnostic("22012", "division by zero")
	fixture.AssertDiagnostic(s.t, stuntgres.Restore(s.p.Ctx, s.p.Native, strings.NewReader(bad)), &want)
	unchanged()
	ctx, cancel := context.WithCancel(s.p.Ctx)
	err = stuntgres.Restore(ctx, s.p.Native, &fixture.CatalogControlCancelReader{Cancel: cancel})
	cancel()
	if !errors.Is(err, context.Canceled) {
		s.t.Fatal("canceled reset-provider restore:", err)
	}
	unchanged()
	// Attach recording when the real cursor opens; the post-control assertion
	// consumes this same cursor and cannot create its missing operation later.
	recordedContext := context.WithValue(s.p.Ctx, fixture.RecorderKey{}, s.b.Recorder)
	heldRows, err := s.observer.QueryContext(recordedContext, "SELECT k FROM pgss_part.pgss_reset_data ORDER BY k")
	if err != nil {
		s.t.Fatal(err)
	}
	defer heldRows.Close()
	if err := stuntgres.Restore(s.p.Ctx, s.p.Native, bytes.NewReader(dump.Bytes())); err != nil {
		s.t.Fatal(err)
	}
	s.both(fixture.Select(identity, []string{"oid", "oid"}, []string{"INT8", "INT8"}, [][]any{{extension, procedure}}))
	check := func() {
		s.both(metadata)
		pgssResetControlAdmission(s, current)
		pgssResetScalar(s, s.observer, current, false)
		pgssResetScalar(s, heldCurrent, current, false)
		pgssResetScalar(s, s.observer, stored, false)
		pgssResetScalar(s, heldStored, stored, false)
		s.both(fixture.Select("SELECT count(*) AS n FROM pgss_part.pg_stat_statements WHERE dbid=(SELECT oid FROM pg_database WHERE datname=current_database())", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
		peerCount()
	}
	check()
	if err := stuntgres.Reset(s.p.Ctx, s.p.Native); err != nil {
		s.t.Fatal(err)
	}
	if err := stuntgres.Restore(s.p.Ctx, s.p.Native, bytes.NewReader(dump.Bytes())); err != nil {
		s.t.Fatal(err)
	}
	check()
	fixture.RunStep(s.t, s.p.Ctx, fixture.SequenceFrozenRows{Executor: s.observer, Rows: heldRows, Query: "SELECT k FROM pgss_part.pgss_reset_data ORDER BY k"}, s.b.Recorder, fixture.Select("SELECT k FROM pgss_part.pgss_reset_data ORDER BY k", []string{"k"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}}))
	connector, err := stuntgres.NewDriver().OpenConnector("pgss_reset_independent")
	if err != nil {
		s.t.Fatal(err)
	}
	fresh := sql.OpenDB(connector)
	defer fresh.Close()
	recorded, record := fixture.RecordedNativePool(s.t, connector)
	conn, err := recorded.Conn(s.p.Ctx)
	if err != nil {
		s.t.Fatal(err)
	}
	defer conn.Close()
	freshRun := func(step fixture.Step) { fixture.RunStep(s.t, s.p.Ctx, conn, record, step) }
	for _, q := range []string{"SET pg_stat_statements.track=none", "CREATE ROLE pgss_reset_control_owner", "CREATE ROLE pgss_reset_control_user", "CREATE ROLE pgss_reset_user", "CREATE ROLE pgss_reset_group"} {
		freshRun(fixture.Exec(q, 0))
	}
	if err := stuntgres.Restore(s.p.Ctx, fresh, bytes.NewReader(dump.Bytes())); err != nil {
		s.t.Fatal(err)
	}
	freshRun(metadata)
	freshRun(fixture.Prepared(metadata))
	for _, q := range []string{stored, current} {
		// A scalar subquery preserves FROM and named/default semantics.
		step := fixture.Select("SELECT ("+q+") IS NOT NULL AS valid", []string{"valid"}, []string{"BOOL"}, [][]any{{true}})
		freshRun(step)
		freshRun(fixture.Prepared(step))
	}
	freshRun(fixture.Select("SELECT count(*) AS n FROM pgss_part.pg_stat_statements", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
	freshRun(fixture.Exec("SET ROLE pgss_reset_control_user", 0))
	freshRun(fixture.Select("SELECT ("+current+") IS NOT NULL AS authorized", []string{"authorized"}, []string{"BOOL"}, [][]any{{true}}))
	freshRun(fixture.Exec("RESET ROLE", 0))
	peerCount()
}

func pgssResetControlAdmission(s *pgssScenario, current string) {
	s.t.Helper()
	s.both(fixture.Select("SELECT count(*) AS n FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname='pgss_part' AND c.relkind='v' AND c.relname IN ('pg_stat_statements','pg_stat_statements_info')", []string{"n"}, []string{"INT8"}, [][]any{{int64(2)}}))
	s.exec("SET ROLE pgss_reset_control_user")
	defer s.observer.ExecContext(s.p.Ctx, "RESET ROLE")
	pgssResetScalar(s, s.observer, current, false)
	pgssResetScalar(s, pgssResetHeld(s, current), current, false)
	s.exec("RESET ROLE")
}
