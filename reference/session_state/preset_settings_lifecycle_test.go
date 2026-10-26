package reference_test

import (
	"bytes"
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferencePresetSettingsAuthority(t *testing.T) {
	// Isolate each authenticated role to bound fixture duration.
	for _, role := range []string{"preset_plain","preset_super","preset_reader","preset_grantee"} {
		t.Run(role,func(t *testing.T) {
			physicalCases(t,[]string{role},func(s *physicalCase) {
				attr := " LOGIN PASSWORD 'role-core-fixture'"; if role == "preset_super" { attr += " SUPERUSER" }
				s.run(fixture.Exec("CREATE ROLE "+role+attr,0))
				if role == "preset_reader" { s.run(fixture.Exec("GRANT pg_read_all_settings TO "+role,0)) }
				if role == "preset_grantee" {
					defer func() { ctx,cancel := context.WithTimeout(context.Background(),5*time.Second); defer cancel(); for _, d := range presetDefinitions { if _,err := s.conn.ExecContext(ctx,"REVOKE SET ON PARAMETER "+d.name+" FROM "+role); err != nil { t.Error(err) } } }()
					for _, d := range presetDefinitions { s.run(fixture.Exec("GRANT SET ON PARAMETER "+d.name+" TO "+role,0)) }
				}
				user := s.login(role)
				user.run(fixture.Select("SELECT session_user,current_user",[]string{"session_user","current_user"},[]string{"NAME","NAME"},[][]any{{role,role}}))
				values := user.presetInventory(false)
				for _, prepared := range []bool{false,true} {
					user.presetInventory(prepared); user.presetReads(values,prepared)
					for _, d := range presetDefinitions {
						user.run(physicalError("SET "+d.name+"='bogus'",presetDenied(d.name),prepared,false))
						user.run(physicalError("RESET "+d.name,presetDenied(d.name),prepared,false))
						user.run(physicalError("SELECT set_config($1,$2,$3)",presetDenied(d.name),prepared,true,d.name,nil,false))
					}
				}
			})
		})
	}
}

func TestReferencePresetSettingsLifecycle(t *testing.T) {
	t.Run("physical-pool-reuse",func(t *testing.T) {
		p := fixture.NewScenario(t)
		for _, b := range p.Backends() {
			b.DB.SetMaxOpenConns(1)
			conn,err := b.DB.Conn(p.Ctx); if err != nil { t.Fatal(err) }
			s := &physicalCase{t,p,conn,b.PG,b.Recorder}
			values := s.presetInventory(false)
			s.run(fixture.Exec("SET application_name='preset-pool-reused'",0))
			if err := conn.Close(); err != nil { t.Fatal(err) }
			reused,err := b.DB.Conn(p.Ctx); if err != nil { t.Fatal(err) }
			s.conn = reused
			s.run(fixture.Select("SHOW application_name",[]string{"application_name"},[]string{"TEXT"},[][]any{{"preset-pool-reused"}}))
			if got := s.presetInventory(true); !reflect.DeepEqual(got,values) { t.Fatal(got,values) }
			reused.Close()
		}
	})
	physicalCases(t,nil,func(s *physicalCase) {
		values := s.presetInventory(false)
		s.run(fixture.Exec("CREATE TABLE preset_sentinel (id integer PRIMARY KEY, value text)",0))
		s.run(fixture.Exec("INSERT INTO preset_sentinel VALUES (1,'kept'),(2,NULL)",2))
		s.run(fixture.Exec("SET application_name='preset-retained'",0))
		peer,err := s.connPoolPeer(); if err != nil { t.Fatal(err) }; defer peer.conn.Close()
		if got := peer.presetInventory(true); !reflect.DeepEqual(got,values) { t.Fatal(got,values) }
		q := "SELECT current_setting('segment_size')"
		stmt,err := s.conn.PrepareContext(s.p.Ctx,q); if err != nil { t.Fatal(err) }; defer stmt.Close()
		held := func() { fixture.RunStep(t,s.p.Ctx,fixture.HeldStatement{Executor:s.conn,Statement:stmt,SQL:q},s.recorder,fixture.Select(q,[]string{"current_setting"},[]string{"TEXT"},[][]any{{"1GB"}})) }
		held()
		for _, d := range presetDefinitions { s.run(physicalError("SET "+d.name+"=DEFAULT",presetDenied(d.name),false,false)) }
		s.run(fixture.Exec("RESET ALL",0)); held(); s.presetUnchanged(values,"")
		if s.pg { return } // Native controls are not PostgreSQL operations.
		var dump bytes.Buffer
		if err := stuntgres.Dump(s.p.Ctx,s.p.Native,&dump); err != nil { t.Fatal(err) }
		for _, d := range presetDefinitions { if strings.Contains(dump.String(),d.name) { t.Fatal("serialized setting",d.name) } }
		for _, control := range []string{"reset","restore"} {
			rows,err := s.conn.QueryContext(s.p.Ctx,"SELECT id,value FROM preset_sentinel ORDER BY id"); if err != nil { t.Fatal(err) }
			if control == "reset" { err = stuntgres.Reset(s.p.Ctx,s.p.Native) } else { err = stuntgres.Restore(s.p.Ctx,s.p.Native,bytes.NewReader(dump.Bytes())) }
			if err != nil { rows.Close(); t.Fatal(err) }
			index := 0
			for rows.Next() { var id int; var value *string; if err := rows.Scan(&id,&value); err != nil { t.Fatal(err) }; index++; if id != index || (id == 1 && (value == nil || *value != "kept")) || (id == 2 && value != nil) { t.Fatal(id,value) } }
			if err := rows.Err(); err != nil || index != 2 { t.Fatal(index,err) }; rows.Close()
			held(); if got := s.presetInventory(true); !reflect.DeepEqual(got,values) { t.Fatal(got,values) }
			if control == "reset" { if err := stuntgres.Restore(s.p.Ctx,s.p.Native,bytes.NewReader(dump.Bytes())); err != nil { t.Fatal(err) } }
		}
		s.presetUnchanged(values,"")
	})
}

func TestReferencePresetSettingsSmoke(t *testing.T) {
	physicalCases(t,[]string{"preset_smoke_plain"},func(s *physicalCase) {
		values := s.presetInventory(false) // Includes raw/unit/boot/version distinctions.
		s.run(fixture.Exec("CREATE TABLE preset_smoke (id integer PRIMARY KEY)",0)); s.run(fixture.Exec("INSERT INTO preset_smoke VALUES (7)",1))
		s.run(fixture.Exec("CREATE ROLE preset_smoke_plain LOGIN PASSWORD 'role-core-fixture'",0))
		plain := s.login("preset_smoke_plain"); plain.presetInventory(true)
		queries := []string{"SELECT current_setting('segment_size')","SELECT id FROM preset_smoke"}
		for _, q := range queries {
			stmt,err := s.conn.PrepareContext(s.p.Ctx,q); if err != nil { t.Fatal(err) }; defer stmt.Close()
			check := func() {
				column,kind,value := "current_setting","TEXT",any("1GB"); if strings.Contains(q,"preset_smoke") { column,kind,value = "id","INT4",int64(7) }
				fixture.RunStep(t,s.p.Ctx,fixture.HeldStatement{Executor:s.conn,Statement:stmt,SQL:q},s.recorder,fixture.Select(q,[]string{column},[]string{kind},[][]any{{value}}))
			}
			check()
			s.run(physicalError("SET segment_size='2GB'",presetDenied("segment_size"),false,false)); check()
			s.run(physicalError("SET data_checksums='bogus'",presetDenied("data_checksums"),true,false)); check()
			s.run(fixture.Exec("SET application_name='preset-smoke'",0)); s.run(fixture.Exec("RESET ALL",0)); check()
			s.run(fixture.Select("SHOW application_name",[]string{"application_name"},[]string{"TEXT"},[][]any{{""}}))
		}
		s.presetReads(values,true)
		if got := s.presetInventory(true); !reflect.DeepEqual(got,values) { t.Fatal(got,values) }
	})
}
