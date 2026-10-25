package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceAccessExecutionLiveAuthority(t *testing.T) {
	objectGrantScenario(t,nil,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn,run func(fixture.Step),prepared bool){
		defer accessExecutionResetConnection(t,c)
		objects:=accessExecutionSetup(t,p,b,c,run)
		peer,err:=b.DB.Conn(p.Ctx);if err!=nil {t.Fatal(err)};defer peer.Close()
		for _,o:=range objects {
			call:=accessExecutionCalls(t,p,c,o)[0]
			held:=accessExecutionHeld(t,p,c,call.query); other:=accessExecutionHeld(t,p,peer,call.query)
			check:=func(h fixture.HeldStatement,value bool){fixture.RunStep(t,p.Ctx,h,b.Recorder,objectGrantAccessValue(call.query,value,call.args...))}
			run(fixture.Exec("REVOKE "+o.privilege+" ON "+o.target+" FROM PUBLIC",0));check(held,false);check(other,false)
			run(fixture.Exec("BEGIN",0));run(fixture.Exec("SAVEPOINT ae_before",0))
			run(fixture.Exec("GRANT "+o.privilege+" ON "+o.target+" TO og_object_reader",0));check(held,true);check(other,false)
			run(fixture.Exec("ROLLBACK TO ae_before",0));check(held,false);check(other,false)
			run(fixture.Exec("GRANT "+o.privilege+" ON "+o.target+" TO og_object_reader",0));run(fixture.Exec("COMMIT",0));check(other,true)
			run(fixture.Exec("BEGIN",0));run(fixture.Exec("REVOKE "+o.privilege+" ON "+o.target+" FROM og_object_reader",0));check(held,false);check(other,true);run(fixture.Exec("ROLLBACK",0));check(held,true)
			implicit:=accessExecutionCalls(t,p,c,o)[4]
			current:=accessExecutionHeld(t,p,c,implicit.query)
			run(fixture.Exec("SET ROLE og_object_reader",0));fixture.RunStep(t,p.Ctx,current,b.Recorder,objectGrantAccessValue(current.SQL,true,implicit.args...));run(fixture.Exec("RESET ROLE",0))
			run(fixture.Exec("REVOKE "+o.privilege+" ON "+o.target+" FROM og_object_reader",0));check(held,false)
			run(fixture.Exec("SET ROLE og_object_reader",0));fixture.RunStep(t,p.Ctx,current,b.Recorder,objectGrantAccessValue(current.SQL,false,implicit.args...));run(fixture.Exec("RESET ROLE",0));fixture.RunStep(t,p.Ctx,current,b.Recorder,objectGrantAccessValue(current.SQL,true,implicit.args...))
			run(fixture.Exec("GRANT "+o.privilege+" ON "+o.target+" TO og_object_other",0));run(fixture.Exec("GRANT og_object_other TO og_object_reader WITH INHERIT TRUE",0));check(held,true)
			run(fixture.Exec("GRANT og_object_other TO og_object_reader WITH INHERIT FALSE",0));check(held,false);run(fixture.Exec("REVOKE og_object_other FROM og_object_reader",0))
		}
	})
}

func TestReferenceAccessExecutionInquiryAuthority(t *testing.T) {
	objectGrantScenario(t,nil,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn,run func(fixture.Step),prepared bool){
		defer accessExecutionResetConnection(t,c)
		objects:=accessExecutionSetup(t,p,b,c,run)
		run(fixture.Exec("CREATE TABLE ae_demand(an name,ao oid,ot text,oo oid,pr text)",0))
		run(fixture.Exec("INSERT INTO ae_demand VALUES(NULL,NULL,NULL,NULL,NULL)",1))
		run(fixture.Exec("GRANT SELECT ON ae_demand TO og_object_reader",0))
		for _,o:=range objects {
			calls:=accessExecutionCalls(t,p,c,o)
			for i,call:=range calls {
				func(){
					defer objectGrantRoutineCleanup(t,p,c,[]string{call.signature})
					held:=accessExecutionHeld(t,p,c,call.query)
					var demand []fixture.HeldStatement
					for _,suffix:=range []string{""," WHERE FALSE"," WHERE oo=0::oid"} {
						query:=accessExecutionColumnCall(call)+" FROM ae_demand"+suffix
						demand=append(demand,accessExecutionHeld(t,p,c,query))
					}
					phase:=func(deny bool){
						fixture.RoleGrantActor(run,"og_object_reader",func(){
							s:=objectGrantAccessValue(call.query,o.initial,call.args...)
							if deny {s=objectGrantAccessDenied(call.query,o.function,true,call.args...)}
							run(s);accessExecutionPairedHeld(t,p,b,prepared,held,s)
							if deny {exec:=s;exec.SelectRows=false;run(exec)}
							// Preparation itself does not require selected EXECUTE.
							if prepared {fresh:=accessExecutionHeld(t,p,c,call.query);fixture.RunStep(t,p.Ctx,fresh,b.Recorder,s)}
							sibling:=calls[i^1];run(objectGrantAccessValue(sibling.query,o.initial,sibling.args...))
							// Literal NULL is the established strict simplification boundary.
							run(objectGrantAccessValue("SELECT "+o.function+"(NULL::text,'bad') AS v",nil))
							for j,h:=range demand {
								rows:=[][]any{{nil}};if j!=0 {rows=nil}
								step:=fixture.Select(h.SQL,[]string{"v"},[]string{"BOOL"},rows)
								if deny {step=objectGrantAccessDenied(h.SQL,o.function,true)}
								run(step);accessExecutionPairedHeld(t,p,b,prepared,h,step)
							}
							dead:="SELECT CASE WHEN FALSE THEN "+accessExecutionColumnCall(call)[7:len(accessExecutionColumnCall(call))-5]+" ELSE FALSE END AS v FROM ae_demand"
							run(objectGrantAccessValue(dead,false))
						})
					}
					phase(false)
					run(fixture.Exec("REVOKE EXECUTE ON FUNCTION "+call.signature+" FROM PUBLIC",0));phase(true)
					run(fixture.Exec("GRANT EXECUTE ON FUNCTION "+call.signature+" TO og_object_reader",0));phase(false)
					run(fixture.Exec("REVOKE EXECUTE ON FUNCTION "+call.signature+" FROM og_object_reader",0));phase(true)
				}()
			}
		}
	})
}
