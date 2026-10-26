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

func vacuumNativeControls(t *testing.T) {
 physicalCases(t,nil,func(s *physicalCase) {
  s.run(fixture.Exec("CREATE TABLE vacuum_control_data(v integer PRIMARY KEY)",0))
  s.run(fixture.Exec("INSERT INTO vacuum_control_data VALUES(7)",1))
  peer:=s.lockPeer()
  s.vacuumAssign(vacuumMutations[0],true,false)
  peer.vacuumValue("vacuum_cost_delay","0","0","default",true)
  if s.pg { return } // The public logical controls have no PostgreSQL equivalent.
  pool,recorder:=fixture.RecordedNativePool(t,s.p.NativeConnector)
  pool.SetMaxOpenConns(1); pool.SetMaxIdleConns(1)
  conn,err:=pool.Conn(s.p.Ctx); if err!=nil { t.Fatal(err) }
  defer func(){ if conn!=nil { conn.Close() } }()
  probe:=&physicalCase{t,s.p,conn,false,recorder}
  probe.vacuumAssign(vacuumMutations[0],true,false)
  identity:=fixture.SettingsPhysical(t,conn)
  conn.Close(); conn,err=pool.Conn(s.p.Ctx); if err!=nil { t.Fatal(err) }; probe.conn=conn
  if fixture.SettingsPhysical(t,conn)!=identity { t.Fatal("pool reuse replaced physical identity") }
  probe.vacuumValue("vacuum_cost_delay","1500us","1.5","session",true)
  var dump bytes.Buffer
  if err:=stuntgres.Dump(s.p.Ctx,s.p.Native,&dump); err!=nil { t.Fatal(err) }
  for _,d:=range vacuumSettings { if strings.Contains(dump.String(),d.name) { t.Fatal("Dump serialized live vacuum setting",d.name) } }
  data:=func(actor *physicalCase,present bool) {
   rows:=[][]any{}
   if present { rows=[][]any{{int64(7)}} }
   actor.run(fixture.Select("SELECT v FROM vacuum_control_data ORDER BY v",[]string{"v"},[]string{"INT4"},rows))
  }
  for _,action:=range []string{"invalid","reset","restore"} {
   retained,err:=probe.conn.QueryContext(s.p.Ctx,"SELECT setting,source,reset_val FROM pg_settings WHERE name='vacuum_cost_delay'"); if err!=nil { t.Fatal(err) }
   switch action {
   case "invalid": if err:=stuntgres.Restore(s.p.Ctx,s.p.Native,strings.NewReader("CREATE TABLE broken(")); err==nil { t.Fatal("invalid Restore succeeded") }
   case "reset": if err:=stuntgres.Reset(s.p.Ctx,s.p.Native); err!=nil { t.Fatal(err) }
   case "restore": if err:=stuntgres.Restore(s.p.Ctx,s.p.Native,bytes.NewReader(dump.Bytes())); err!=nil { t.Fatal(err) }
   }
   if !retained.Next() { t.Fatal("retained setting row missing") }
   var value,source,reset string
   if err:=retained.Scan(&value,&source,&reset); err!=nil || value!="1.5" || source!="session" || reset!="0" { t.Fatal("retained metadata",value,source,reset,err) }
   if retained.Next() || retained.Err()!=nil { t.Fatal("retained cardinality",retained.Err()) }; retained.Close()
   probe.vacuumValue("vacuum_cost_delay","1500us","1.5","session",true)
   s.vacuumValue("vacuum_cost_delay","1500us","1.5","session",false)
   peer.vacuumValue("vacuum_cost_delay","0","0","default",false)
   if fixture.SettingsPhysical(t,conn)!=identity { t.Fatal("logical control changed physical identity") }
   if action=="reset" {
    probe.run(fixture.Select("SELECT count(*) FROM pg_tables WHERE schemaname='public' AND tablename='vacuum_control_data'",[]string{"count"},[]string{"INT8"},[][]any{{int64(0)}}))
   } else { data(probe,true); data(peer,true) }
  }
  retained,err:=probe.conn.QueryContext(s.p.Ctx,"SELECT setting FROM pg_settings WHERE name='vacuum_cost_delay'"); if err!=nil { t.Fatal(err) }
  // Consume before ErrBadConn: database/sql owns a Rows callback on this Conn.
  if !retained.Next() { t.Fatal("replacement capture missing") }; var value string
  if err:=retained.Scan(&value); err!=nil || value!="1.5" { t.Fatal(value,err) }; if retained.Next() || retained.Err()!=nil { t.Fatal(retained.Err()) }; retained.Close()
  if err:=conn.Raw(func(any)error{return driver.ErrBadConn}); !errors.Is(err,driver.ErrBadConn) { t.Fatal(err) }; conn.Close()
  conn,err=pool.Conn(s.p.Ctx); if err!=nil { t.Fatal(err) }; probe.conn=conn
  if fixture.SettingsPhysical(t,conn)==identity { t.Fatal("pool failed to replace bad physical session") }
  probe.vacuumValue("vacuum_cost_delay","0","0","default",true)
  peer.vacuumValue("vacuum_cost_delay","0","0","default",true)
  data(probe,true)
  conn.Close(); peer.conn.Close(); s.conn.Close()
  if err:=pool.Close(); err!=nil { t.Fatal(err) }
  if err:=s.p.Memory.Close(); err!=nil { t.Fatal(err) }
  if err:=s.p.Native.Close(); err!=nil { t.Fatal(err) }
  reopened,reopenedRecorder:=fixture.RecordedNativePool(t,s.p.NativeConnector); defer reopened.Close()
  reopenedConn,err:=reopened.Conn(s.p.Ctx); if err!=nil { t.Fatal(err) }; defer reopenedConn.Close()
  fixture.RunStep(t,s.p.Ctx,reopenedConn,reopenedRecorder,fixture.Select("SELECT v FROM vacuum_control_data",[]string{"v"},[]string{"INT4"},[][]any{{int64(7)}}))
  fixture.RunStep(t,s.p.Ctx,reopenedConn,reopenedRecorder,fixture.Select("SHOW vacuum_cost_delay",[]string{"vacuum_cost_delay"},[]string{"TEXT"},[][]any{{"0"}}))
 })
}
