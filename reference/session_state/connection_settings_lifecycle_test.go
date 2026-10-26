package reference_test

import (
 "bytes"
 "database/sql/driver"
 "errors"
 "reflect"
 "strings"
 "testing"

 "stuntgres"
 "stuntgres/reference/fixture"
)

func TestReferenceConnectionSettingsLifecycle(t *testing.T) {
 for _, tc := range connectionSettings {
  if tc.context != "user" { continue }
  t.Run(tc.name,func(t *testing.T) {
   connectionCases(t,nil,func(s *physicalCase) {
    initial, alternate := connectionInitial(tc,s.pg),connectionAlternate(tc)
    peer,err := s.connPoolPeer(); if err != nil { s.t.Fatal(err) }; defer peer.conn.Close()
    query := "SELECT current_setting('"+tc.name+"')"
    held,err := s.conn.PrepareContext(s.p.Ctx,query); if err != nil { s.t.Fatal(err) }; defer held.Close()
    check := func(value string,prepared bool) {
     s.connectionRead(tc,value,prepared)
     fixture.RunStep(s.t,s.p.Ctx,fixture.HeldStatement{Executor:s.conn,Statement:held,SQL:query},s.recorder,fixture.Select(query,[]string{"current_setting"},[]string{"TEXT"},[][]any{{value}}))
     peer.connectionRead(tc,initial,prepared)
    }
    for _, prepared := range []bool{false,true} {
     exec := func(query string) { step := fixture.Exec(query,0); step.Prepared = prepared; s.run(step) }
     for _, end := range []string{"COMMIT","ROLLBACK"} {
      exec("BEGIN"); exec("SET LOCAL "+tc.name+"="+physicalLiteral(alternate.input)); check(alternate.display,prepared)
      exec(end); check(initial,prepared)
      exec("BEGIN")
      step := fixture.Select("SELECT set_config($1,$2,true)",[]string{"set_config"},[]string{"TEXT"},[][]any{{alternate.display}},tc.name,alternate.input); step.Prepared = prepared; s.run(step)
      check(alternate.display,prepared); exec(end); check(initial,prepared)
     }
     exec("BEGIN"); exec("SET "+tc.name+"="+physicalLiteral(alternate.input))
     exec("SAVEPOINT connection_save"); exec("SET LOCAL "+tc.name+"=DEFAULT"); check(initial,prepared)
     exec("ROLLBACK TO connection_save"); check(alternate.display,prepared)
     exec("ROLLBACK"); check(initial,prepared)
     exec("BEGIN"); exec("SET "+tc.name+"="+physicalLiteral(alternate.input)); exec("COMMIT"); check(alternate.display,prepared)
     exec("BEGIN"); exec("RESET ALL"); check(initial,prepared); exec("ROLLBACK"); check(alternate.display,prepared)
     for _, reset := range []string{"SET "+tc.name+"=DEFAULT","RESET "+tc.name} {
      exec(reset); check(initial,prepared); exec("SET "+tc.name+"="+physicalLiteral(alternate.input))
     }
     step := fixture.Select("SELECT set_config($1,$2,false)",[]string{"set_config"},[]string{"TEXT"},[][]any{{initial}},tc.name,nil); step.Prepared = prepared; s.run(step)
     check(initial,prepared)
    }
    var notices []fixture.Diagnostic
    _,noticeConn,recorder := fixture.NoticeReferencePool(s.t,s.p,s.pg,&notices)
    defer noticeConn.Close()
    observed := &physicalCase{s.t,s.p,noticeConn,s.pg,recorder}
    for _, prepared := range []bool{false,true} {
     notices = nil
     step := fixture.Exec("SET LOCAL "+tc.name+"="+physicalLiteral(alternate.input),0)
     step.Prepared = prepared
     observed.run(step)
     want := []fixture.Diagnostic{{Severity:"WARNING",SeverityUnlocalized:"WARNING",Code:"25P01",Message:"SET LOCAL can only be used in transaction blocks"}}
     if !reflect.DeepEqual(notices,want) { s.t.Fatalf("outside-transaction LOCAL notices %#v want %#v",notices,want) }
     observed.connectionRead(tc,initial,prepared)
    }
   })
  })
 }
 t.Run("physical-reuse-replacement-controls",connectionSettingsControls)
}

func connectionSettingsControls(t *testing.T) {
 connectionCases(t,nil,func(s *physicalCase) {
  pool := s.p.Memory; if s.pg { pool = s.p.Postgres }
  pool.SetMaxOpenConns(1); pool.SetMaxIdleConns(1)
  s.connectionSentinelSetup()
  for _, tc := range connectionSettings { if tc.context == "user" { value := connectionAlternate(tc); s.run(fixture.Exec("SET "+tc.name+"="+physicalLiteral(value.input),0)) } }
  before := fixture.SettingsPhysical(t,s.conn)
  if err := s.conn.Close(); err != nil { s.t.Fatal(err) }
  conn,err := pool.Conn(s.p.Ctx); if err != nil { s.t.Fatal(err) }; defer conn.Close(); s.conn = conn
  if fixture.SettingsPhysical(t,conn) != before { s.t.Fatal("physical session not reused") }
  for _, tc := range connectionSettings { if tc.context == "user" { s.connectionRead(tc,connectionAlternate(tc).display,true) } }
  if !s.pg {
   pool.SetMaxOpenConns(2)
   peer,err := s.connPoolPeer(); if err != nil { s.t.Fatal(err) }; defer peer.conn.Close()
   var dump bytes.Buffer
   if err := stuntgres.Dump(s.p.Ctx,s.p.Native,&dump); err != nil { s.t.Fatal(err) }
   for _, tc := range connectionSettings { if strings.Contains(dump.String(),tc.name) { s.t.Fatal("Dump serialized setting",tc.name) } }
   for _, control := range []string{"reset","restore"} {
    query := "SELECT id,value FROM connection_sentinel ORDER BY id"
    if control == "restore" { query = "SELECT name,setting FROM pg_settings WHERE name='scram_iterations'" }
    retained,err := s.conn.QueryContext(s.p.Ctx,query)
    if err != nil { s.t.Fatal(err) }
    if control == "reset" { err = stuntgres.Reset(s.p.Ctx,s.p.Native) } else { err = stuntgres.Restore(s.p.Ctx,s.p.Native,bytes.NewReader(dump.Bytes())) }
    if err != nil { retained.Close(); s.t.Fatal(err) }
    seen := 0
    for retained.Next() {
     if control == "reset" {
      var id int64; var value any
      if err := retained.Scan(&id,&value); err != nil { s.t.Fatal(err) }
      seen++
      if id != int64(seen) || (id == 1 && value != "kept") || (id == 2 && value != nil) { s.t.Fatal(id,value) }
     } else {
      var name,value string; if err := retained.Scan(&name,&value); err != nil { s.t.Fatal(err) }
      if name != "scram_iterations" || value != "8192" { s.t.Fatal(name,value) }; seen++
     }
    }
    want := 2; if control == "restore" { want = 1 }
    if retained.Err() != nil || seen != want { s.t.Fatal("retained rows",seen,retained.Err()) }; retained.Close()
    for _, tc := range connectionSettings { if tc.context == "user" {
     s.connectionRead(tc,connectionAlternate(tc).display,true)
     peer.connectionRead(tc,connectionInitial(tc,false),false)
    } }
    if control == "reset" {
     s.run(fixture.Select("SELECT table_name FROM information_schema.tables WHERE table_schema='public' AND table_name='connection_sentinel'",[]string{"table_name"},[]string{"NAME"},nil))
    } else { s.connectionSentinel() }
   }
   peer.conn.Close(); pool.SetMaxOpenConns(1)
  }
  s.connectionSentinel()
  if err := conn.Raw(func(any) error { return driver.ErrBadConn }); !errors.Is(err,driver.ErrBadConn) { s.t.Fatal(err) }
  conn.Close()
  replacement,err := pool.Conn(s.p.Ctx); if err != nil { s.t.Fatal(err) }; defer replacement.Close(); s.conn = replacement
  if fixture.SettingsPhysical(t,replacement) == before { s.t.Fatal("physical session not replaced") }
  for _, tc := range connectionSettings { s.connectionRead(tc,connectionInitial(tc,s.pg),true) }
  s.connectionSentinel()
 })
}
