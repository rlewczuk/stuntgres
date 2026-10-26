package reference_test

import (
 "context"
 "strconv"
 "testing"
 "time"
 "stuntgres/reference/fixture"
)

func TestReferenceVacuumSettingsContext(t *testing.T) {
 for _,d:=range vacuumSettings {
  if d.context=="user" { continue }
  t.Run(d.name,func(t *testing.T) {
   physicalCases(t,[]string{"vacuum_context_login"},func(s *physicalCase) {
    s.run(fixture.Exec("CREATE ROLE vacuum_context_login LOGIN PASSWORD 'role-core-fixture'",0))
    ordinary:=s.login("vacuum_context_login"); peer:=s.lockPeer()
    s.vacuumLogicalSetup("vacuum_context_login")
    for _,observer:=range []*physicalCase{s,ordinary,peer} { observer.vacuumLogicalRows(false) }
    changed:="off"
    if d.kind!="bool" { n,err:=strconv.ParseFloat(d.raw,64); if err!=nil { t.Fatal(err) }; changed=strconv.FormatFloat(n+1,'f',-1,64) }
    for phase,observer:=range []*physicalCase{s,ordinary,ordinary} {
     if phase==2 {
      s.run(fixture.Exec("GRANT SET ON PARAMETER "+d.name+" TO vacuum_context_login",0))
      // Cleanup survives a fatal assertion and uses a context independent of the scenario.
      defer func() { ctx,cancel:=context.WithTimeout(context.Background(),5*time.Second); defer cancel(); if _,err:=s.conn.ExecContext(ctx,"REVOKE SET ON PARAMETER "+d.name+" FROM vacuum_context_login"); err!=nil { t.Error(err) } }()
     }
     for _,prepared:=range []bool{false,true} {
      for _,command:=range []string{"SET "+d.name+"="+physicalLiteral(d.raw),"SET SESSION "+d.name+"="+physicalLiteral(d.raw),"SET "+d.name+"="+physicalLiteral(changed),"SET SESSION "+d.name+"="+physicalLiteral(changed),"SET SESSION "+d.name+"='bogus'","SET "+d.name+"='bogus'","SET "+d.name+"=DEFAULT","SET SESSION "+d.name+"=DEFAULT","RESET "+d.name} {
       observer.run(physicalError(command,lockContext(d.name,d.context),prepared,false))
       observer.vacuumValue(d.name,d.show,d.raw,"default",prepared)
      }
      for _,input:=range []string{d.raw,changed,"bogus"} {
       observer.run(fixture.Exec("BEGIN",0))
       observer.run(physicalError("SET LOCAL "+d.name+"="+physicalLiteral(input),lockContext(d.name,d.context),prepared,false))
       observer.run(fixture.Exec("ROLLBACK",0))
      }
      observer.run(fixture.Exec("BEGIN",0))
      observer.run(physicalError("SET LOCAL "+d.name+"=DEFAULT",lockContext(d.name,d.context),prepared,false))
      observer.run(fixture.Exec("ROLLBACK",0))
      for _,local:=range []bool{false,true} { for _,value:=range []any{d.raw,changed,"bogus",nil} { observer.run(physicalError("SELECT set_config($1,$2,$3)",lockContext(d.name,d.context),prepared,true,d.name,value,local)) } }
      observer.run(physicalError("SET "+d.name+" TO 1,2",physicalDiagnostic("22023","SET "+d.name+" takes only one argument"),prepared,false))
      observer.run(fixture.Exec("RESET ALL",0))
      observer.vacuumValue(d.name,d.show,d.raw,"default",prepared)
      peer.vacuumValue(d.name,d.show,d.raw,"default",prepared)
      observer.vacuumLogicalRows(false)
      peer.vacuumLogicalRows(false)
     }
    }
   })
  })
 }
}
