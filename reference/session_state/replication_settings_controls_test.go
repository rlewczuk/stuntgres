package reference_test

import (
	"bytes"
	"database/sql/driver"
	"errors"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func (s *physicalCase) replicationControls(peer *physicalCase) {
	s.t.Helper()
	// PostgreSQL proves ordinary live-session retention; native replacement
	// controls have no PostgreSQL counterpart.
	s.replicationValue("wal_sender_timeout","1500ms","1500","session",false)
	s.replicationValue("output_plugin_libraries","native_probe","native_probe","session",false)
	peer.replicationValue("wal_sender_timeout","2s","2000","session",false)
	peer.replicationValue("output_plugin_libraries","peer_probe","peer_probe","session",false)
	if s.pg { return }
	var dump bytes.Buffer
	if err := stuntgres.Dump(s.p.Ctx,s.p.Native,&dump); err != nil { s.t.Fatal(err) }
	for _, action := range []string{"invalid","reset","restore"} {
		rows,err := s.conn.QueryContext(s.p.Ctx,"SELECT "+physicalColumnsSQL+" FROM pg_settings WHERE name IN ("+replicationNames()+") ORDER BY name")
		if err != nil { s.t.Fatal(err) }
		statisticsSettingsCheckMetadata(s.t,rows,false,physicalColumns,physicalTypes)
		switch action {
		case "invalid": if err := stuntgres.Restore(s.p.Ctx,s.p.Native,strings.NewReader("not valid SQL")); err == nil { s.t.Fatal("invalid restore succeeded") }
		case "reset": if err := stuntgres.Reset(s.p.Ctx,s.p.Native); err != nil { s.t.Fatal(err) }
		case "restore": if err := stuntgres.Restore(s.p.Ctx,s.p.Native,bytes.NewReader(dump.Bytes())); err != nil { s.t.Fatal(err) }
		}
		count := 0
		statisticsSettingsCheckMetadata(s.t,rows,false,physicalColumns,physicalTypes)
		for rows.Next() {
			cells,dest := make([]any,len(physicalColumns)),make([]any,len(physicalColumns))
			for i := range cells { dest[i] = &cells[i] }
			if err := rows.Scan(dest...); err != nil { s.t.Fatal(err) }; for i := range cells { cells[i] = fixture.Normalize(cells[i]) }
			if count >= len(replicationSettings) { s.t.Fatal("extra retained row",cells) }
			d := replicationSettings[count]; raw,source := d.raw,"default"
			if d.name == "wal_sender_timeout" { raw,source = "1500","session" }; if d.name == "output_plugin_libraries" { raw,source = "native_probe","session" }
			want := []any{d.name,raw,replicationNullable(d.unit),d.category,d.description,replicationNullable(d.extra),d.context,d.kind,source,replicationNullable(d.min),replicationNullable(d.max),nil,d.raw,d.raw,nil,nil,false}
			if !reflect.DeepEqual(cells,want) { s.t.Fatalf("%s retained: %#v want %#v",action,cells,want) }; count++
		}
		if rows.Err() != nil || count != 26 { s.t.Fatal("retained inventory",count,rows.Err()) }
		rows.Close()
		s.replicationInventory(true,true,map[string]string{"wal_sender_timeout":"1500","output_plugin_libraries":"native_probe"})
		peer.replicationInventory(true,true,map[string]string{"wal_sender_timeout":"2000","output_plugin_libraries":"peer_probe"})
		if action == "reset" {
			missing := physicalDiagnostic("42P01",`relation "replication_control_data" does not exist`)
			missing.Position = "15"
			s.run(physicalError("SELECT v FROM replication_control_data",missing,false,true))
		} else { s.run(fixture.Select("SELECT v FROM replication_control_data",[]string{"v"},[]string{"INT4"},[][]any{{int64(7)}})) }
	}
	fresh,err := s.connPoolPeer(); if err != nil { s.t.Fatal(err) }; defer fresh.conn.Close()
	fresh.replicationValue("wal_sender_timeout","1min","60000","default",true)
	fresh.replicationValue("output_plugin_libraries","pgoutput, test_decoding","pgoutput, test_decoding","default",true)
	// A dedicated single-physical pool makes reuse/replacement deterministic.
	pool,recorder := fixture.RecordedNativePool(s.t,s.p.NativeConnector)
	pool.SetMaxOpenConns(1); pool.SetMaxIdleConns(1)
	conn,err := pool.Conn(s.p.Ctx); if err != nil { s.t.Fatal(err) }
	probe := &physicalCase{s.t,s.p,conn,false,recorder}
	probe.replicationSetConfig("wal_sender_timeout","1500ms",false,"1500ms",false)
	probe.replicationSetConfig("output_plugin_libraries","pooled_probe",false,"pooled_probe",false)
	identity := fixture.SettingsPhysical(s.t,conn); conn.Close()
	conn,err = pool.Conn(s.p.Ctx); if err != nil { s.t.Fatal(err) }; probe.conn = conn
	if fixture.SettingsPhysical(s.t,conn) != identity { s.t.Fatal("physical session not reused") }
	probe.replicationValue("wal_sender_timeout","1500ms","1500","session",true)
	probe.replicationValue("output_plugin_libraries","pooled_probe","pooled_probe","session",true)
	if err := conn.Raw(func(any) error { return driver.ErrBadConn }); !errors.Is(err,driver.ErrBadConn) { s.t.Fatal(err) }; conn.Close()
	conn,err = pool.Conn(s.p.Ctx); if err != nil { s.t.Fatal(err) }; defer conn.Close(); probe.conn = conn
	if fixture.SettingsPhysical(s.t,conn) == identity { s.t.Fatal("physical session not replaced") }
	probe.replicationValue("wal_sender_timeout","1min","60000","default",true)
	probe.replicationValue("output_plugin_libraries","pgoutput, test_decoding","pgoutput, test_decoding","default",true)
	probe.run(fixture.Select("SELECT v FROM replication_control_data",[]string{"v"},[]string{"INT4"},[][]any{{int64(7)}}))
}

func TestReferenceReplicationSettingsNativeControls(t *testing.T) {
	physicalCases(t,nil,func(s *physicalCase) {
		s.run(fixture.Exec("CREATE TABLE replication_control_data(v integer PRIMARY KEY)",0))
		s.run(fixture.Exec("INSERT INTO replication_control_data VALUES(7)",1))
		peer,err := s.connPoolPeer(); if err != nil { t.Fatal(err) }; defer peer.conn.Close()
		s.replicationSetConfig("wal_sender_timeout","1500ms",false,"1500ms",false)
		s.replicationSetConfig("output_plugin_libraries","native_probe",false,"native_probe",false)
		peer.replicationSetConfig("wal_sender_timeout","2s",false,"2s",false)
		peer.replicationSetConfig("output_plugin_libraries","peer_probe",false,"peer_probe",false)
		s.replicationControls(peer)
		s.run(fixture.Select("SHOW track_commit_timestamp",[]string{"track_commit_timestamp"},[]string{"TEXT"},[][]any{{"off"}}))
	})
	// Real native tracking remains operative, not a simulated flag. This is a
	// host-configured phase, using the existing public commit inquiry witness.
	db := sessionPool(t,stuntgres.NewDriverWithConfig(stuntgres.DriverConfig{TrackCommitTimestamp:true}),"replication-tracking")
	conn,err := db.Conn(t.Context()); if err != nil { t.Fatal(err) }; defer conn.Close()
	checkTracking := func() {
		t.Helper()
		var current,boot,reset string
		if err := conn.QueryRowContext(t.Context(),"SELECT setting,boot_val,reset_val FROM pg_settings WHERE name='track_commit_timestamp'").Scan(&current,&boot,&reset); err != nil || current != "on" || boot != "on" || reset != "on" { t.Fatal(current,boot,reset,err) }
	}
	checkTracking()
	fixture.LockExec(t,t.Context(),conn,"CREATE TABLE replication_tracking(v integer)",0)
	var dump bytes.Buffer; if err := stuntgres.Dump(t.Context(),db,&dump); err != nil { t.Fatal(err) }
	for _, action := range []string{"initial","reset","restore"} {
		if action == "reset" { if err := stuntgres.Reset(t.Context(),db); err != nil { t.Fatal(err) }; fixture.LockExec(t,t.Context(),conn,"CREATE TABLE replication_tracking(v integer)",0) }
		if action == "restore" { if err := stuntgres.Restore(t.Context(),db,bytes.NewReader(dump.Bytes())); err != nil { t.Fatal(err) } }
		checkTracking()
		var before,after,stamp,lookup time.Time
		if err := conn.QueryRowContext(t.Context(),"SELECT clock_timestamp()").Scan(&before); err != nil { t.Fatal(err) }
		fixture.LockExec(t,t.Context(),conn,"INSERT INTO replication_tracking VALUES(7)",1)
		if err := conn.QueryRowContext(t.Context(),"SELECT clock_timestamp()").Scan(&after); err != nil { t.Fatal(err) }
		var xid string; var origin int64
		if err := conn.QueryRowContext(t.Context(),"SELECT * FROM pg_last_committed_xact()").Scan(&xid,&stamp,&origin); err != nil { t.Fatal(err) }
		id,err := strconv.ParseUint(xid,10,32)
		if err != nil || id < 3 || origin != 0 || stamp.Before(before) || stamp.After(after) || stamp.IsZero() { t.Fatal("genuine commit witness",xid,stamp,origin,err) }
		if err := conn.QueryRowContext(t.Context(),"SELECT pg_xact_commit_timestamp($1)",xid).Scan(&lookup); err != nil || !lookup.Equal(stamp) { t.Fatal("commit lookup",lookup,stamp,err) }
	}
}
