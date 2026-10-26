package reference_test

import (
 "reflect"
 "testing"
 "stuntgres/reference/fixture"
)

func TestReferenceVacuumSettingsLifecycle(t *testing.T) {
 for _,m:=range vacuumMutations {
  t.Run(m.name,func(t *testing.T) {
   physicalCases(t,nil,func(s *physicalCase) {
    peer:=s.lockPeer(); d:=vacuumDefinition(m.name)
    query:="SELECT current_setting("+physicalLiteral(m.name)+")"
    held,err:=s.conn.PrepareContext(s.p.Ctx,query); if err!=nil { t.Fatal(err) }; defer held.Close()
    check:=func(assigned bool) {
     show,raw,source:=d.show,d.raw,"default"; if assigned { show,raw,source=m.show,m.raw,"session" }
     s.vacuumValue(m.name,show,raw,source,true)
     fixture.RunStep(t,s.p.Ctx,fixture.HeldStatement{Executor:s.conn,Statement:held,SQL:query},s.recorder,clientText(query,"current_setting",show))
     peer.vacuumValue(d.name,d.show,d.raw,"default",false)
    }
    set:="SET "+m.name+"="+physicalLiteral(m.input)
    local:="SET LOCAL "+m.name+"="+physicalLiteral(m.input)
    for _,prepared:=range []bool{false,true} {
     for _,tc:=range []struct{commands []string; assigned bool}{
      {[]string{"BEGIN",set,"ROLLBACK"},false},
      {[]string{"BEGIN",set,"COMMIT"},true},
      {[]string{"RESET "+m.name,"BEGIN",local},true},
      {[]string{"COMMIT"},false},
      {[]string{"BEGIN",local},true},
      {[]string{"ROLLBACK"},false},
      {[]string{"BEGIN",set,"SET LOCAL "+m.name+"="+physicalLiteral(d.raw)},false},
     } {
      for _,command:=range tc.commands { step:=fixture.Exec(command,0); step.Prepared=prepared; s.run(step) }
      // SET then LOCAL default is effective default with session provenance.
      if len(tc.commands)==3 && tc.commands[2]=="SET LOCAL "+m.name+"="+physicalLiteral(d.raw) {
       s.vacuumValue(m.name,d.show,d.raw,"session",true)
       fixture.RunStep(t,s.p.Ctx,fixture.HeldStatement{Executor:s.conn,Statement:held,SQL:query},s.recorder,clientText(query,"current_setting",d.show))
      } else { check(tc.assigned) }
     }
     s.run(fixture.Exec("COMMIT",0)); check(true)
     s.run(fixture.Exec("RESET "+m.name,0)); check(false)
     for _,tc:=range []struct{commands []string; assigned bool}{
      {[]string{"BEGIN","SAVEPOINT vacuum_save",set,"ROLLBACK TO vacuum_save","RELEASE vacuum_save","COMMIT"},false},
      {[]string{"BEGIN","SAVEPOINT vacuum_save",set,"RELEASE vacuum_save","COMMIT"},true},
      {[]string{"RESET "+m.name,"BEGIN","SAVEPOINT vacuum_save",local,"RELEASE vacuum_save"},true},
      {[]string{"COMMIT"},false},
     } { for _,command:=range tc.commands { step:=fixture.Exec(command,0); step.Prepared=prepared; s.run(step) }; check(tc.assigned) }
     for _,isLocal:=range []bool{false,true} { for _,finish:=range []string{"COMMIT","ROLLBACK"} {
      s.run(fixture.Exec("BEGIN",0))
      step:=fixture.Select("SELECT set_config($1,$2,$3)",[]string{"set_config"},[]string{"TEXT"},[][]any{{m.show}},m.name,m.input,isLocal); step.Prepared=prepared; s.run(step); check(true)
      s.run(fixture.Exec(finish,0)); check(!isLocal && finish=="COMMIT")
      s.run(fixture.Exec("RESET "+m.name,0)); check(false)
     } }
     for _,reset:=range []string{"SET "+m.name+"=DEFAULT","RESET "+m.name,"RESET ALL","SELECT set_config($1,NULL,false)"} {
      s.run(fixture.Exec(set,0)); step:=fixture.Exec(reset,0)
      if reset=="SELECT set_config($1,NULL,false)" { step=fixture.Select(reset,[]string{"set_config"},[]string{"TEXT"},[][]any{{d.show}},m.name) }
      step.Prepared=prepared; s.run(step); check(false)
     }
    }
    var notices []fixture.Diagnostic
    _,conn,recorder:=fixture.NoticeReferencePool(t,s.p,s.pg,&notices); defer conn.Close()
    observer:=&physicalCase{t,s.p,conn,s.pg,recorder}
    for _,prepared:=range []bool{false,true} {
     notices=nil; step:=fixture.Exec(local,0); step.Prepared=prepared; observer.run(step)
     want:=[]fixture.Diagnostic{{Severity:"WARNING",SeverityUnlocalized:"WARNING",Code:"25P01",Message:"SET LOCAL can only be used in transaction blocks"}}
     if !reflect.DeepEqual(notices,want) { t.Fatalf("LOCAL notices %#v want %#v",notices,want) }
     observer.vacuumValue(d.name,d.show,d.raw,"default",prepared)
    }
   })
  })
 }
 t.Run("native-controls",vacuumNativeControls)
}
