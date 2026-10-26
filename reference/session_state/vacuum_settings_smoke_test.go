package reference_test

import (
 "testing"
 "stuntgres/reference/fixture"
)

func TestReferenceVacuumSettingsSimulationSmoke(t *testing.T) {
 physicalCases(t,[]string{"vacuum_smoke_login"},func(s *physicalCase) {
  s.run(fixture.Exec("CREATE ROLE vacuum_smoke_login LOGIN PASSWORD 'role-core-fixture'",0))
  s.vacuumLogicalSetup("vacuum_smoke_login")
  ordinary:=s.login("vacuum_smoke_login"); peer:=s.lockPeer()
  ordinary.vacuumInventory(true,false)
  ordinary.vacuumValue("autovacuum_naptime","1min","60","default",false)
  ordinary.vacuumValue("vacuum_cost_delay","0","0","default",true)
  query:="SELECT current_setting('vacuum_cost_delay')"
  held,err:=ordinary.conn.PrepareContext(s.p.Ctx,query); if err!=nil { t.Fatal(err) }; defer held.Close()
  ordinary.vacuumAssign(vacuumMutations[0],true,false)
  fixture.RunStep(t,s.p.Ctx,fixture.HeldStatement{Executor:ordinary.conn,Statement:held,SQL:query},ordinary.recorder,clientText(query,"current_setting","1500us"))
  peer.vacuumValue("vacuum_cost_delay","0","0","default",true)
  ordinary.vacuumAssign(vacuumMutation{"vacuum_max_eager_freeze_failure_rate","0.5","0.5","0.5"},true,true)
  ordinary.run(physicalError("SET vacuum_max_eager_freeze_failure_rate='1.1'",vacuumBounds(vacuumDefinition("vacuum_max_eager_freeze_failure_rate"),"1.1"),false,false))
  ordinary.vacuumValue("vacuum_max_eager_freeze_failure_rate","0.5","0.5","session",true)
  for _,m:=range []vacuumMutation{{"vacuum_freeze_min_age","1000000000","1000000000","1000000000"},{"vacuum_failsafe_age","0","0","0"},{"vacuum_truncate","off","off","off"}} {
   ordinary.vacuumAssign(m,true,false); ordinary.vacuumLogicalRows(false); peer.vacuumLogicalRows(false)
  }
  ordinary.run(physicalError("SET vacuum_cost_delay='101ms'",vacuumBounds(vacuumDefinition("vacuum_cost_delay"),"101"),false,false))
  ordinary.vacuumValue("vacuum_cost_delay","1500us","1.5","session",false)
  ordinary.run(physicalError("SET autovacuum_naptime='bogus'",lockContext("autovacuum_naptime","sighup"),true,false))
  ordinary.run(physicalError("SET autovacuum_worker_slots=16",physicalRestart("autovacuum_worker_slots"),true,false))
  ordinary.vacuumValue("autovacuum_naptime","1min","60","default",false)
  ordinary.vacuumValue("autovacuum_worker_slots","16","16","default",true)
  ordinary.run(fixture.Exec("BEGIN",0))
  ordinary.run(fixture.Prepared(fixture.Exec("SET LOCAL vacuum_cost_delay='2ms'",0)))
  ordinary.vacuumValue("vacuum_cost_delay","2ms","2","session",true)
  ordinary.run(fixture.Exec("COMMIT",0))
  ordinary.vacuumValue("vacuum_cost_delay","1500us","1.5","session",true)
  for _,prepared:=range []bool{false,true} { ordinary.vacuumLogicalErrors(prepared) }
  ordinary.run(fixture.Exec("INSERT INTO public.vacuum_child VALUES(2,1,2)",1))
  ordinary.vacuumLogicalRows(true); peer.vacuumLogicalRows(true)
  ordinary.run(fixture.Prepared(fixture.Exec("RESET ALL",0)))
  ordinary.vacuumInventory(true,false)
  peer.vacuumInventory(false,true)
  ordinary.vacuumLogicalRows(true); peer.vacuumLogicalRows(true)
 })
}
