package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceDblinkConnectionControls(t *testing.T) {
	t.Run("persistent-objects", func(t *testing.T) { connectionCase(t, false, connectionPersistentControls) })
	t.Run("live-resources", func(t *testing.T) { connectionCase(t, true, connectionLiveControls) })
	t.Run("binding-replay", func(t *testing.T) { connectionCase(t, false, connectionBindingControls) })
	t.Run("binding-replay-live", func(t *testing.T) { connectionCase(t, true, connectionBindingLiveControls) })
}

func connectionPersistentControls(s *connectionScenario) {
	t := s.t
	s.exec("CREATE ROLE dbc_owner")
	s.exec("CREATE ROLE dbc_reader")
	s.exec("CREATE SCHEMA dbc_moved")
	s.exec("GRANT USAGE ON SCHEMA dbc_ext,dbc_moved TO dbc_reader")
	s.exec("CREATE TABLE dbc_marker(v integer)")
	fixture.RunStep(t, s.p.Ctx, s.c, s.b.Recorder, fixture.Exec("INSERT INTO dbc_marker VALUES(73)", 1))
	s.exec("CREATE VIEW dbc_ext.saved_connections AS SELECT dbc_ext.dblink_get_connections() AS value")
	s.exec("CREATE FUNCTION dbc_ext.saved_connect(text) RETURNS text LANGUAGE SQL AS 'SELECT dbc_ext.dblink_connect($1)'")
	s.exec("ALTER FUNCTION dbc_ext.dblink_get_connections() RENAME TO saved_list")
	s.exec("ALTER FUNCTION dbc_ext.saved_list() SET SCHEMA dbc_moved")
	s.exec("ALTER FUNCTION dbc_moved.saved_list() OWNER TO dbc_owner")
	s.exec("REVOKE EXECUTE ON FUNCTION dbc_moved.saved_list() FROM PUBLIC")
	s.exec("GRANT EXECUTE ON FUNCTION dbc_moved.saved_list() TO dbc_reader")
	s.exec("ALTER FUNCTION dbc_moved.saved_list() SECURITY DEFINER")
	s.exec("ALTER FUNCTION dbc_moved.saved_list() SECURITY INVOKER")
	s.exec("ALTER FUNCTION dbc_moved.saved_list() SET application_name='discarded'")
	s.exec("ALTER FUNCTION dbc_moved.saved_list() RESET application_name")
	s.exec("ALTER FUNCTION dbc_moved.saved_list() RESET ALL")
	s.exec("ALTER FUNCTION dbc_ext.dblink_connect_u(text) SECURITY INVOKER")
	s.exec("ALTER FUNCTION dbc_moved.saved_list() SET application_name='dbc_saved_config'")
	checks := []fixture.Step{
		fixture.Select("SELECT v FROM dbc_marker", []string{"v"}, []string{"INT4"}, [][]any{{int64(73)}}),
		fixture.Select("SELECT value FROM dbc_ext.saved_connections", []string{"value"}, []string{"_TEXT"}, [][]any{{nil}}),
		fixture.Select("SELECT r.rolname::text AS owner,p.prosrc AS symbol,p.prorettype::bigint AS result,has_function_privilege('dbc_reader',p.oid,'EXECUTE') AS reader FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace JOIN pg_roles r ON r.oid=p.proowner WHERE n.nspname='dbc_moved' AND p.proname='saved_list'", []string{"owner", "symbol", "result", "reader"}, []string{"TEXT", "TEXT", "INT8", "BOOL"}, [][]any{{"dbc_owner", "dblink_get_connections", int64(1009), true}}),
		fixture.Select("SELECT p.proconfig[1] AS setting FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace WHERE n.nspname='dbc_moved' AND p.proname='saved_list'", []string{"setting"}, []string{"TEXT"}, [][]any{{"application_name=dbc_saved_config"}}),
		fixture.Select("SELECT p.prosecdef AS definer FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace WHERE n.nspname='dbc_ext' AND p.proname='dblink_connect_u' AND p.pronargs=1", []string{"definer"}, []string{"BOOL"}, [][]any{{false}}),
		fixture.Select("SELECT count(*) AS n FROM pg_depend d JOIN pg_rewrite rw ON rw.oid=d.objid JOIN pg_class v ON v.oid=rw.ev_class JOIN pg_proc p ON p.oid=d.refobjid JOIN pg_namespace n ON n.oid=p.pronamespace WHERE d.classid=2618 AND d.refclassid=1255 AND v.relname='saved_connections' AND n.nspname='dbc_moved' AND p.proname='saved_list'", []string{"n"}, []string{"INT8"}, [][]any{{int64(1)}}),
		connectionText("SELECT dbc_ext.saved_connect(NULL::text) AS value", nil),
	}
	check := func(x fixture.Executor, r *fixture.Recorder) {
		for _, step := range checks {
			fixture.RunStep(t, s.p.Ctx, x, r, step)
			fixture.RunStep(t, s.p.Ctx, x, r, fixture.Prepared(step))
		}
	}
	check(s.c, s.b.Recorder)
	if len(s.provider.Snapshot()) != 0 {
		t.Fatal("stored recipe/NULL/prepare opened provider")
	}
	if s.b.PG {
		return
	}
	s.exec("CREATE TEMP TABLE dbc_session_keep(v integer)")
	fixture.RunStep(t, s.p.Ctx, s.c, s.b.Recorder, fixture.Exec("INSERT INTO dbc_session_keep VALUES(82)", 1))
	s.exec("SET application_name='dbc_controls_keep'")
	old, err := s.c.QueryContext(s.p.Ctx, "SELECT v FROM dbc_marker")
	if err != nil {
		t.Fatal(err)
	}
	defer old.Close()
	columns, err := old.ColumnTypes()
	if err != nil || len(columns) != 1 || columns[0].DatabaseTypeName() != "INT4" {
		t.Fatal("old descriptor", columns, err)
	}
	dump := fixture.ExtensionDump(t, s.p.Ctx, s.p.Native)
	if len(s.provider.Snapshot()) != 0 {
		t.Fatal("Dump executed volatile recipe")
	}
	if err := stuntgres.Reset(s.p.Ctx, s.p.Native); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(s.p.Ctx, s.p.Native, strings.NewReader(dump)); err != nil {
		t.Fatal(err)
	}
	check(s.c, s.b.Recorder)
	s.run(fixture.Select("SELECT v FROM dbc_session_keep", []string{"v"}, []string{"INT4"}, [][]any{{int64(82)}}))
	s.run(fixture.Select("SELECT current_setting('application_name') AS value", []string{"value"}, []string{"TEXT"}, [][]any{{"dbc_controls_keep"}}))
	if !old.Next() {
		t.Fatal("old finalized row lost", old.Err())
	}
	var v int64
	if err := old.Scan(&v); err != nil || v != 73 {
		t.Fatal("old finalized value", v, err)
	}
	if old.Next() || old.Err() != nil || columns[0].DatabaseTypeName() != "INT4" {
		t.Fatal("old finalized tail/descriptor", old.Err())
	}
	unchanged := func() {
		check(s.c, s.b.Recorder)
		if fixture.ExtensionDump(t, s.p.Ctx, s.p.Native) != dump {
			t.Fatal("failed restore changed symbolic dump")
		}
	}
	if err := stuntgres.Restore(s.p.Ctx, s.p.Native, strings.NewReader(dump+"\nINVALID SQL;")); err == nil {
		t.Fatal("malformed Restore accepted")
	}
	unchanged()
	ctx, cancel := context.WithCancel(s.p.Ctx)
	err = stuntgres.Restore(ctx, s.p.Native, &fixture.CatalogControlCancelReader{Cancel: cancel})
	cancel()
	if !errors.Is(err, context.Canceled) {
		t.Fatal("canceled restore", err)
	}
	unchanged()
	raw, target, recorder, provider := connectionDestination(t, s.p, fixture.ExternalSQLFixtureVersion, true)
	for _, q := range []string{"CREATE ROLE dbc_owner", "CREATE ROLE dbc_reader"} {
		fixture.RunStep(t, s.p.Ctx, target, recorder, fixture.Exec(q, 0))
	}
	if err := stuntgres.Restore(s.p.Ctx, raw, strings.NewReader(dump)); err != nil {
		t.Fatal(err)
	}
	check(target, recorder)
	if len(provider.Snapshot()) != 0 {
		t.Fatal("fresh replay opened/borrowed transport")
	}
}

func connectionLiveControls(s *connectionScenario) {
	_, named := s.open(new("control_named"))
	_, defaultPID := s.open(nil)
	if s.b.PG {
		s.disconnect(new("control_named"), named)
		s.disconnect(nil, defaultPID)
		return
	}
	t := s.t
	s.exec("CREATE TABLE dbc_marker(v integer)")
	fixture.RunStep(t, s.p.Ctx, s.c, s.b.Recorder, fixture.Exec("INSERT INTO dbc_marker VALUES(91)", 1))
	var notices []fixture.Diagnostic
	peerDB, peerConn, peerRecorder := fixture.NoticeReferencePool(t, s.p, false, &notices)
	defer peerDB.Close()
	defer peerConn.Close()
	peer := *s
	peer.c = peerConn
	peer.b.Recorder = peerRecorder
	_, peerPID := peer.open(new("peer"))
	// Native registrations live for this Driver's lifetime; there is no DROP
	// DATABASE command. Use an independent registration per physical scenario.
	database := fmt.Sprintf("dbc_other_%d", connectionSequence.Add(1))
	s.exec("CREATE DATABASE " + database)
	otherDB, otherRecorder := fixture.RecordedNativePool(t, s.p.NativeConnector.WithDatabase(database))
	defer otherDB.Close()
	otherConn, otherErr := otherDB.Conn(s.p.Ctx)
	if otherErr != nil {
		t.Fatal(otherErr)
	}
	defer otherConn.Close()
	other := *s
	other.c = otherConn
	other.b.Recorder = otherRecorder
	other.exec("CREATE SCHEMA dbc_ext")
	other.exec("CREATE EXTENSION dblink SCHEMA dbc_ext")
	_, otherPID := other.open(new("other_database"))
	before := s.activity()
	dump := fixture.ExtensionDump(t, s.p.Ctx, s.p.Native)
	s.unchangedActivity(before)
	for _, pid := range []int32{named, defaultPID} {
		s.remoteText(nil, pid, "SELECT 'source-still-live'::text", "source-still-live")
	}
	raw, target, recorder, provider := connectionDestination(t, s.p, fixture.ExternalSQLFixtureVersion, true)
	if err := stuntgres.Restore(s.p.Ctx, raw, strings.NewReader(dump)); err != nil {
		t.Fatal(err)
	}
	destination := *s
	destination.c = target
	destination.b.Recorder = recorder
	destination.provider = provider
	destination.list()
	destination.run(connectionError("SELECT dbc_ext.dblink_disconnect() AS value", "08003", "connection not available"))
	if len(provider.Snapshot()) != 0 {
		t.Fatal("destination replay opened source resources")
	}
	_, destinationPID := destination.open(new("independent"))
	destination.list("independent")
	if err := stuntgres.Restore(s.p.Ctx, s.p.Native, strings.NewReader(dump+"\nINVALID SQL;")); err == nil {
		t.Fatal("malformed restore succeeded")
	}
	failedBefore := s.activity()
	failedContext, cancel := context.WithCancel(s.p.Ctx)
	err := stuntgres.Restore(failedContext, s.p.Native, &fixture.CatalogControlCancelReader{Cancel: cancel})
	cancel()
	if !errors.Is(err, context.Canceled) {
		t.Fatal("canceled Restore changed live state", err)
	}
	s.unchangedActivity(failedBefore)
	s.list("control_named")
	peer.list("peer")
	for _, pid := range []int32{named, defaultPID} {
		s.remoteText(nil, pid, "SELECT 'unchanged'::text", "unchanged")
	}
	if err := stuntgres.Restore(s.p.Ctx, s.p.Native, strings.NewReader(dump)); err != nil {
		t.Fatal(err)
	}
	for _, pid := range []int32{named, defaultPID, peerPID} {
		s.assertClosed(pid)
	}
	s.list()
	peer.list()
	destination.remoteText(new("independent"), destinationPID, "SELECT 'independent-driver-live'::text", "independent-driver-live")
	other.remoteText(new("other_database"), otherPID, "SELECT 'other-database-live'::text", "other-database-live")
	s.run(connectionError("SELECT dbc_ext.dblink_disconnect() AS value", "08003", "connection not available"))
	s.run(connectionError("SELECT dbc_ext.dblink_disconnect('control_named') AS value", "08003", `connection "control_named" not available`))
	_, resetPID := s.open(new("reset"))
	if err := stuntgres.Reset(s.p.Ctx, s.p.Native); err != nil {
		t.Fatal(err)
	}
	s.assertClosed(resetPID)
	s.exec("CREATE SCHEMA dbc_ext")
	s.exec("CREATE EXTENSION dblink SCHEMA dbc_ext")
	s.list()
	other.remoteText(new("other_database"), otherPID, "SELECT 'other-database-after-reset'::text", "other-database-after-reset")
	other.disconnect(new("other_database"), otherPID)
	destination.disconnect(new("independent"), destinationPID)
}
func (s *connectionScenario) assertClosed(pid int32) {
	s.t.Helper()
	if got := s.wire(pid).Activity().Closes; got != 1 {
		s.t.Fatal("actual transport Close count", pid, got, "want 1")
	}
	if _, err := s.wire(pid).Execute(s.p.Ctx, "SELECT 1"); err == nil {
		s.t.Fatal("control retained live physical handle", pid)
	}
	connectionGone(s.t, s.p.Ctx, pid)
}
func connectionDestination(t *testing.T, p *fixture.Pair, version string, bind bool) (*sql.DB, *sql.Conn, *fixture.Recorder, *fixture.ExternalSQLProvider) {
	t.Helper()
	d := stuntgres.NewDriver()
	provider := &fixture.ExternalSQLProvider{}
	if version != "" {
		if err := d.RegisterExternalSQLProvider(stuntgres.ExternalSQLProviderDefinition{Name: "dblink_fixture", Version: version, Provider: provider}); err != nil {
			t.Fatal(err)
		}
	}
	if bind {
		if err := d.BindDblinkExternalSQLProvider("dblink_fixture"); err != nil {
			t.Fatal(err)
		}
	}
	connector, err := d.OpenConnector("dbc_destination")
	if err != nil {
		t.Fatal(err)
	}
	raw := sql.OpenDB(connector)
	t.Cleanup(func() {
		if err := raw.Close(); err != nil {
			t.Error(err)
		}
	})
	db, recorder := fixture.RecordedNativePool(t, connector)
	c, err := db.Conn(p.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := c.Close(); err != nil {
			t.Error(err)
		}
	})
	return raw, c, recorder, provider
}
