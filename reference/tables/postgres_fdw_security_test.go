package reference_test

import (
	"database/sql"
	"regexp"
	"testing"

	"stuntgres/reference/fixture"
)

func postgresFDWSecurityDiagnostic(postOpen bool) fixture.Diagnostic {
	d := fdwDiagnostic("2F003", "password or GSSAPI delegated credentials required")
	d.Detail = "Non-superusers must delegate GSSAPI credentials, provide a password, or enable SCRAM pass-through in user mapping."
	if postOpen {
		d.Detail = "Non-superuser cannot connect if the server does not request a password or use GSSAPI with delegated credentials."
		d.Hint = "Target server's authentication method must be changed or password_required=false set in the user mapping attributes."
	}
	return d
}

// Only the external libpq/host establishment Detail has variable endpoint text.
// All other exposed logical fields, including absent Context/Position, are exact.
func postgresFDWConnectionFailure(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, prepared bool, password bool) {
	t.Helper()
	const q = "SELECT id FROM pf_table"
	var rows *sql.Rows
	var err error
	if prepared {
		var stmt *sql.Stmt
		stmt, err = c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal("connection failure occurred during Prepare:", err)
		}
		defer stmt.Close()
		rows, err = stmt.QueryContext(p.Ctx)
	} else {
		rows, err = c.QueryContext(p.Ctx, q)
	}
	if rows != nil {
		defer rows.Close()
		if rows.Next() {
			t.Fatal("failed endpoint returned a row")
		}
		if err == nil {
			err = rows.Err()
		}
	}
	if err == nil {
		t.Fatal("failed endpoint returned success")
	}
	d := fixture.ExtractDiagnostic(t, err)
	pattern := `^connection to server at "[^"\n]+"(?: \([^)\n]+\))?, port [0-9]+ failed: Connection refused\n\tIs the server running on that host and accepting TCP/IP connections\?$`
	if password {
		pattern = `^connection to server at "[^"\n]+"(?: \([^)\n]+\))?, port [0-9]+ failed: FATAL:  password authentication failed for user "stuntgres"$`
	}
	if !regexp.MustCompile(pattern).MatchString(d.Detail) {
		t.Fatalf("external connection Detail outside bounded endpoint grammar: %#v", d)
	}
	want := fdwDiagnostic("08001", `could not connect to server "pf_server"`)
	want.Detail = d.Detail
	fixture.AssertDiagnostic(t, err, &want)
}

func TestReferencePostgresFDWSecurity(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"pf_actor", "pf_remote_trust"})
	p, provider := postgresFDWPhysicalScenario(t)
	fixture.ExternalSQLPasswordAuthentication(t, p, "stuntgres", "stuntgres")
	fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, fixture.Exec("CREATE ROLE pf_remote_trust LOGIN PASSWORD 'trust-secret'", 0))
	fixture.ExternalSQLTrustAuthentication(t, p, "pf_remote_trust")
	postgresFDWEach(t, p, func(t *testing.T, b fixture.Backend, c *sql.Conn, prepared bool, run func(fixture.Step)) {
		postgresFDWPhysicalInstall(t, b, run)
		run(fixture.Exec("CREATE ROLE pf_actor", 0))
		defer func() {
			for _, q := range []string{"RESET ROLE", "DROP OWNED BY pf_actor CASCADE", "DROP ROLE pf_actor"} {
				if _, err := c.ExecContext(p.Ctx, q); err != nil {
					t.Error(q, err)
				}
			}
		}()
		run(fixture.Exec("GRANT USAGE ON SCHEMA public TO pf_actor", 0))
		run(fixture.Exec("GRANT USAGE ON FOREIGN SERVER pf_server TO pf_actor", 0))
		run(fixture.Exec("GRANT SELECT ON pf_table,pf_identity TO pf_actor", 0))
		run(fixture.Exec("CREATE USER MAPPING FOR pf_actor SERVER pf_server OPTIONS(user 'stuntgres')", 0))
		run(fixture.Exec("SET ROLE pf_actor", 0))
		before := provider.OpenCalls()
		postgresFDWQueryFailure(t, p, b, c, "SELECT id FROM pf_table", prepared, postgresFDWSecurityDiagnostic(false))
		if !b.PG && provider.OpenCalls() != before {
			t.Fatal("password precheck opened transport")
		}
		run(fixture.Exec("ALTER USER MAPPING FOR CURRENT_USER SERVER pf_server OPTIONS(ADD password '')", 0))
		postgresFDWQueryFailure(t, p, b, c, "SELECT id FROM pf_table", prepared, postgresFDWSecurityDiagnostic(false))
		if !b.PG && provider.OpenCalls() != before {
			t.Fatal("empty password opened transport")
		}
		// SQL-supplied passthrough/GSS configuration is not evidence of incoming
		// keys/tokens. The PG login here has no SCRAM password exchange either.
		run(fixture.Exec("RESET ROLE", 0))
		run(fixture.Exec("ALTER USER MAPPING FOR pf_actor SERVER pf_server OPTIONS(SET password 'stuntgres')", 0))
		run(fixture.Exec("SET ROLE pf_actor", 0))
		run(fixture.Select("SELECT id FROM pf_table ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}, {int64(2)}, {int64(3)}}))
		pid := postgresFDWPID(t, p, c)
		if !b.PG {
			wires := provider.Snapshot()
			if wires[len(wires)-1].Info().CredentialMechanism != "scram-sha-256" {
				t.Fatal("supplied password was not actual SCRAM evidence")
			}
		}
		run(fixture.Exec("RESET ROLE", 0))
		run(fixture.Exec("ALTER USER MAPPING FOR pf_actor SERVER pf_server OPTIONS(SET user 'pf_remote_trust',SET password 'unused-explicit')", 0))
		postgresFDWObserveGone(t, p, pid)
		run(fixture.Exec("SET ROLE pf_actor", 0))
		postgresFDWQueryFailure(t, p, b, c, "SELECT id FROM pf_table", prepared, postgresFDWSecurityDiagnostic(true))
		if !b.PG {
			wires := provider.Snapshot()
			wire := wires[len(wires)-1]
			if wire.Info().CredentialMechanism != "none" {
				t.Fatal("real trust authentication was not observed")
			}
			postgresFDWObserveGone(t, p, wire.Info().BackendPID)
		}
		run(fixture.Exec("RESET ROLE", 0))
		fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, fixture.Exec(`GRANT USAGE ON SCHEMA "pf source ""quoted" TO pf_remote_trust`, 0))
		fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, fixture.Exec(`GRANT SELECT ON "pf source ""quoted"."data table" TO pf_remote_trust`, 0))
		run(fixture.Exec("ALTER USER MAPPING FOR pf_actor SERVER pf_server OPTIONS(ADD password_required 'false')", 0))
		run(fixture.Exec("SET ROLE pf_actor", 0))
		run(fixture.Select("SELECT count(*) FROM pf_table", []string{"count"}, []string{"INT8"}, [][]any{{int64(4)}}))
		run(fixture.Exec("RESET ROLE", 0))
		if !b.PG {
			run(fixture.Exec("ALTER USER MAPPING FOR pf_actor SERVER pf_server OPTIONS(SET user 'stuntgres',SET password 'stuntgres',SET password_required 'true')", 0))
			provider.ConfigureFaults(func(f *fixture.ExternalSQLFaults) { f.OmitCredentialEvidence = true })
			defer provider.ConfigureFaults(func(f *fixture.ExternalSQLFaults) { f.OmitCredentialEvidence = false })
			run(fixture.Exec("SET ROLE pf_actor", 0))
			postgresFDWQueryFailure(t, p, b, c, "SELECT id FROM pf_table", prepared, postgresFDWSecurityDiagnostic(true))
			wires := provider.Snapshot()
			wire := wires[len(wires)-1]
			if wire.Info().CredentialMechanism != "unknown" {
				t.Fatal("credential withholding fabricated evidence")
			}
			postgresFDWObserveGone(t, p, wire.Info().BackendPID)
			provider.ConfigureFaults(func(f *fixture.ExternalSQLFaults) { f.OmitCredentialEvidence = false })
			run(fixture.Exec("RESET ROLE", 0))
			run(fixture.Exec("ALTER USER MAPPING FOR pf_actor SERVER pf_server OPTIONS(DROP password,ADD use_scram_passthrough 'true',ADD gssdelegation 'true')", 0))
			run(fixture.Exec("SET ROLE pf_actor", 0))
			before := provider.OpenCalls()
			postgresFDWQueryFailure(t, p, b, c, "SELECT id FROM pf_table", prepared, postgresFDWSecurityDiagnostic(false))
			if provider.OpenCalls() != before {
				t.Fatal("options fabricated incoming SCRAM/GSS credentials")
			}
			run(fixture.Exec("RESET ROLE", 0))
			run(fixture.Exec("ALTER USER MAPPING FOR pf_actor SERVER pf_server OPTIONS(ADD password 'stuntgres',DROP use_scram_passthrough,DROP gssdelegation)", 0))
		}
		run(fixture.Exec("ALTER USER MAPPING FOR pf_actor SERVER pf_server OPTIONS(SET user 'stuntgres',SET password 'wrong-password',SET password_required 'true')", 0))
		run(fixture.Exec("SET ROLE pf_actor", 0))
		postgresFDWConnectionFailure(t, p, b, c, prepared, true)
		run(fixture.Exec("RESET ROLE", 0))
		run(fixture.Exec("ALTER USER MAPPING FOR pf_actor SERVER pf_server OPTIONS(SET password 'stuntgres')", 0))
		run(fixture.Exec("ALTER SERVER pf_server OPTIONS(SET port '1')", 0))
		run(fixture.Exec("SET ROLE pf_actor", 0))
		postgresFDWConnectionFailure(t, p, b, c, prepared, false)
		run(fixture.Exec("RESET ROLE", 0))
		run(fixture.Exec("ALTER SERVER pf_server OPTIONS(SET port "+fileFDWLiteral(postgresFDWPort(t, b))+")", 0))
		run(fixture.Exec("SET ROLE pf_actor", 0))
		run(fixture.Select("SELECT count(*) FROM pf_table", []string{"count"}, []string{"INT8"}, [][]any{{int64(4)}}))
		run(fixture.Exec("RESET ROLE", 0))
	})
}
