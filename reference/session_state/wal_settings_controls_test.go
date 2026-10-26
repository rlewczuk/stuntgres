package reference_test

import (
	"bytes"
	"database/sql/driver"
	"errors"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceWALSettingsControls(t *testing.T) {
	physicalCases(t,nil,func(s *physicalCase) {
		s.run(fixture.Exec("CREATE TABLE wal_control_data(v integer PRIMARY KEY)",0))
		s.run(fixture.Exec("INSERT INTO wal_control_data VALUES(7)",1))
		peer,err := s.connPoolPeer(); if err != nil { t.Fatal(err) }; defer peer.conn.Close()
		for _, m := range walMutable { s.walSet(m.name,m.input,false); s.walValue(m.name,m.show,m.raw,"session",true); d := walDefinition(m.name); peer.walValue(m.name,d.show,d.raw,"default",true) }
		if s.pg { return } // Logical native controls have no PostgreSQL counterpart.
		var dump bytes.Buffer
		if err := stuntgres.Dump(s.p.Ctx,s.p.Native,&dump); err != nil { t.Fatal(err) }
		for _, m := range walMutable { if strings.Contains(dump.String(),m.name) { t.Fatal("logical dump contains session setting",m.name) } }
		for _, action := range []string{"reset","restore"} {
			rows,err := s.conn.QueryContext(s.p.Ctx,"SELECT name,setting FROM pg_settings WHERE name IN ('commit_delay','commit_siblings','synchronous_commit','wal_compression','wal_init_zero','wal_recycle','wal_skip_threshold') ORDER BY name")
			if err != nil { t.Fatal(err) }
			if action == "reset" { err = stuntgres.Reset(s.p.Ctx,s.p.Native) } else { err = stuntgres.Restore(s.p.Ctx,s.p.Native,bytes.NewReader(dump.Bytes())) }
			if err != nil { t.Fatal(err) }
			for _, m := range walMutable { s.walValue(m.name,m.show,m.raw,"session",true); d := walDefinition(m.name); peer.walValue(m.name,d.show,d.raw,"default",true) }
			// A mutation after capture cannot rewrite the already retained rows.
			s.run(fixture.Exec("RESET ALL",0))
			count := 0
			for rows.Next() {
				var name,value string; if err := rows.Scan(&name,&value); err != nil { t.Fatal(err) }
				if count >= len(walMutable) || name != walMutable[count].name || value != walMutable[count].raw { t.Fatal("retained control settings",name,value,count) }; count++
			}
			if rows.Err() != nil || count != 7 { t.Fatal(count,rows.Err()) }; rows.Close()
			for _, m := range walMutable { s.walSet(m.name,m.input,false) }
			if action == "restore" { s.run(fixture.Select("SELECT v FROM wal_control_data",[]string{"v"},[]string{"INT4"},[][]any{{int64(7)}})) } else {
				diagnostic := physicalDiagnostic("42P01",`relation "wal_control_data" does not exist`); diagnostic.Position = "15"
				s.run(physicalError("SELECT v FROM wal_control_data",diagnostic,false,true))
			}
		}
		fresh := sessionPool(t,stuntgres.NewDriver(),"wal-dump-replay")
		if err := stuntgres.Restore(s.p.Ctx,fresh,bytes.NewReader(dump.Bytes())); err != nil { t.Fatal(err) }
		conn,err := fresh.Conn(s.p.Ctx); if err != nil { t.Fatal(err) }; defer conn.Close()
		for _, m := range walMutable { var value string; if err := conn.QueryRowContext(s.p.Ctx,"SHOW "+m.name).Scan(&value); err != nil || value != walDefinition(m.name).show { t.Fatal("fresh replay default",m.name,value,err) } }
		var value int; if err := conn.QueryRowContext(s.p.Ctx,"SELECT v FROM wal_control_data").Scan(&value); err != nil || value != 7 { t.Fatal(value,err) }
		pool,recorder := fixture.RecordedNativePool(t,s.p.NativeConnector)
		pool.SetMaxOpenConns(1); pool.SetMaxIdleConns(1)
		pooled,err := pool.Conn(s.p.Ctx); if err != nil { t.Fatal(err) }
		probe := &physicalCase{t,s.p,pooled,false,recorder}
		for _, m := range walMutable { probe.walSet(m.name,m.input,false) }
		identity := fixture.SettingsPhysical(t,pooled); pooled.Close()
		pooled,err = pool.Conn(s.p.Ctx); if err != nil { t.Fatal(err) }; probe.conn = pooled
		if fixture.SettingsPhysical(t,pooled) != identity { t.Fatal("physical connection not reused") }
		for _, m := range walMutable { probe.walValue(m.name,m.show,m.raw,"session",true) }
		if err := pooled.Raw(func(any) error { return driver.ErrBadConn }); !errors.Is(err,driver.ErrBadConn) { t.Fatal(err) }; pooled.Close()
		pooled,err = pool.Conn(s.p.Ctx); if err != nil { t.Fatal(err) }; defer pooled.Close(); probe.conn = pooled
		if fixture.SettingsPhysical(t,pooled) == identity { t.Fatal("physical connection not replaced") }
		probe.walDefaults(true)
	})
}
