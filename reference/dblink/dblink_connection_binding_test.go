package reference_test

import (
	"errors"
	"strings"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func connectionBindingControls(s *connectionScenario) {
	if s.b.PG {
		return
	} // Native host authority/replay, not a PostgreSQL SQL API.
	t := s.t
	s.exec("CREATE TABLE dbc_marker(v integer)")
	fixture.RunStep(t, s.p.Ctx, s.c, s.b.Recorder, fixture.Exec("INSERT INTO dbc_marker VALUES(37)", 1))
	dump := fixture.ExtensionDump(t, s.p.Ctx, s.p.Native)
	for _, tc := range []struct {
		name, version string
		bind          bool
	}{
		{"missing", "", false},
		{"unbound", fixture.ExternalSQLFixtureVersion, false},
		{"version", "incompatible-version", true},
	} {
		raw, target, recorder, provider := connectionDestination(t, s.p, tc.version, tc.bind)
		destination := *s
		destination.c, destination.b.Recorder, destination.provider = target, recorder, provider
		destination.exec("CREATE SCHEMA dbc_ext")
		destination.exec("CREATE EXTENSION dblink SCHEMA dbc_ext")
		destination.exec("CREATE TABLE dbc_marker(v integer)")
		fixture.RunStep(t, s.p.Ctx, target, recorder, fixture.Exec("INSERT INTO dbc_marker VALUES(11)", 1))
		before := fixture.ExtensionDump(t, s.p.Ctx, raw)
		if err := stuntgres.Restore(s.p.Ctx, raw, strings.NewReader(dump)); err == nil {
			t.Fatal("missing/incompatible destination binding accepted", tc.name)
		}
		if after := fixture.ExtensionDump(t, s.p.Ctx, raw); after != before {
			t.Fatal("failed binding replay changed old catalog/data/origins", tc.name)
		}
		destination.list()
		if len(provider.Snapshot()) != 0 {
			t.Fatal("failed binding replay opened a transport", tc.name)
		}
		if !tc.bind {
			destination.assertUnavailableBinding()
			// An explicitly unbound installation is itself a legitimate replay recipe.
			unboundDump := fixture.ExtensionDump(t, s.p.Ctx, raw)
			fresh, c, r, p := connectionDestination(t, s.p, "", false)
			emptyBefore := fixture.ExtensionDump(t, s.p.Ctx, fresh)
			stripped := unboundDump
			for line := range strings.SplitSeq(unboundDump, "\n") {
				if strings.HasPrefix(line, "-- stuntgres-external-sql-v1 ") {
					stripped = strings.Replace(unboundDump, line+"\n", "", 1)
					break
				}
			}
			if stripped == unboundDump {
				t.Fatal("unbound installation omitted its explicit requirement marker")
			}
			if err := stuntgres.Restore(s.p.Ctx, fresh, strings.NewReader(stripped)); err == nil {
				t.Fatal("unbound replay accepted missing marker")
			}
			if fixture.ExtensionDump(t, s.p.Ctx, fresh) != emptyBefore {
				t.Fatal("missing unbound marker partially published")
			}
			if err := stuntgres.Restore(s.p.Ctx, fresh, strings.NewReader(unboundDump)); err != nil {
				t.Fatal("unbound installation replay", err)
			}
			unbound := destination
			unbound.c, unbound.b.Recorder, unbound.provider = c, r, p
			unbound.list()
			unbound.assertUnavailableBinding()
		}
	}
	raw, target, recorder, provider := connectionDestination(t, s.p, fixture.ExternalSQLFixtureVersion, true)
	destination := *s
	destination.c, destination.b.Recorder, destination.provider = target, recorder, provider
	destination.exec("CREATE TABLE dbc_marker(v integer)")
	fixture.RunStep(t, s.p.Ctx, target, recorder, fixture.Exec("INSERT INTO dbc_marker VALUES(11)", 1))
	before := fixture.ExtensionDump(t, s.p.Ctx, raw)
	header := ""
	for line := range strings.SplitSeq(dump, "\n") {
		if strings.HasPrefix(line, "-- stuntgres-external-sql-v1 ") {
			header = line + "\n"
			break
		}
	}
	if header == "" {
		t.Fatal("bound dblink installation omitted provider identity requirement")
	}
	for _, invalid := range []string{
		header + dump,
		strings.Replace(dump, header, "", 1),
		strings.Replace(dump, header, "-- stuntgres-external-sql-v1 malformed\n", 1),
		strings.Replace(dump, "stuntgres-external-sql-v1 ", "stuntgres-external-sql-v99 ", 1),
	} {
		if err := stuntgres.Restore(s.p.Ctx, raw, strings.NewReader(invalid)); err == nil {
			t.Fatal("missing/malformed/duplicate dblink requirement accepted")
		}
		if fixture.ExtensionDump(t, s.p.Ctx, raw) != before {
			t.Fatal("invalid prerequisite partially replaced destination")
		}
	}
	if err := stuntgres.Restore(s.p.Ctx, raw, strings.NewReader(dump)); err != nil {
		t.Fatal(err)
	}
	destination.list()
	fixture.RunStep(t, s.p.Ctx, target, recorder, fixture.Select("SELECT v FROM dbc_marker", []string{"v"}, []string{"INT4"}, [][]any{{int64(37)}}))
	if len(provider.Snapshot()) != 0 {
		t.Fatal("matching replay opened or borrowed source capability")
	}
}

func (s *connectionScenario) assertUnavailableBinding() {
	s.t.Helper()
	q := "SELECT dbc_ext.dblink_connect($1::text) AS value"
	executor := fixture.Executor(s.c)
	if s.prepared {
		statement, err := s.c.PrepareContext(s.p.Ctx, q)
		if err != nil {
			s.t.Fatal(err)
		}
		defer statement.Close()
		executor = fixture.HeldStatement{Executor: s.c, Statement: statement, SQL: q}
	}
	rows, err := executor.QueryContext(s.p.Ctx, q, "dbname=not_opened")
	if rows != nil {
		rows.Close()
	}
	var unavailable *stuntgres.ExternalSQLUnavailableError
	if !errors.As(err, &unavailable) {
		s.t.Fatal("unbound direct conninfo was not honestly unavailable", err)
	}
	if _, sql := err.(interface{ SQLState() string }); sql {
		s.t.Fatal("unavailable host capability invented SQLSTATE", err)
	}
	s.list()
	if len(s.provider.Snapshot()) != 0 {
		s.t.Fatal("unbound call borrowed a registered provider")
	}
}

func connectionBindingLiveControls(s *connectionScenario) {
	if s.b.PG {
		return
	}
	t := s.t
	dump := fixture.ExtensionDump(t, s.p.Ctx, s.p.Native)
	raw, target, recorder, provider := connectionDestination(t, s.p, "incompatible-version", true)
	destination := *s
	destination.c, destination.b.Recorder, destination.provider = target, recorder, provider
	destination.exec("CREATE SCHEMA dbc_ext")
	destination.exec("CREATE EXTENSION dblink SCHEMA dbc_ext")
	destination.exec("CREATE TABLE dbc_marker(v integer)")
	fixture.RunStep(t, s.p.Ctx, target, recorder, fixture.Exec("INSERT INTO dbc_marker VALUES(19)", 1))
	_, named := destination.open(new("retained"))
	_, defaultPID := destination.open(nil)
	before := destination.activity()
	if err := stuntgres.Restore(s.p.Ctx, raw, strings.NewReader(dump)); err == nil {
		t.Fatal("incompatible binding replay accepted")
	}
	destination.unchangedActivity(before)
	destination.list("retained")
	fixture.RunStep(t, s.p.Ctx, target, recorder, fixture.Select("SELECT v FROM dbc_marker", []string{"v"}, []string{"INT4"}, [][]any{{int64(19)}}))
	destination.remoteText(new("retained"), named, "SELECT 'failed-replay-retained'::text", "failed-replay-retained")
	destination.remoteText(nil, defaultPID, "SELECT 'default-retained'::text", "default-retained")
	unboundRaw, unboundConn, unboundRecorder, _ := connectionDestination(t, s.p, "", false)
	for _, q := range []string{"CREATE SCHEMA dbc_ext", "CREATE EXTENSION dblink SCHEMA dbc_ext"} {
		fixture.RunStep(t, s.p.Ctx, unboundConn, unboundRecorder, fixture.Exec(q, 0))
	}
	unboundDump := fixture.ExtensionDump(t, s.p.Ctx, unboundRaw)
	if err := stuntgres.Restore(s.p.Ctx, raw, strings.NewReader(unboundDump)); err != nil {
		t.Fatal("explicit unbound source required its absent capability", err)
	}
	destination.assertClosed(named)
	destination.assertClosed(defaultPID)
	destination.list()
	_, next := destination.open(new("destination_binding_only"))
	destination.disconnect(new("destination_binding_only"), next)
}
