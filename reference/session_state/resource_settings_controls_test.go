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

func resourceNativeControls(t *testing.T) {
	physicalCases(t,nil,func(s *physicalCase) {
		s.run(fixture.Exec("CREATE TABLE resource_control_data(v integer PRIMARY KEY)",0))
		s.run(fixture.Exec("INSERT INTO resource_control_data VALUES(7)",1))
		s.run(fixture.Exec("SET work_mem=1024",0))
		peer:=s.lockPeer(); peer.resourceValue("work_mem","4MB","4096","default",true)
		if s.pg { s.resourceValue("work_mem","1MB","1024","session",true); return }
		pool,recorder:=fixture.RecordedNativePool(s.t,s.p.NativeConnector)
		pool.SetMaxOpenConns(1); pool.SetMaxIdleConns(1)
		conn,err:=pool.Conn(s.p.Ctx); if err!=nil { s.t.Fatal(err) }
		probe:=&physicalCase{s.t,s.p,conn,false,recorder}
		probe.run(fixture.Exec("SET temp_buffers=2048",0))
		probe.run(fixture.Exec("CREATE TEMP TABLE resource_control_temp(v integer)",0))
		probe.run(fixture.Exec("INSERT INTO resource_control_temp VALUES(11)",1))
		probe.run(fixture.Exec("SET work_mem=1024",0))
		identity:=fixture.SettingsPhysical(s.t,conn)
		conn.Close(); conn,err=pool.Conn(s.p.Ctx); if err!=nil { s.t.Fatal(err) }; probe.conn=conn
		if fixture.SettingsPhysical(s.t,conn)!=identity { s.t.Fatal("physical session not reused") }
		probe.resourceValue("work_mem","1MB","1024","session",true)
		probe.run(physicalError("SET temp_buffers='1024'",resourceTempError("1024"),true,false))
		probe.run(fixture.Exec("DROP TABLE resource_control_temp",0))
		var dump bytes.Buffer
		if err:=stuntgres.Dump(s.p.Ctx,s.p.Native,&dump); err!=nil { s.t.Fatal(err) }
		if strings.Contains(dump.String(),"SET work_mem") || strings.Contains(dump.String(),"SET temp_buffers") { s.t.Fatal("Dump serialized live resource settings") }
		for _, action:=range []string{"invalid","reset","restore"} {
			retained,err:=probe.conn.QueryContext(s.p.Ctx,"SELECT setting FROM pg_settings WHERE name='work_mem'"); if err!=nil { s.t.Fatal(err) }
			switch action {
			case "invalid": if err:=stuntgres.Restore(s.p.Ctx,s.p.Native,strings.NewReader("CREATE TABLE broken(")); err==nil { s.t.Fatal("invalid Restore succeeded") }
			case "reset": if err:=stuntgres.Reset(s.p.Ctx,s.p.Native); err!=nil { s.t.Fatal(err) }
			case "restore": if err:=stuntgres.Restore(s.p.Ctx,s.p.Native,bytes.NewReader(dump.Bytes())); err!=nil { s.t.Fatal(err) }
			}
			if !retained.Next() { s.t.Fatal("retained metadata missing") }; var value string
			if err:=retained.Scan(&value); err!=nil || value!="1024" { s.t.Fatal(value,err) }
			if retained.Next() || retained.Err()!=nil { s.t.Fatal("retained metadata cardinality",retained.Err()) }; retained.Close()
			probe.resourceValue("work_mem","1MB","1024","session",true)
			probe.run(physicalError("SET temp_buffers='1024'",resourceTempError("1024"),true,false))
			peer.resourceValue("work_mem","4MB","4096","default",false)
		}
		probe.run(fixture.Select("SELECT v FROM resource_control_data",[]string{"v"},[]string{"INT4"},[][]any{{int64(7)}}))
		// Simulated capacity0 must not disable native logical 2PC.
		probe.resourceValue("max_prepared_transactions","0","0","default",false)
		for _, command:=range []string{"BEGIN","INSERT INTO resource_control_data VALUES(8)","PREPARE TRANSACTION 'resource_simulation_2pc'","COMMIT PREPARED 'resource_simulation_2pc'"} { affected:=int64(0); if strings.HasPrefix(command,"INSERT") { affected=1 }; probe.run(fixture.Exec(command,affected)) }
		probe.run(fixture.Select("SELECT v FROM resource_control_data ORDER BY v",[]string{"v"},[]string{"INT4"},[][]any{{int64(7)},{int64(8)}}))
		if err:=conn.Raw(func(any)error{return driver.ErrBadConn}); !errors.Is(err,driver.ErrBadConn) { s.t.Fatal(err) }; conn.Close()
		conn,err=pool.Conn(s.p.Ctx); if err!=nil { s.t.Fatal(err) }; defer conn.Close(); probe.conn=conn
		if fixture.SettingsPhysical(s.t,conn)==identity { s.t.Fatal("physical session not replaced") }
		probe.resourceValue("work_mem","4MB","4096","default",true)
		probe.run(fixture.Exec("SET temp_buffers=3072",0)); probe.resourceValue("temp_buffers","24MB","3072","session",true)
	})
}

func TestReferenceResourceSettingsSimulationSmoke(t *testing.T) {
	physicalCases(t,nil,func(s *physicalCase) {
		peer:=s.lockPeer()
		s.run(fixture.Exec("CREATE TABLE resource_smoke(v integer PRIMARY KEY CHECK(v>0))",0))
		s.run(fixture.Exec("INSERT INTO resource_smoke VALUES(7)",1))
		s.resourceValue("work_mem","4MB","4096","default",false)
		query:="SELECT current_setting('work_mem')"
		held,err:=s.conn.PrepareContext(s.p.Ctx,query); if err!=nil { s.t.Fatal(err) }; defer held.Close()
		for _, tc:=range []struct{name,input,show,raw string}{{"work_mem","1024kB","1MB","1024"},{"backend_flush_after","256","2MB","256"},{"hash_mem_multiplier","1.25","1.25","1.25"},{"io_combine_limit","128","1MB","128"},{"file_copy_method","COPY","copy","copy"}} {
			step:=fixture.Exec("SET "+tc.name+"="+physicalLiteral(tc.input),0); step.Prepared=true; s.run(step); s.resourceValue(tc.name,tc.show,tc.raw,"session",true)
		}
		fixture.RunStep(s.t,s.p.Ctx,fixture.HeldStatement{Executor:s.conn,Statement:held,SQL:query},s.recorder,clientText(query,"current_setting","1MB"))
		duplicate:=physicalDiagnostic("23505",`duplicate key value violates unique constraint "resource_smoke_pkey"`)
		duplicate.Detail,duplicate.Schema,duplicate.Table,duplicate.Constraint="Key (v)=(7) already exists.","public","resource_smoke","resource_smoke_pkey"
		s.run(physicalError("INSERT INTO resource_smoke VALUES(8),(7)",duplicate,true,false))
		s.run(physicalError("SET vacuum_buffer_usage_limit='127'",resourceValidationError("vacuum_buffer_usage_limit","127"),true,false))
		s.resourceValue("vacuum_buffer_usage_limit","2MB","2048","default",true)
		s.run(physicalError("SET shared_buffers='bogus'",physicalRestart("shared_buffers"),false,false))
		s.run(physicalError("SET file_extend_method='write_zeros'",lockContext("file_extend_method","sighup"),true,false))
		s.run(fixture.Exec("SET temp_buffers=2048",0)); s.run(fixture.Exec("CREATE TEMP TABLE resource_smoke_temp(v integer)",0)); s.run(fixture.Exec("INSERT INTO resource_smoke_temp VALUES(11)",1))
		s.run(physicalError("SET temp_buffers='1024'",resourceTempError("1024"),true,false))
		s.run(fixture.Exec("RESET temp_buffers",0)); s.resourceValue("temp_buffers","8MB","1024","default",false)
		s.run(fixture.Exec("SET temp_buffers=2048",0)); s.resourceValue("temp_buffers","16MB","2048","session",true)
		s.run(fixture.Exec("RESET ALL",0))
		s.resourceValue("work_mem","4MB","4096","default",true)
		s.run(fixture.Select("SELECT v FROM resource_smoke",[]string{"v"},[]string{"INT4"},[][]any{{int64(7)}}))
		s.run(fixture.Select("SELECT v FROM resource_smoke_temp",[]string{"v"},[]string{"INT4"},[][]any{{int64(11)}}))
		peer.resourceValue("work_mem","4MB","4096","default",true)
		peer.resourceValue("temp_buffers","8MB","1024","default",true)
	})
}
