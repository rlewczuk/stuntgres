package reference_test

import (
	"fmt"
	"testing"
)

// REL_18_6 fe-connect.c checks the parsed port against 1..65535 after
// syntax-only PQconninfoParse. Expected diagnostics are release-authored,
// not copied from a native provider's ParseConfig error.
func TestReferenceDblinkConnectionPortBounds(t *testing.T) {
	for _, port := range []string{"0", "65536", "-1"} {
		t.Run("diagnostic/"+port, func(t *testing.T) {
			connectionCase(t, false, func(s *connectionScenario) {
				before := s.provider.OpenCalls()
				for _, input := range []string{
					"host=127.0.0.1 dbname=stuntgres_reference password=explicit port=" + port,
					"postgresql://localhost/stuntgres_reference?password=explicit&port=" + port,
				} {
					s.run(parserFailure(input, `invalid port number: "`+port+`"`))
					s.list()
				}
				if !s.b.PG && s.provider.OpenCalls() != before {
					t.Fatal("numeric range rejection entered provider Open")
				}
			})
		})
	}
	t.Run("reservation-order-and-reuse", func(t *testing.T) {
		connectionCase(t, true, connectionPortReservations)
	})
}

func connectionPortReservations(s *connectionScenario) {
	s.exec("CREATE ROLE dbc_reader")
	s.exec("GRANT USAGE ON SCHEMA dbc_ext TO dbc_reader")
	app, namedPID := s.open(new("port_live"))
	_, defaultPID := s.open(nil)
	s.remote(new("port_live"), namedPID, "CREATE TEMP TABLE port_state(v text); INSERT INTO port_state VALUES('named preserved')")
	s.remote(nil, defaultPID, "CREATE TEMP TABLE port_state(v text); INSERT INTO port_state VALUES('default preserved')")
	defer s.exec("RESET SESSION AUTHORIZATION")
	for _, restricted := range []bool{false, true} {
		if restricted {
			s.exec("SET SESSION AUTHORIZATION dbc_reader")
		}
		for _, port := range []string{"0", "65536", "-1"} {
			withoutSecret := "host=127.0.0.1 dbname=stuntgres_reference port=" + port
			input := withoutSecret + " password=explicit"
			before, calls := s.activity(), s.provider.OpenCalls()
			if restricted {
				precheck := connectionError("SELECT dbc_ext.dblink_connect($1::text,$2::text) AS value", "2F003", "password or GSSAPI delegated credentials required", "port_live", withoutSecret)
				precheck.Diagnostic.Detail = "Non-superusers must provide a password in the connection string or send delegated GSSAPI credentials."
				s.run(precheck)
			}
			s.run(connectionError("SELECT dbc_ext.dblink_connect($1::text,$2::text) AS value", "42710", "duplicate connection name", "port_live", input))
			fresh := connectionError("SELECT dbc_ext.dblink_connect($1::text,$2::text) AS value", "08001", "could not establish connection", "port_retry", input)
			fresh.Diagnostic.Detail = `invalid port number: "` + port + `"`
			s.run(fresh)
			s.run(fresh)                                         // A failed validation must release the pending name each time.
			s.run(parserFailure(input, fresh.Diagnostic.Detail)) // Existing default survives.
			s.list("port_live")
			s.unchangedActivity(before)
			if !s.b.PG && s.provider.OpenCalls() != calls {
				s.t.Fatal("duplicate/precheck/range rejection entered provider Open")
			}
			if s.pid(app) != namedPID {
				s.t.Fatal("numeric range failure replaced named backend")
			}
			s.remoteText(new("port_live"), namedPID, "SELECT v FROM port_state", "named preserved")
			s.remoteText(nil, defaultPID, "SELECT v FROM port_state", "default preserved")
			retryApp := fmt.Sprintf("dbc_port_retry_%d", connectionSequence.Add(1))
			s.run(connectionText("SELECT dbc_ext.dblink_connect($1::text,$2::text) AS value", "OK", "port_retry", s.conninfo(retryApp, true)))
			s.list("port_live", "port_retry")
			s.disconnect(new("port_retry"), s.pid(retryApp))
		}
	}
	s.exec("RESET SESSION AUTHORIZATION")
	s.disconnect(new("port_live"), namedPID)
	s.disconnect(nil, defaultPID)
	s.list()
}
