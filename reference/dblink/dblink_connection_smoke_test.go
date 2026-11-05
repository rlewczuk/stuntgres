package reference_test

import (
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceDblinkConnectionFeatureSmoke(t *testing.T) {
	connectionCase(t, true, func(s *connectionScenario) {
		t := s.t
		s.list()
		app, named := s.open(new("smoke"))
		s.list("smoke")
		s.remote(new("smoke"), named, "BEGIN; CREATE TEMP TABLE smoke_tx(v text); INSERT INTO smoke_tx VALUES('real remote transaction')")
		s.remoteText(new("smoke"), named, "SELECT v FROM smoke_tx", "real remote transaction")
		before := len(s.provider.Snapshot())
		s.run(connectionError("SELECT dbc_ext.dblink_connect($1::text,$2::text) AS value", "42710", "duplicate connection name", "smoke", s.conninfo("dbc_smoke_duplicate", false)))
		if len(s.provider.Snapshot()) != before || s.pid(app) != named {
			t.Fatal("duplicate replaced/reopened genuine session")
		}
		_, oldDefault := s.open(nil)
		s.remote(nil, oldDefault, "CREATE TEMP TABLE smoke_default(v text); INSERT INTO smoke_default VALUES('old default preserved'); BEGIN")
		s.run(parserFailure("dbname", `missing "=" after "dbname" in connection info string`))
		s.remoteText(nil, oldDefault, "SELECT v FROM smoke_default", "old default preserved")
		_, newDefault := s.open(nil)
		if newDefault == oldDefault {
			t.Fatal("replacement reused actual backend")
		}
		connectionGone(t, s.p.Ctx, oldDefault)
		if !s.b.PG {
			s.assertClosed(oldDefault)
		}
		s.disconnect(new("smoke"), named)
		s.run(connectionError("SELECT dbc_ext.dblink_disconnect('smoke') AS value", "08003", `connection "smoke" not available`))
		s.list()
		s.remote(nil, newDefault, "CREATE TEMP TABLE smoke_security(v text); INSERT INTO smoke_security VALUES('security rejection preserved default')")
		trust := s.trustConninfo()
		s.exec("CREATE ROLE dbc_reader")
		s.exec("GRANT USAGE ON SCHEMA dbc_ext TO dbc_reader")
		s.exec("SET SESSION AUTHORIZATION dbc_reader")
		s.run(connectionText("SELECT dbc_ext.dblink_connect_u(NULL::text) AS value", nil))
		s.run(connectionError("SELECT dbc_ext.dblink_connect_u($1::text) AS value", "42501", "permission denied for function dblink_connect_u", trust))
		s.run(connectionError("SELECT dbc_ext.dblink_connect_u($1::text,$2::text) AS value", "42501", "permission denied for function dblink_connect_u", "untrusted", trust))
		postcheck := stepForTrustOwner(trust)
		postcheck.Query = "SELECT dbc_ext.dblink_connect($1::text) AS value"
		postcheck.Args = []any{trust}
		s.run(postcheck)
		s.remoteText(nil, newDefault, "SELECT v FROM smoke_security", "security rejection preserved default")
		if !s.b.PG {
			wires := s.provider.Snapshot()
			rejected := wires[len(wires)-1]
			if rejected.Info().CredentialMechanism != "none" {
				t.Fatal("actual trust rejection proof", rejected.Info())
			}
			s.assertClosed(rejected.Info().BackendPID)
		}
		s.exec("RESET SESSION AUTHORIZATION")
		s.exec("GRANT EXECUTE ON FUNCTION dbc_ext.dblink_connect_u(text,text) TO dbc_reader")
		s.exec("SET SESSION AUTHORIZATION dbc_reader")
		s.run(connectionText("SELECT dbc_ext.dblink_connect_u($1::text,$2::text) AS value", "OK", "granted", trust))
		s.list("granted")
		s.disconnect(new("granted"), s.trustPID())
		s.exec("RESET SESSION AUTHORIZATION")
		if s.b.PG {
			s.disconnect(nil, newDefault)
			return
		}
		// Actual native controls are not advertised as PostgreSQL parity.
		s.exec("CREATE TABLE dbc_marker(v integer)")
		fixture.RunStep(t, s.p.Ctx, s.c, s.b.Recorder, fixture.Exec("INSERT INTO dbc_marker VALUES(37)", 1))
		dumpBefore := s.activity()
		dump := fixture.ExtensionDump(t, s.p.Ctx, s.p.Native)
		s.unchangedActivity(dumpBefore)
		s.remoteText(nil, newDefault, "SELECT 'dump-left-source-live'::text", "dump-left-source-live")
		raw, target, recorder, destinationProvider := connectionDestination(t, s.p, fixture.ExternalSQLFixtureVersion, true)
		fixture.RunStep(t, s.p.Ctx, target, recorder, fixture.Exec("CREATE ROLE dbc_reader", 0))
		if err := stuntgres.Restore(s.p.Ctx, raw, strings.NewReader(dump)); err != nil {
			t.Fatal(err)
		}
		fixture.RunStep(t, s.p.Ctx, target, recorder, fixture.Select("SELECT v FROM dbc_marker", []string{"v"}, []string{"INT4"}, [][]any{{int64(37)}}))
		fixture.RunStep(t, s.p.Ctx, target, recorder, fixture.Select("SELECT dbc_ext.dblink_get_connections() AS value", []string{"value"}, []string{"_TEXT"}, [][]any{{nil}}))
		if len(destinationProvider.Snapshot()) != 0 {
			t.Fatal("fresh restore borrowed/serialized a live handle")
		}
		if err := stuntgres.Restore(s.p.Ctx, s.p.Native, strings.NewReader(dump)); err != nil {
			t.Fatal(err)
		}
		s.assertClosed(newDefault)
		s.list()
	})
}
