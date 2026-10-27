package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePlannerCostsLifecycle(t *testing.T) {
	for _,d:=range plannerCostSettings {
		t.Run(d.name,func(t *testing.T) {
			physicalCases(t,nil,func(s *physicalCase) {
				peer:=s.lockPeer()
				query:="SELECT current_setting("+physicalLiteral(d.name)+")"
				held,err:=s.conn.PrepareContext(s.p.Ctx,query); if err!=nil { s.t.Fatal(err) }; defer held.Close()
				check:=func(raw string) {
					show,source:=raw,"session"
					if raw==d.boot { show,source=d.show,"default" } else if d.kind=="integer" { if raw=="2" { show="16kB" } else { show="24kB" } }
					s.plannerCostValue(d,show,raw,source,true)
					fixture.RunStep(s.t,s.p.Ctx,fixture.HeldStatement{Executor:s.conn,Statement:held,SQL:query},s.recorder,clientText(query,"current_setting",show))
					peer.plannerCostValue(d,d.show,d.boot,"default",false)
				}
				for _,prepared:=range []bool{false,true} {
					command:=func(q string) { step:=fixture.Exec(q,0); step.Prepared=prepared; s.run(step) }
					set:="SET "+d.name+"='2'"; local:="SET LOCAL "+d.name+"='3'"
					for _,end:=range []string{"COMMIT","ROLLBACK"} {
						command("BEGIN"); command(set); check("2"); command(end)
						if end=="COMMIT" { check("2") } else { check(d.boot) }
						command("RESET "+d.name); check(d.boot)
						command("BEGIN"); command(local); check("3"); command(end); check(d.boot)
					}
					command("BEGIN"); command(set); check("2"); command(local); check("3"); command("COMMIT"); check("2")
					command("RESET "+d.name)
					command("BEGIN"); command(set); command(local); check("3"); command("ROLLBACK"); check(d.boot)
					command("BEGIN"); command(set); command("SAVEPOINT planner_outer"); command(local); check("3")
					command("SAVEPOINT planner_inner"); command("SET "+d.name+"='2'"); check("2")
					command("ROLLBACK TO planner_inner"); check("3"); command("RELEASE planner_inner")
					command("ROLLBACK TO planner_outer"); check("2"); command("RELEASE planner_outer"); command("ROLLBACK"); check(d.boot)
					command("BEGIN"); command("SAVEPOINT planner_release"); command(local); command("RELEASE planner_release"); check("3"); command("COMMIT"); check(d.boot)
					for _,localFlag:=range []bool{false,true} {
						command("BEGIN")
						show:="2"; if d.kind=="integer" { show="16kB" }
						step:=fixture.Select("SELECT set_config($1,$2,$3)",[]string{"set_config"},[]string{"TEXT"},[][]any{{show}},d.name,"2",localFlag); step.Prepared=prepared; s.run(step); check("2")
						command("COMMIT"); if localFlag { check(d.boot) } else { check("2") }; command("RESET "+d.name)
					}
					for _,reset:=range []string{"SET "+d.name+"=DEFAULT","RESET "+d.name,"RESET ALL","SELECT set_config($1,NULL,false)"} {
						command(set); check("2")
						if reset=="SELECT set_config($1,NULL,false)" { step:=fixture.Select(reset,[]string{"set_config"},[]string{"TEXT"},[][]any{{d.show}},d.name); step.Prepared=prepared; s.run(step) } else { command(reset) }
						check(d.boot)
					}
				}
			})
		})
	}
}
