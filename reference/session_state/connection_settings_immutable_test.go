package reference_test

import (
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceConnectionSettingsImmutable(t *testing.T) {
	for _, tc := range connectionSettings {
		if tc.context == "user" {
			continue
		}
		t.Run(tc.name, func(t *testing.T) {
			connectionCases(t, nil, func(s *physicalCase) {
				initial := connectionInitial(tc, s.pg)
				s.connectionSentinelSetup()
				peer, err := s.connPoolPeer()
				if err != nil {
					s.t.Fatal(err)
				}
				defer peer.conn.Close()
				for _, prepared := range []bool{false, true} {
					for _, value := range []string{initial, "", "bogus", "-1", "2147483648", "/nonexistent/connection-file"} {
						for _, prefix := range []string{"SET ", "SET SESSION "} {
							s.run(physicalError(prefix+tc.name+"="+physicalLiteral(value), connectionContext(tc), prepared, false))
							s.connectionRead(tc, initial, prepared)
						}
					}
					for _, query := range []string{"SET " + tc.name + "=DEFAULT", "RESET " + tc.name, "SET \"" + strings.ToUpper(tc.name) + "\"='bogus'"} {
						s.run(physicalError(query, connectionContext(tc), prepared, false))
					}
					for _, name := range []string{tc.name, strings.ToUpper(tc.name)} {
						query := "SET \"" + name + "\"='a','b'"
						diagnostic := physicalDiagnostic("22023", "SET "+name+" takes only one argument")
						if tc.name == "listen_addresses" || tc.name == "unix_socket_directories" || tc.name == "oauth_validator_libraries" {
							diagnostic = connectionContext(tc)
						}
						s.run(physicalError(query, diagnostic, prepared, false))
					}
					for _, value := range []any{initial, "", "bogus", nil} {
						for _, local := range []any{false, true, nil} {
							s.run(physicalError("SELECT set_config($1,$2,$3)", connectionContext(tc), prepared, true, tc.name, value, local))
						}
					}
					s.run(fixture.Exec("BEGIN", 0))
					for _, query := range []string{"SET LOCAL " + tc.name + "='bogus'", "SET LOCAL " + tc.name + "=DEFAULT", "RESET " + tc.name} {
						s.run(fixture.Exec("SAVEPOINT connection_expected", 0))
						s.run(physicalError(query, connectionContext(tc), prepared, false))
						s.run(fixture.Exec("ROLLBACK TO connection_expected", 0))
						s.connectionRead(tc, initial, prepared)
						s.connectionSentinel()
						s.run(fixture.Exec("RELEASE connection_expected", 0))
					}
					s.run(fixture.Exec("COMMIT", 0))
				}
				for _, query := range []string{"SET " + tc.name + "='bogus'", "RESET " + tc.name, "SELECT set_config('" + tc.name + "','bogus',false)"} {
					stmt, err := s.conn.PrepareContext(s.p.Ctx, query)
					if err != nil {
						s.t.Fatal("immutable mutation must prepare", query, err)
					}
					s.connectionRead(tc, initial, false)
					for range 2 {
						step := physicalError(query, connectionContext(tc), false, strings.HasPrefix(query, "SELECT"))
						fixture.RunStep(s.t, s.p.Ctx, fixture.HeldStatement{Executor: s.conn, Statement: stmt, SQL: query}, s.recorder, step)
					}
					stmt.Close()
				}
				s.run(fixture.Exec("RESET ALL", 0))
				s.connectionRead(tc, initial, true)
				peer.connectionRead(tc, initial, true)
				s.connectionSentinel()
				peer.connectionSentinel()
			})
		})
	}
}

func TestReferenceConnectionSettingsUnknownAnchors(t *testing.T) {
	roles := []string{"connection_anchor_plain", "connection_anchor_reader"}
	connectionCases(t, roles, func(s *physicalCase) {
		for _, role := range roles {
			s.run(fixture.Exec("CREATE ROLE "+role+" LOGIN PASSWORD 'role-core-fixture'", 0))
		}
		s.run(fixture.Exec("GRANT pg_read_all_settings TO connection_anchor_reader", 0))
		for index, actor := range []*physicalCase{s, s.login(roles[0]), s.login(roles[1])} {
			for _, anchor := range connectionAnchors {
				for _, prepared := range []bool{false, true} {
					diagnostic := physicalDiagnostic("42704", "unrecognized configuration parameter \""+anchor+"\"")
					for _, query := range []string{"SHOW " + anchor, "SELECT current_setting('" + anchor + "')", "SET " + anchor + "='x'", "RESET " + anchor, "SELECT set_config('" + anchor + "','x',false)"} {
						step := physicalError(query, diagnostic, prepared, strings.HasPrefix(query, "SHOW") || strings.HasPrefix(query, "SELECT"))
						if prepared && strings.HasPrefix(query, "SHOW") {
							step.ErrorStage = "prepare"
						}
						actor.run(step)
					}
					for _, missingOK := range []bool{false, true} {
						query := "SELECT current_setting('" + anchor + "',false)"
						if missingOK {
							query = "SELECT current_setting('" + anchor + "',true)"
						}
						var step fixture.Step
						if missingOK {
							step = fixture.Select(query, []string{"current_setting"}, []string{"TEXT"}, [][]any{{nil}})
							step.Prepared = prepared
						} else {
							step = physicalError(query, diagnostic, prepared, true)
						}
						actor.run(step)
					}
					actor.run(physicalError("SET "+anchor+"='a','b'", physicalDiagnostic("22023", "SET "+anchor+" takes only one argument"), prepared, false))
					step := fixture.Select("SELECT name FROM pg_settings WHERE name="+physicalLiteral(anchor), []string{"name"}, []string{"TEXT"}, nil)
					step.Prepared = prepared
					actor.run(step)
				}
			}
			actor.connectionVisible(index != 1, false)
		}
	})
}

func (s *physicalCase) connectionSentinelSetup() {
	s.run(fixture.Exec("CREATE TABLE connection_sentinel (id integer,value text)", 0))
	s.run(fixture.Exec("INSERT INTO connection_sentinel VALUES(1,'kept'),(2,NULL)", 2))
}

func (s *physicalCase) connectionSentinel() {
	s.run(fixture.Select("SELECT id,value FROM connection_sentinel ORDER BY id", []string{"id", "value"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "kept"}, {int64(2), nil}}))
	s.run(fixture.Select("SELECT column_name,data_type,is_nullable FROM information_schema.columns WHERE table_schema='public' AND table_name='connection_sentinel' ORDER BY ordinal_position", []string{"column_name", "data_type", "is_nullable"}, []string{"NAME", "VARCHAR", "VARCHAR"}, [][]any{{"id", "integer", "YES"}, {"value", "text", "YES"}}))
}
