package reference_test

import (
	"fmt"
	"runtime"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/lib/pq"
	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceDblinkConnectionSecurity(t *testing.T) {
	t.Run("strict-null-and-runtime-admission", func(t *testing.T) {
		connectionCase(t, false, func(s *connectionScenario) {
			s.exec("CREATE ROLE dbc_reader")
			s.exec("GRANT USAGE ON SCHEMA dbc_ext TO dbc_reader")
			s.exec("CREATE TEMP TABLE dbc_null_input(v text)")
			defer s.exec("DROP TABLE dbc_null_input")
			fixture.RunStep(s.t, s.p.Ctx, s.c, s.b.Recorder, fixture.Exec("INSERT INTO dbc_null_input VALUES(NULL)", 1))
			s.exec("GRANT SELECT ON dbc_null_input TO dbc_reader")
			s.exec("SET SESSION AUTHORIZATION dbc_reader")
			defer s.exec("RESET SESSION AUTHORIZATION")
			// Constant NULL eliminates a STRICT call during planning, including
			// VOLATILE calls. This does not exercise runtime EXECUTE admission.
			for _, q := range []string{"SELECT dbc_ext.dblink_connect_u(NULL::text) AS value", "SELECT dbc_ext.dblink_connect_u(NULL::text,NULL::text) AS value", "SELECT dbc_ext.dblink_connect_u(NULL::text,'dbname=unused') AS value", "SELECT dbc_ext.dblink_connect_u('denied',NULL::text) AS value"} {
				s.run(connectionText(q, nil))
			}
			// Neither overload is executable by the reader. NonNULL arguments
			// and a runtime NULL column both leave a genuine call to admit.
			for _, q := range []string{"SELECT dbc_ext.dblink_connect_u('dbname=unused') AS value", "SELECT dbc_ext.dblink_connect_u('denied','dbname=unused') AS value", "SELECT dbc_ext.dblink_connect_u(v) AS value FROM dbc_null_input", "SELECT dbc_ext.dblink_connect_u('denied',v) AS value FROM dbc_null_input"} {
				s.run(connectionError(q, "42501", "permission denied for function dblink_connect_u"))
			}
			for _, input := range []string{"", "dbname=unused", "password=''", "password=present password=", "postgresql://stuntgres:@localhost/postgres", "service=dbc_nonexistent_service", "passfile=/dbc_missing_password_file", "dbname='unterminated", "port=invalid"} {
				step := connectionError("SELECT dbc_ext.dblink_connect($1::text,$2::text) AS value", "2F003", "password or GSSAPI delegated credentials required", "denied", input)
				step.Diagnostic.Detail = "Non-superusers must provide a password in the connection string or send delegated GSSAPI credentials."
				s.run(step)
			}
			s.list()
			if len(s.provider.Snapshot()) != 0 {
				s.t.Fatal("pre-auth admission reached provider")
			}
		})
	})
	t.Run("scram-and-trust", func(t *testing.T) { connectionCase(t, true, connectionSecurityPhysical) })
	t.Run("identity-current-admission", func(t *testing.T) { connectionCase(t, false, connectionSecurityIdentity) })
}

func connectionSecurityPhysical(s *connectionScenario) {
	t := s.t
	s.exec("SET application_name='dbc_outer_security'")
	s.exec("ALTER FUNCTION dbc_ext.dblink_connect_u(text,text) SET application_name='dbc_inner_security'")
	s.exec("CREATE ROLE dbc_reader")
	s.exec("GRANT USAGE ON SCHEMA dbc_ext TO dbc_reader")
	trust := s.trustConninfo()
	app := fmt.Sprintf("dbc_scram_%d", connectionSequence.Add(1))
	scram := s.conninfo(app, true)
	// The independent host probe observes the actual handshake, not the HBA
	// rule or presence of a configured password. It is never an engine handle.
	probe := &fixture.ExternalSQLProvider{}
	observed, err := probe.Open(s.p.Ctx, stuntgres.ExternalSQLConnectRequest{ConninfoPresent: true, Conninfo: s.nativePasswordConninfo(app)})
	if err != nil {
		t.Fatal(err)
	}
	if observed.Info().CredentialMechanism != "scram-sha-256" {
		t.Fatal("real password route must negotiate SCRAM", observed.Info())
	}
	if err := observed.Close(s.p.Ctx); err != nil {
		t.Fatal(err)
	}
	connectionGone(t, s.p.Ctx, observed.Info().BackendPID)
	s.exec("SET SESSION AUTHORIZATION dbc_reader")
	defer s.exec("RESET SESSION AUTHORIZATION")
	s.run(connectionText("SELECT dbc_ext.dblink_connect($1::text,$2::text) AS value", "OK", "scram", scram))
	pid := s.pid(app)
	if !s.b.PG && s.wire(pid).Info().CredentialMechanism != "scram-sha-256" {
		t.Fatal("engine used unobserved authentication", s.wire(pid).Info())
	}
	for _, input := range []string{"password=''", "dbname='unterminated"} {
		precheck := connectionError("SELECT dbc_ext.dblink_connect($1::text,$2::text) AS value", "2F003", "password or GSSAPI delegated credentials required", "scram", input)
		precheck.Diagnostic.Detail = "Non-superusers must provide a password in the connection string or send delegated GSSAPI credentials."
		s.run(precheck)
	}
	numericBefore := s.activity()
	s.run(connectionError("SELECT dbc_ext.dblink_connect($1::text,$2::text) AS value", "42710", "duplicate connection name", "scram", "password=explicit port=invalid"))
	numeric := connectionError("SELECT dbc_ext.dblink_connect($1::text,$2::text) AS value", "08001", "could not establish connection", "bad_port", "password=explicit port=invalid")
	numeric.Diagnostic.Detail = `invalid integer value "invalid" for connection option "port"`
	s.run(numeric)
	s.list("scram")
	s.unchangedActivity(numericBefore)
	if s.pid(app) != pid {
		t.Fatal("numeric option failure replaced old authenticated connection")
	}
	s.disconnect(new("scram"), pid)
	s.run(connectionText("SELECT dbc_ext.dblink_connect($1::text,$2::text) AS value", "OK", "bad_port", scram))
	s.disconnect(new("bad_port"), s.pid(app))
	before := len(s.provider.Snapshot())
	step := connectionError("SELECT dbc_ext.dblink_connect($1::text,$2::text) AS value", "2F003", "password or GSSAPI delegated credentials required", "trust", trust)
	step.Diagnostic.Detail = "Non-superusers may only connect using credentials they provide, eg: password in connection string or delegated GSSAPI credentials"
	step.Diagnostic.Hint = "Ensure provided credentials match target server's authentication method."
	s.run(step)
	s.list()
	if !s.b.PG {
		wires := s.provider.Snapshot()
		if len(wires) != before+1 {
			t.Fatal("postcheck did not open exactly one real wire")
		}
		wire := wires[len(wires)-1]
		if wire.Info().CredentialMechanism != "none" {
			t.Fatal("trust misreported password use", wire.Info())
		}
		if _, err := wire.Execute(s.p.Ctx, "SELECT 1"); err == nil {
			t.Fatal("rejected trust wire still executes")
		}
		connectionGone(t, s.p.Ctx, wire.Info().BackendPID)
	}
	if !s.b.PG {
		s.provider.ConfigureFaults(func(f *fixture.ExternalSQLFaults) { f.OmitCredentialEvidence = true })
		defer s.provider.ConfigureFaults(func(f *fixture.ExternalSQLFaults) { f.OmitCredentialEvidence = false })
		unknown := step
		unknown.Args = []any{"unknown", scram}
		s.run(unknown)
		wires := s.provider.Snapshot()
		wire := wires[len(wires)-1]
		if wire.Info().CredentialMechanism != "unknown" {
			t.Fatal("host evidence omission was not honored")
		}
		s.assertClosed(wire.Info().BackendPID)
		s.provider.ConfigureFaults(func(f *fixture.ExternalSQLFaults) { f.OmitCredentialEvidence = false })
	}
	s.run(connectionError("SELECT dbc_ext.dblink_connect_u($1::text,$2::text) AS value", "42501", "permission denied for function dblink_connect_u", "trusted", trust))
	s.exec("RESET SESSION AUTHORIZATION")
	s.exec("GRANT EXECUTE ON FUNCTION dbc_ext.dblink_connect_u(text,text) TO dbc_reader")
	q := "SELECT dbc_ext.dblink_connect_u($1::text,$2::text) AS value"
	held, err := s.c.PrepareContext(s.p.Ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	s.exec("SET SESSION AUTHORIZATION dbc_reader")
	fixture.RunStep(t, s.p.Ctx, fixture.HeldStatement{Executor: s.c, Statement: held, SQL: q}, s.b.Recorder, connectionText(q, "OK", "trusted", trust))
	s.list("trusted")
	trustedPID := s.trustPID()
	s.run(fixture.Select("SELECT current_user::text AS current,session_user::text AS session", []string{"current", "session"}, []string{"TEXT", "TEXT"}, [][]any{{"dbc_reader", "dbc_reader"}}))
	s.run(connectionSettingText("SELECT current_setting('application_name') AS value", "dbc_outer_security", s.b.PG))
	// Constant NULL bypasses the ungranted overload, but a real nonNULL call
	// stays denied even while the two-argument overload has been granted.
	s.run(connectionText("SELECT dbc_ext.dblink_connect_u(NULL::text) AS value", nil))
	s.run(connectionError("SELECT dbc_ext.dblink_connect_u($1::text) AS value", "42501", "permission denied for function dblink_connect_u", trust))
	s.disconnect(new("trusted"), trustedPID)
	s.exec("RESET SESSION AUTHORIZATION")
	s.exec("REVOKE EXECUTE ON FUNCTION dbc_ext.dblink_connect_u(text,text) FROM dbc_reader")
	s.exec("SET SESSION AUTHORIZATION dbc_reader")
	fixture.RunStep(t, s.p.Ctx, fixture.HeldStatement{Executor: s.c, Statement: held, SQL: q}, s.b.Recorder, connectionError(q, "42501", "permission denied for function dblink_connect_u", "denied", trust))
	s.run(fixture.Select("SELECT current_user::text AS current,session_user::text AS session", []string{"current", "session"}, []string{"TEXT", "TEXT"}, [][]any{{"dbc_reader", "dbc_reader"}}))
	s.exec("RESET SESSION AUTHORIZATION")
	// SECURITY DEFINER is the actual owner's environment, not a name bypass.
	s.exec("ALTER FUNCTION dbc_ext.dblink_connect_u(text,text) OWNER TO dbc_reader")
	s.exec("GRANT EXECUTE ON FUNCTION dbc_ext.dblink_connect_u(text,text) TO PUBLIC")
	s.run(stepForTrustOwner(trust))
	s.run(connectionSettingText("SELECT current_setting('application_name') AS value", "dbc_outer_security", s.b.PG))
	s.exec("ALTER FUNCTION dbc_ext.dblink_connect_u(text,text) SECURITY INVOKER")
	s.run(connectionText(q, "OK", "invoker", trust))
	s.disconnect(new("invoker"), s.trustPID())
	s.run(connectionSettingText("SELECT current_setting('application_name') AS value", "dbc_outer_security", s.b.PG))
}

func stepForTrustOwner(conninfo string) fixture.Step {
	step := connectionError("SELECT dbc_ext.dblink_connect_u($1::text,$2::text) AS value", "2F003", "password or GSSAPI delegated credentials required", "owner", conninfo)
	step.Diagnostic.Detail = "Non-superusers may only connect using credentials they provide, eg: password in connection string or delegated GSSAPI credentials"
	step.Diagnostic.Hint = "Ensure provided credentials match target server's authentication method."
	return step
}
func (s *connectionScenario) nativePasswordConninfo(app string) string {
	c, err := pgconn.ParseConfig(fixture.DSN)
	if err != nil {
		s.t.Fatal(err)
	}
	return connectionURI(c.Host, c.Port, c.User, c.Password, c.Database, app)
}
func (s *connectionScenario) trustConninfo() string {
	s.t.Helper()
	const role = "dbc_trust_remote"
	if _, err := fixture.Admin.ExecContext(s.p.Ctx, "CREATE ROLE "+role+" LOGIN PASSWORD 'stuntgres'"); err != nil {
		s.t.Fatal(err)
	}
	s.t.Cleanup(func() {
		if _, err := fixture.Admin.ExecContext(s.p.Ctx, "DROP ROLE "+pq.QuoteIdentifier(role)); err != nil {
			s.t.Error(err)
		}
	})
	fixture.ExternalSQLTrustAuthentication(s.t, s.p, role)
	c, err := pgconn.ParseConfig(fixture.DSN)
	if err != nil {
		s.t.Fatal(err)
	}
	conninfo := connectionURI(c.Host, c.Port, role, "stuntgres", c.Database, "dbc_trust_probe")
	probe := &fixture.ExternalSQLProvider{}
	for {
		wire, err := probe.Open(s.p.Ctx, stuntgres.ExternalSQLConnectRequest{ConninfoPresent: true, Conninfo: conninfo})
		if err != nil {
			s.t.Fatal(err)
		}
		mechanism := wire.Info().CredentialMechanism
		if err := wire.Close(s.p.Ctx); err != nil {
			s.t.Fatal(err)
		}
		connectionGone(s.t, s.p.Ctx, wire.Info().BackendPID)
		if mechanism == "none" {
			break
		}
		if err := s.p.Ctx.Err(); err != nil {
			s.t.Fatal("real trust HBA rule was not loaded", mechanism, err)
		}
		runtime.Gosched()
	}
	base, err := pgconn.ParseConfig(s.conninfo("dbc_trust", false))
	if err != nil {
		s.t.Fatal(err)
	}
	return connectionURI(base.Host, base.Port, role, "stuntgres", base.Database, fmt.Sprintf("dbc_trust_%d", connectionSequence.Add(1)))
}

func connectionSecurityIdentity(s *connectionScenario) {
	t := s.t
	s.exec("CREATE ROLE dbc_reader")
	s.exec("CREATE SCHEMA dbc_moved")
	s.exec("GRANT USAGE ON SCHEMA dbc_ext,dbc_moved TO dbc_reader")
	// A stored view retains its authentic routine dependency through rename.
	// A held SQL statement instead reparses its original text on invalidation.
	s.exec("CREATE VIEW dbc_ext.saved_disconnect AS SELECT dbc_ext.dblink_disconnect('stored_missing'::text) AS value")
	q := "SELECT dbc_ext.dblink_disconnect($1::text) AS value"
	held, err := s.c.PrepareContext(s.p.Ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	x := fixture.HeldStatement{Executor: s.c, Statement: held, SQL: q}
	fixture.RunStep(t, s.p.Ctx, x, s.b.Recorder, connectionText(q, nil, nil))
	s.exec("ALTER FUNCTION dbc_ext.dblink_disconnect(text) RENAME TO renamed_disconnect")
	s.exec("ALTER EXTENSION dblink SET SCHEMA dbc_moved")
	s.exec("CREATE FUNCTION dbc_ext.dblink_disconnect(text) RETURNS text LANGUAGE SQL AS 'SELECT ''ordinary shadow''::text'")
	s.run(connectionText(q, "ordinary shadow", "unused"))
	fixture.RunStep(t, s.p.Ctx, x, s.b.Recorder, connectionText(q, "ordinary shadow", nil))
	s.run(connectionError("SELECT value FROM dbc_ext.saved_disconnect", "08003", `connection "stored_missing" not available`))
	movedQuery := "SELECT dbc_moved.renamed_disconnect($1::text) AS value"
	movedHeld, err := s.c.PrepareContext(s.p.Ctx, movedQuery)
	if err != nil {
		t.Fatal(err)
	}
	defer movedHeld.Close()
	moved := fixture.HeldStatement{Executor: s.c, Statement: movedHeld, SQL: movedQuery}
	fixture.RunStep(t, s.p.Ctx, moved, s.b.Recorder, connectionError(movedQuery, "08003", `connection "missing" not available`, "missing"))
	s.exec("REVOKE EXECUTE ON FUNCTION dbc_moved.renamed_disconnect(text) FROM PUBLIC")
	s.exec("SET SESSION AUTHORIZATION dbc_reader")
	defer s.exec("RESET SESSION AUTHORIZATION")
	fixture.RunStep(t, s.p.Ctx, moved, s.b.Recorder, connectionError(movedQuery, "42501", "permission denied for function renamed_disconnect", "missing"))
	s.exec("RESET SESSION AUTHORIZATION")
	s.exec("GRANT EXECUTE ON FUNCTION dbc_moved.renamed_disconnect(text) TO dbc_reader")
	s.exec("SET SESSION AUTHORIZATION dbc_reader")
	fixture.RunStep(t, s.p.Ctx, moved, s.b.Recorder, connectionError(movedQuery, "08003", `connection "missing" not available`, "missing"))
	s.exec("RESET SESSION AUTHORIZATION")
	s.exec("REVOKE USAGE ON SCHEMA dbc_moved FROM dbc_reader")
	s.exec("SET SESSION AUTHORIZATION dbc_reader")
	schemaDenied := connectionError(movedQuery, "42501", "permission denied for schema dbc_moved", "missing")
	schemaDenied.Diagnostic.Position = "8"
	fixture.RunStep(t, s.p.Ctx, moved, s.b.Recorder, schemaDenied)
}

func (s *connectionScenario) trustPID() int32 {
	s.t.Helper()
	var pid int32
	var user, db string
	if err := fixture.Admin.QueryRowContext(s.p.Ctx, "SELECT pid,usename,datname FROM pg_stat_activity WHERE application_name LIKE 'dbc_trust_%' AND usename='dbc_trust_remote'").Scan(&pid, &user, &db); err != nil || pid <= 0 || user != "dbc_trust_remote" || db != "stuntgres_reference" {
		s.t.Fatal("genuine trust backend identity", pid, user, db, err)
	}
	if !s.b.PG {
		info := s.wire(pid).Info()
		if info.CredentialMechanism != "none" || info.User != user || info.Database != db {
			s.t.Fatal("actual trust authentication/identity", info)
		}
	}
	return pid
}
