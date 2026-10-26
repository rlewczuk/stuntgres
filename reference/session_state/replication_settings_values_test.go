package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func replicationTimeoutError(input string) fixture.Diagnostic {
	message, hint := `invalid value for parameter "wal_sender_timeout": "`+input+`"`, ""
	switch input {
	case "-1": message = `-1 ms is outside the valid range for parameter "wal_sender_timeout" (0 ms .. 2147483647 ms)`
	case "2147483648": hint = "Value exceeds integer range."
	case "1 MS": hint = `Valid units for this parameter are "us", "ms", "s", "min", "h", and "d".`
	}
	d := physicalDiagnostic("22023",message); d.Hint = hint; return d
}

func (s *physicalCase) replicationData() {
	s.t.Helper()
	duplicate := physicalDiagnostic("23505", `duplicate key value violates unique constraint "replication_mutable_data_pkey"`)
	duplicate.Detail, duplicate.Schema, duplicate.Table, duplicate.Constraint = "Key (id)=(1) already exists.", "public", "replication_mutable_data", "replication_mutable_data_pkey"
	s.run(physicalError("INSERT INTO replication_mutable_data VALUES(3,'new'),(1,'duplicate')",duplicate,false,false))
	s.run(fixture.Select("SELECT id,value FROM replication_mutable_data ORDER BY id", []string{"id","value"}, []string{"INT4","TEXT"}, [][]any{{int64(1),"kept"},{int64(2),nil}}))
}
func (s *physicalCase) replicationMutableSetup() *physicalCase {
	s.t.Helper()
	s.run(fixture.Exec("CREATE TABLE replication_mutable_data(id integer PRIMARY KEY,value text)",0))
	s.run(fixture.Exec("INSERT INTO replication_mutable_data VALUES(1,'kept'),(2,NULL)",2))
	peer,err := s.connPoolPeer(); if err != nil { s.t.Fatal(err) }; s.t.Cleanup(func(){ peer.conn.Close() })
	return peer
}
func (s *physicalCase) replicationSetConfig(name string, input any, local any, want string, prepared bool) {
	s.t.Helper()
	step := fixture.Select("SELECT set_config($1,$2,$3)", []string{"set_config"}, []string{"TEXT"}, [][]any{{want}},name,input,local)
	step.Prepared = prepared
	step.CheckMetadata = func(t *testing.T, rows *sql.Rows) { settingsTextMetadata(t,rows,s.pg,true) }
	s.run(step)
	step = fixture.Exec("SELECT set_config($1,$2,$3)",1,name,input,local); step.Prepared = prepared; s.run(step)
}
func (s *physicalCase) replicationTransactions(name, original, local, session, nested string, prepared bool) {
	s.t.Helper()
	set := func(prefix,value string) { step := fixture.Exec(prefix+name+"="+physicalLiteral(value),0); step.Prepared = prepared; s.run(step) }
	read := func(value string) { step := fixture.Select("SHOW "+name, []string{name}, []string{"TEXT"}, [][]any{{value}}); step.Prepared = prepared; s.run(step) }
	set("SET ",original)
	for _, end := range []string{"ROLLBACK","COMMIT"} {
		s.run(fixture.Exec("BEGIN",0)); set("SET LOCAL ",local); read(local)
		s.run(fixture.Exec("SAVEPOINT replication_local",0)); set("SET LOCAL ",nested); read(nested)
		s.run(fixture.Exec("ROLLBACK TO SAVEPOINT replication_local",0)); read(local)
		s.run(fixture.Exec("RELEASE SAVEPOINT replication_local",0)); s.run(fixture.Exec(end,0)); read(original)
	}
	s.run(fixture.Exec("BEGIN",0)); set("SET ",session); read(session); s.run(fixture.Exec("ROLLBACK",0)); read(original)
	s.run(fixture.Exec("BEGIN",0)); set("SET ",session); read(session); s.run(fixture.Exec("COMMIT",0)); read(session); set("SET ",original)
	s.run(fixture.Exec("BEGIN",0)); s.replicationSetConfig(name,local,true,local,prepared); read(local); s.run(fixture.Exec("COMMIT",0)); read(original)
	s.replicationData()
}

func TestReferenceReplicationSettingsWalSenderTimeout(t *testing.T) {
	physicalCases(t, []string{"replication_timeout_plain"},func(s *physicalCase) {
		peer := s.replicationMutableSetup()
		s.run(fixture.Exec("CREATE ROLE replication_timeout_plain LOGIN PASSWORD 'role-core-fixture'",0))
		plain := s.login("replication_timeout_plain")
		for _, prepared := range []bool{false,true} {
			for _, actor := range []*physicalCase{s,plain} {
				actor.replicationValue("wal_sender_timeout","1min","60000","default",prepared)
				for _, tc := range []struct{ input,show,raw string }{{"1500ms","1500ms","1500"},{"2min","2min","120000"},{"0","0","0"},{"2147483647","2147483647ms","2147483647"}} {
					step := fixture.Exec("SET wal_sender_timeout="+physicalLiteral(tc.input),0); step.Prepared = prepared; actor.run(step)
					actor.replicationValue("wal_sender_timeout",tc.show,tc.raw,"session",prepared)
					actor.replicationSetConfig("wal_sender_timeout",tc.input,false,tc.show,prepared)
					peer.replicationValue("wal_sender_timeout","1min","60000","default",prepared)
				}
				actor.replicationSetConfig("wal_sender_timeout","1500ms",false,"1500ms",prepared)
				for _, input := range []string{"-1","2147483648","1 MS","bogus"} {
					actor.run(physicalError("SET wal_sender_timeout="+physicalLiteral(input),replicationTimeoutError(input),prepared,false))
					actor.run(physicalError("SELECT set_config($1,$2,false)",replicationTimeoutError(input),prepared,true,"wal_sender_timeout",input))
					actor.replicationValue("wal_sender_timeout","1500ms","1500","session",prepared)
					s.replicationData(); peer.replicationValue("wal_sender_timeout","1min","60000","default",prepared)
				}
				for _, q := range []string{"SET wal_sender_timeout=DEFAULT","RESET wal_sender_timeout"} {
					step := fixture.Exec(q,0); step.Prepared = prepared; actor.run(step)
					actor.replicationValue("wal_sender_timeout","1min","60000","default",prepared)
					actor.replicationSetConfig("wal_sender_timeout","1500ms",false,"1500ms",prepared)
				}
				actor.replicationSetConfig("wal_sender_timeout",nil,nil,"1min",prepared)
				actor.replicationValue("wal_sender_timeout","1min","60000","default",prepared)
			}
			s.replicationTransactions("wal_sender_timeout","1500ms","2s","3s","4s",prepared)
			s.run(fixture.Exec("BEGIN",0)); s.run(fixture.Exec("SAVEPOINT replication_invalid",0))
			s.run(physicalError("SET wal_sender_timeout='-1'",replicationTimeoutError("-1"),prepared,false))
			s.run(physicalError("SELECT 1",physicalDiagnostic("25P02","current transaction is aborted, commands ignored until end of transaction block"),false,true))
			s.run(fixture.Exec("ROLLBACK TO SAVEPOINT replication_invalid",0)); s.replicationValue("wal_sender_timeout","1500ms","1500","session",prepared)
			s.run(fixture.Exec("RELEASE SAVEPOINT replication_invalid",0)); s.run(fixture.Exec("COMMIT",0))
			// A held reader sees execution-time state, not its preparation value.
			q := "SELECT current_setting('wal_sender_timeout')"
			stmt,err := s.conn.PrepareContext(s.p.Ctx,q); if err != nil { t.Fatal(err) }
			s.replicationSetConfig("wal_sender_timeout","2s",false,"2s",prepared)
			fixture.RunStep(t,s.p.Ctx,fixture.HeldStatement{Executor:s.conn,Statement:stmt,SQL:q},s.recorder,fixture.Select(q,[]string{"current_setting"},[]string{"TEXT"},[][]any{{"2s"}})); stmt.Close()
			s.run(fixture.Exec("RESET wal_sender_timeout",0))
		}
	})
}

func TestReferenceReplicationSettingsOutputPluginLibraries(t *testing.T) {
	physicalCases(t,nil,func(s *physicalCase) {
		peer := s.replicationMutableSetup()
		for _, prepared := range []bool{false,true} {
			for _, tc := range []struct{ sql,want string }{
				{"pgoutput, test_decoding","pgoutput, test_decoding"},
				{"'Mixed Case', 'a,b'",`"Mixed Case", "a,b"`},
				{"'a,b'",`"a,b"`}, {"''",`""`}, {"'a\"b'",`"a""b"`}, {"'select'",`"select"`}, {"123","123"},
			} {
				step := fixture.Exec("SET output_plugin_libraries TO "+tc.sql,0); step.Prepared = prepared; s.run(step)
				s.replicationValue("output_plugin_libraries",tc.want,tc.want,"session",prepared)
				peer.replicationValue("output_plugin_libraries","pgoutput, test_decoding","pgoutput, test_decoding","default",prepared)
				s.replicationData()
			}
			for _, raw := range []string{`not-a-real-library, "odd name"`,"",`unterminated " list,,`,"/missing/library"} {
				s.replicationSetConfig("output_plugin_libraries",raw,false,raw,prepared)
				s.replicationValue("output_plugin_libraries",raw,raw,"session",prepared)
			}
			s.replicationTransactions("output_plugin_libraries","native_probe","local_probe","session_probe","nested_probe",prepared)
			for _, q := range []string{"SET output_plugin_libraries=DEFAULT","RESET output_plugin_libraries","RESET ALL"} {
				step := fixture.Exec(q,0); step.Prepared = prepared; s.run(step)
				s.replicationValue("output_plugin_libraries","pgoutput, test_decoding","pgoutput, test_decoding","default",prepared)
				s.replicationSetConfig("output_plugin_libraries","override",false,"override",prepared)
			}
			s.replicationSetConfig("output_plugin_libraries",nil,false,"pgoutput, test_decoding",prepared)
			s.replicationValue("output_plugin_libraries","pgoutput, test_decoding","pgoutput, test_decoding","default",prepared)
			s.replicationData()
		}
	})
}
