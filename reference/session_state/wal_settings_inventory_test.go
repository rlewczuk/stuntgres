package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceWALSettingsInventory(t *testing.T) {
	for _, actor := range []string{"super","plain","reader","setter"} {
		t.Run(actor,func(t *testing.T) {
			roles := []string{"wal_inventory_plain","wal_inventory_reader","wal_inventory_bridge","wal_inventory_setter"}
			physicalCases(t,roles,func(s *physicalCase) {
				for _, role := range roles { s.run(fixture.Exec("CREATE ROLE "+role+" LOGIN PASSWORD 'role-core-fixture'",0)) }
				s.run(fixture.Exec("GRANT pg_read_all_settings TO wal_inventory_bridge",0))
				s.run(fixture.Exec("GRANT wal_inventory_bridge TO wal_inventory_reader",0))
				s.run(fixture.Exec("GRANT SET ON PARAMETER commit_delay TO wal_inventory_setter",0))
				defer s.walRevoke("REVOKE SET ON PARAMETER commit_delay FROM wal_inventory_setter")
				user := s; if actor != "super" { user = s.login("wal_inventory_"+actor) }
				for _, prepared := range []bool{false,true} {
					user.walDefaults(prepared)
					user.walInventory(prepared,actor == "super" || actor == "reader")
					var found = map[string]bool{}
					var rows *sql.Rows
					var err error
					if prepared {
						stmt,e := user.conn.PrepareContext(s.p.Ctx,"SHOW ALL"); if e != nil { t.Fatal(e) }
						defer stmt.Close()
						rows,err = stmt.QueryContext(s.p.Ctx)
					} else { rows,err = user.conn.QueryContext(s.p.Ctx,"SHOW ALL") }
					if err != nil { t.Fatal(err) }
					replicationShowAllMetadata(t,rows,s.pg)
					for rows.Next() {
						var name,value,description string; if err := rows.Scan(&name,&value,&description); err != nil { t.Fatal(err) }
						for _, d := range walSettings { if name == d.name {
							d = walProfile(d,s.pg)
							if found[name] || value != d.show || description != d.description { t.Fatal("WAL SHOW ALL",name,value,description) }; found[name] = true
						} }
					}
					if rows.Err() != nil || len(found) != 40 { t.Fatal("WAL SHOW ALL count",len(found),rows.Err()) }; rows.Close()
				}
			})
		})
	}
}

func TestReferenceWALSettingsContext(t *testing.T) {
	for _, d := range walSettings {
		if d.context != "postmaster" && d.context != "sighup" { continue }
		t.Run(d.name,func(t *testing.T) {
			physicalCases(t,[]string{"wal_context_user"},func(s *physicalCase) {
				d := walProfile(d,s.pg)
				s.run(fixture.Exec("CREATE ROLE wal_context_user LOGIN PASSWORD 'role-core-fixture'",0))
				defer s.walRevoke("REVOKE SET ON PARAMETER "+d.name+" FROM wal_context_user")
				s.run(fixture.Exec("CREATE TABLE wal_context_data(v integer)",0)); s.run(fixture.Exec("INSERT INTO wal_context_data VALUES(7)",1))
				peer,err := s.connPoolPeer(); if err != nil { t.Fatal(err) }; defer peer.conn.Close()
				user := s.login("wal_context_user")
				alternative := "other ' ; $(not-executed)"
				switch d.kind { case "bool": alternative = "off"; if d.raw == "off" { alternative = "on" }; case "integer": alternative = "64"; case "real": alternative = "0.5"; case "enum": alternative = strings.Split(strings.Trim(d.enums,"{}"),",")[0] }
				switch d.name {
				case "wal_decode_buffer_size": alternative = "1MB"
				case "recovery_target": alternative = "immediate"
				case "recovery_target_lsn": alternative = "0/123"
				case "recovery_target_name": alternative = "named_restore_point"
				case "recovery_target_time": alternative = "2026-01-01 00:00:00+00"
				case "recovery_target_timeline": alternative = "current"
				case "recovery_target_xid": alternative = "123"
				}
				for _, granted := range []bool{false,true} {
					if granted { s.run(fixture.Exec("GRANT SET ON PARAMETER "+d.name+" TO wal_context_user",0)) }
					for _, actor := range []*physicalCase{s,user} { for _, prepared := range []bool{false,true} {
						for _, query := range []string{
							"SET "+d.name+"="+physicalLiteral(alternative),"SET "+d.name+"="+physicalLiteral(walReset(d)),"SET "+d.name+"='not valid'",
							"SET "+d.name+"=DEFAULT","RESET "+d.name,
							"SELECT set_config('"+d.name+"','not valid',false)","SELECT set_config('"+d.name+"','not valid',true)","SELECT set_config('"+d.name+"',NULL,false)","SELECT set_config('"+d.name+"',NULL,true)",
							"SELECT set_config('"+d.name+"','not valid',NULL)",
						} { actor.run(physicalError(query,walContext(d),prepared,strings.HasPrefix(query,"SELECT"))) }
						actor.run(physicalError("SET "+d.name+"='a','b'",physicalDiagnostic("22023","SET "+d.name+" takes only one argument"),prepared,false))
						actor.run(fixture.Exec("BEGIN",0))
						actor.run(physicalError("SET LOCAL "+d.name+"="+physicalLiteral(alternative),walContext(d),prepared,false))
						actor.run(physicalError("SELECT 1",physicalDiagnostic("25P02","current transaction is aborted, commands ignored until end of transaction block"),false,true))
						actor.run(fixture.Exec("ROLLBACK",0))
						actor.walValue(d.name,d.show,d.raw,walSource(d,s.pg),prepared)
					} }
				}
				peer.walValue(d.name,d.show,d.raw,walSource(d,s.pg),false)
				s.run(fixture.Exec("RESET ALL",0))
				s.walValue(d.name,d.show,d.raw,walSource(d,s.pg),false)
				s.run(fixture.Select("SELECT v FROM wal_context_data",[]string{"v"},[]string{"INT4"},[][]any{{int64(7)}}))
			})
		})
	}
}
