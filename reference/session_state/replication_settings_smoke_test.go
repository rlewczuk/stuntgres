package reference_test

import (
	"context"
	"testing"
	"time"

	"stuntgres/reference/fixture"
)

func TestReferenceReplicationSettingsSmoke(t *testing.T) {
	physicalCases(t,[]string{"replication_smoke_plain"},func(s *physicalCase) {
		s.run(fixture.Exec("CREATE TABLE replication_control_data(v integer PRIMARY KEY)",0))
		s.run(fixture.Exec("INSERT INTO replication_control_data VALUES(7)",1))
		s.run(fixture.Exec("CREATE ROLE replication_smoke_plain LOGIN PASSWORD 'role-core-fixture'",0))
		plain := s.login("replication_smoke_plain")
		s.replicationInventory(true,false,nil)
		plain.replicationInventory(false,true,nil)
		s.replicationValue("wal_sender_timeout","1min","60000","default",false)
		s.run(fixture.Exec("SET wal_sender_timeout='1500ms'",0))
		s.replicationValue("wal_sender_timeout","1500ms","1500","session",true)
		s.run(physicalError("SET wal_sender_timeout=-1",replicationTimeoutError("-1"),true,false))
		s.replicationValue("wal_sender_timeout","1500ms","1500","session",false)
		s.run(physicalError("SET hot_standby=off",physicalRestart("hot_standby"),false,false))
		s.run(fixture.Select("SHOW hot_standby",[]string{"hot_standby"},[]string{"TEXT"},[][]any{{"on"}}))
		s.run(physicalError("SET hot_standby_feedback=on",physicalDiagnostic("55P02",`parameter "hot_standby_feedback" cannot be changed now`),true,false))
		s.run(fixture.Select("SHOW hot_standby_feedback",[]string{"hot_standby_feedback"},[]string{"TEXT"},[][]any{{"off"}}))
		s.replicationSetConfig("output_plugin_libraries","native_probe",false,"native_probe",true)
		plain.run(physicalError("SELECT current_setting('output_plugin_libraries',true)",physicalDenied("output_plugin_libraries"),true,true))
		plain.run(physicalError("SET output_plugin_libraries='plain_probe'",replicationSetDenied(),false,false))
		s.run(fixture.Exec("GRANT SET ON PARAMETER output_plugin_libraries TO replication_smoke_plain",0))
		defer func(){ ctx,cancel := context.WithTimeout(context.Background(),5*time.Second); defer cancel(); if _,err := s.conn.ExecContext(ctx,"REVOKE SET ON PARAMETER output_plugin_libraries FROM replication_smoke_plain"); err != nil { s.t.Error(err) } }()
		plain.run(fixture.Exec("SET output_plugin_libraries='granted_probe'",0))
		plain.run(physicalError("SELECT set_config('output_plugin_libraries','rejected_probe',false)",physicalDenied("output_plugin_libraries"),true,true))
		s.run(fixture.Exec("GRANT pg_read_all_settings TO replication_smoke_plain",0))
		plain.replicationValue("output_plugin_libraries","granted_probe","granted_probe","session",true)
		s.run(fixture.Exec("REVOKE pg_read_all_settings FROM replication_smoke_plain",0))
		plain.run(physicalError("SELECT current_setting('output_plugin_libraries')",physicalDenied("output_plugin_libraries"),false,true))
		plain.run(fixture.Exec("RESET output_plugin_libraries",0))
		s.run(fixture.Exec("REVOKE SET ON PARAMETER output_plugin_libraries FROM replication_smoke_plain",0))
		duplicate := physicalDiagnostic("23505", `duplicate key value violates unique constraint "replication_control_data_pkey"`)
		duplicate.Detail, duplicate.Schema, duplicate.Table, duplicate.Constraint = "Key (v)=(7) already exists.", "public", "replication_control_data", "replication_control_data_pkey"
		s.run(physicalError("INSERT INTO replication_control_data VALUES(8),(7)",duplicate,true,false))
		s.run(fixture.Select("SELECT v FROM replication_control_data",[]string{"v"},[]string{"INT4"},[][]any{{int64(7)}}))
		peer,err := s.connPoolPeer(); if err != nil { t.Fatal(err) }; defer peer.conn.Close()
		peer.replicationSetConfig("wal_sender_timeout","2s",false,"2s",false)
		peer.replicationSetConfig("output_plugin_libraries","peer_probe",false,"peer_probe",false)
		s.replicationControls(peer)
	})
}
