package reference_test

import (
 "crypto/md5"
 "fmt"
 "reflect"
 "testing"

 "stuntgres/reference/fixture"
)

func TestReferenceConnectionSettingsNonEffects(t *testing.T) {
 connectionCases(t,[]string{"connection_smoke_login"},func(s *physicalCase) {
  s.connectionSentinelSetup()
  s.run(fixture.Exec("CREATE ROLE connection_smoke_login LOGIN PASSWORD 'role-core-fixture'",0))
  peer,err := s.connPoolPeer(); if err != nil { s.t.Fatal(err) }; defer peer.conn.Close()
  for _, tc := range connectionSettings {
   initial := connectionInitial(tc,s.pg)
   s.connectionRead(tc,initial,false)
   if tc.context != "user" {
    s.run(physicalError("SET "+tc.name+"='not-an-operative-physical-setting'",connectionContext(tc),false,false))
    s.run(physicalError("SELECT set_config($1,$2,false)",connectionContext(tc),true,true,tc.name,"bogus"))
    s.connectionRead(tc,initial,true)
   } else {
    value := connectionAlternate(tc)
    s.run(fixture.Exec("SET "+tc.name+"="+physicalLiteral(value.input),0))
    s.connectionRead(tc,value.display,true)
   }
   peer.connectionRead(tc,initial,false)
   s.connectionSentinel(); peer.connectionSentinel()
  }
  for _, anchor := range connectionAnchors {
   s.run(physicalError("SHOW "+anchor,physicalDiagnostic("42704","unrecognized configuration parameter \""+anchor+"\""),false,true))
  }
  login := s.login("connection_smoke_login")
  login.run(fixture.Select("SELECT session_user,current_user",[]string{"session_user","current_user"},[]string{"NAME","NAME"},[][]any{{"connection_smoke_login","connection_smoke_login"}}))
  s.run(fixture.Select("SELECT rolname,rolcanlogin,rolpassword FROM pg_roles WHERE rolname='connection_smoke_login'",[]string{"rolname","rolcanlogin","rolpassword"},[]string{"NAME","BOOL","TEXT"},[][]any{{"connection_smoke_login",true,"********"}}))
  if !s.pg { s.connectionNativePasswordBoundary() }
  s.run(fixture.Exec("RESET ALL",0))
  for _, tc := range connectionSettings {
   s.connectionRead(tc,connectionInitial(tc,s.pg),true)
   peer.connectionRead(tc,connectionInitial(tc,s.pg),true)
  }
  s.connectionSentinel(); peer.connectionSentinel()
 })
}

func (s *physicalCase) connectionNativePasswordBoundary() {
 // Native trusted connectors have no password handshake and pg_roles masks
 // verifiers. Do not claim that either observes stored secret bytes. The
 // existing public warning path is observable independently of those limits.
 var notices []fixture.Diagnostic
 _,conn,recorder := fixture.NoticeReferencePool(s.t,s.p,false,&notices)
 defer conn.Close()
 run := func(step fixture.Step) { fixture.RunStep(s.t,s.p.Ctx,conn,recorder,step) }
 for _, query := range []string{"SET password_encryption='md5'","SET scram_iterations=8192","SET md5_password_warnings=off"} { run(fixture.Exec(query,0)) }
 notices = nil
 encoded := fmt.Sprintf("md5%x",md5.Sum([]byte("role-core-fixtureconnection_smoke_login")))
 run(fixture.Exec("ALTER ROLE connection_smoke_login PASSWORD "+physicalLiteral(encoded),0))
 want := []fixture.Diagnostic{{Severity:"WARNING",SeverityUnlocalized:"WARNING",Code:"01P01",Message:"setting an MD5-encrypted password",Detail:"MD5 password support is deprecated and will be removed in a future release of PostgreSQL.",Hint:"Refer to the PostgreSQL documentation for details about migrating to another password type."}}
 if !reflect.DeepEqual(notices,want) { s.t.Fatalf("simulated MD5 setting changed existing warning behavior: %#v want %#v",notices,want) }
}
