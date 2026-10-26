package reference_test

import (
 "context"
 "database/sql"
 "strings"
 "testing"
 "time"

 "stuntgres/reference/fixture"
)

var connectionVisibilityRoles = []string{"connection_plain","connection_reader","connection_bridge","connection_inherited","connection_monitor","connection_setonly","connection_super","connection_supermember","connection_parameter"}

func (s *physicalCase) connectionAuthorityReads(allowed,prepared bool) {
 for _, tc := range connectionSettings {
  if !tc.restricted || allowed { s.connectionRead(tc,connectionInitial(tc,s.pg),prepared); continue }
  for _, name := range []string{tc.name,strings.ToUpper(tc.name)} {
   for _, query := range []string{"SHOW \""+name+"\"","SELECT current_setting('"+name+"')"} {
    step := physicalError(query,physicalDenied(name),prepared,true)
    if prepared && strings.HasPrefix(query,"SHOW") { step.ErrorStage = "prepare" }
    s.run(step)
   }
  }
 }
 s.connectionVisible(allowed,prepared)
}

func TestReferenceConnectionSettingsVisibility(t *testing.T) {
 connectionCases(t,connectionVisibilityRoles,func(s *physicalCase) {
  for _, role := range connectionVisibilityRoles {
   attributes := "LOGIN PASSWORD 'role-core-fixture'"
   if role == "connection_super" { attributes += " SUPERUSER" }
   s.run(fixture.Exec("CREATE ROLE "+role+" "+attributes,0))
  }
  for _, query := range []string{
   "GRANT pg_read_all_settings TO connection_reader",
   "GRANT pg_read_all_settings TO connection_bridge",
   "GRANT connection_bridge TO connection_inherited",
   "GRANT pg_monitor TO connection_monitor",
   "GRANT connection_reader TO connection_setonly WITH INHERIT FALSE, SET TRUE",
   "GRANT connection_super TO connection_supermember",
  } { s.run(fixture.Exec(query,0)) }
  defer func() {
   ctx,cancel := context.WithTimeout(context.Background(),5*time.Second); defer cancel()
   for _, tc := range connectionSettings {
    if tc.context == "user" { continue }
    if _,err := s.conn.ExecContext(ctx,"REVOKE SET ON PARAMETER "+tc.name+" FROM connection_parameter"); err != nil { s.t.Error("parameter privilege cleanup",tc.name,err) }
   }
  }()
  for _, tc := range connectionSettings { if tc.context != "user" { s.run(fixture.Exec("GRANT SET ON PARAMETER "+tc.name+" TO connection_parameter",0)) } }
  for _, prepared := range []bool{false,true} { s.connectionAuthorityReads(true,prepared) }
  for _, actor := range []struct{ role string; allowed bool }{
   {"connection_plain",false},{"connection_reader",true},{"connection_inherited",true},
   {"connection_monitor",true},{"connection_setonly",false},{"connection_supermember",false},{"connection_parameter",false},
  } {
   user := s.login(actor.role)
   user.run(fixture.Select("SELECT session_user,current_user",[]string{"session_user","current_user"},[]string{"NAME","NAME"},[][]any{{actor.role,actor.role}}))
   for _, prepared := range []bool{false,true} {
    user.connectionAuthorityReads(actor.allowed,prepared)
    for _, tc := range connectionSettings {
     if tc.context == "user" { continue }
     user.run(physicalError("SET "+tc.name+"='bogus'",connectionContext(tc),prepared,false))
     user.run(physicalError("SELECT set_config($1,$2,$3)",connectionContext(tc),prepared,true,tc.name,nil,false))
    }
   }
   if actor.role == "connection_setonly" {
    user.run(fixture.Exec("SET ROLE connection_reader",0)); user.connectionAuthorityReads(true,true)
    user.run(fixture.Exec("RESET ROLE",0)); user.connectionAuthorityReads(false,true)
   }
   user.conn.Close()
  }
 })
}

type connectionHeldReader struct { query,name,kind string; stmt *sql.Stmt }

func (s *physicalCase) connectionPrepareReaders(show bool) []connectionHeldReader {
 readers := []connectionHeldReader{
  {query:"SHOW ALL",kind:"all"},
  {query:"SELECT name FROM pg_settings WHERE name IN ("+connectionNames()+") ORDER BY name",kind:"catalog"},
 }
 for _, tc := range connectionSettings {
  if !tc.restricted { continue }
  if show { readers = append(readers,connectionHeldReader{query:"SHOW "+tc.name,name:tc.name,kind:"show"}) }
  readers = append(readers,connectionHeldReader{query:"SELECT current_setting('"+tc.name+"')",name:tc.name,kind:"current"})
 }
 for i := range readers {
  stmt,err := s.conn.PrepareContext(s.p.Ctx,readers[i].query); if err != nil { s.t.Fatal(readers[i].query,err) }
  readers[i].stmt = stmt
  s.t.Cleanup(func(){stmt.Close()})
 }
 return readers
}

func (s *physicalCase) connectionHeldReads(readers []connectionHeldReader,allowed bool) {
 for _, reader := range readers {
  if reader.kind == "all" {
   rows,err := reader.stmt.QueryContext(s.p.Ctx); if err != nil { s.t.Fatal(err) }
   s.connectionCheckAll(rows,allowed); rows.Close(); continue
  }
  var step fixture.Step
  if reader.kind == "catalog" {
   var want [][]any
   for _, tc := range connectionSettings { if allowed || !tc.restricted { want = append(want,[]any{tc.name}) } }
   step = fixture.Select(reader.query,[]string{"name"},[]string{"TEXT"},want)
  } else if !allowed {
   step = physicalError(reader.query,physicalDenied(reader.name),false,true)
  } else {
   column := "current_setting"; if reader.kind == "show" { column = reader.name }
   for _, tc := range connectionSettings { if tc.name == reader.name { step = fixture.Select(reader.query,[]string{column},[]string{"TEXT"},[][]any{{connectionInitial(tc,s.pg)}}) } }
  }
  fixture.RunStep(s.t,s.p.Ctx,fixture.HeldStatement{Executor:s.conn,Statement:reader.stmt,SQL:reader.query},s.recorder,step)
 }
}

func TestReferenceConnectionSettingsPreparedAuthorization(t *testing.T) {
 roles := []string{"connection_prepared_plain","connection_prepared_reader","connection_prepared_bridge"}
 connectionCases(t,roles,func(s *physicalCase) {
  for _, role := range roles { s.run(fixture.Exec("CREATE ROLE "+role+" LOGIN PASSWORD 'role-core-fixture'",0)) }
  s.run(fixture.Exec("GRANT pg_read_all_settings TO connection_prepared_bridge",0))
  s.run(fixture.Exec("GRANT connection_prepared_bridge TO connection_prepared_reader",0))
  plain := s.login(roles[0])
  for _, tc := range connectionSettings {
   if !tc.restricted { continue }
   step := physicalError("SHOW "+tc.name,physicalDenied(tc.name),true,true); step.ErrorStage = "prepare"; plain.run(step)
  }
  plainReaders := plain.connectionPrepareReaders(false)
  plain.connectionHeldReads(plainReaders,false)
  s.run(fixture.Exec("GRANT pg_read_all_settings TO connection_prepared_plain",0)); plain.connectionHeldReads(plainReaders,true)
  s.run(fixture.Exec("REVOKE pg_read_all_settings FROM connection_prepared_plain",0)); plain.connectionHeldReads(plainReaders,false)
  readers := s.connectionPrepareReaders(true)
  s.connectionHeldReads(readers,true)
  var retained *sql.Rows
  if !s.pg {
   // Native Rows are materialized under one coherent operation authority.
   // A later role change cannot retroactively redact that owned snapshot.
   var err error
   retained,err = s.conn.QueryContext(s.p.Ctx,"SELECT name,setting FROM pg_settings WHERE name IN ("+connectionNames()+") ORDER BY name")
   if err != nil { s.t.Fatal(err) }
   defer retained.Close()
  }
  s.run(fixture.Exec("SET ROLE connection_prepared_plain",0)); s.connectionHeldReads(readers,false)
  if retained != nil {
   count := 0
   for retained.Next() {
    var name,value string
    if err := retained.Scan(&name,&value); err != nil { s.t.Fatal(err) }
    if count >= len(connectionSettings) { s.t.Fatal("extra retained connection row",name) }
    tc := connectionSettings[count]
    if name != tc.name || value != connectionRaw(tc,connectionInitial(tc,false)) { s.t.Fatal("retained authority snapshot",name,value,tc.name) }
    count++
   }
   if retained.Err() != nil || count != 38 { s.t.Fatal("retained authority count",count,retained.Err()) }
   retained.Close()
  }
  s.run(fixture.Exec("RESET ROLE",0)); s.connectionHeldReads(readers,true)
  reader := s.login(roles[1])
  reader.run(fixture.Select("SELECT session_user,current_user",[]string{"session_user","current_user"},[]string{"NAME","NAME"},[][]any{{roles[1],roles[1]}}))
  held := reader.connectionPrepareReaders(true)
  reader.connectionHeldReads(held,true)
  s.run(fixture.Exec("REVOKE pg_read_all_settings FROM connection_prepared_bridge",0)); reader.connectionHeldReads(held,false)
  s.run(fixture.Exec("GRANT pg_read_all_settings TO connection_prepared_bridge",0)); reader.connectionHeldReads(held,true)
 })
}
