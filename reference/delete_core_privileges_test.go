package reference_test

import (
	"context"
	"testing"
)

func TestReferenceDeleteCorePrivileges(t *testing.T) {
	const role="delete_privileges_actor"
	original:=[][]any{{int64(1),int64(10)},{int64(2),int64(20)},{int64(2),int64(21)},{int64(3),int64(30)}}
	survivors:=[][]any{{int64(1),int64(10)},{int64(3),int64(30)}}
	for _, tc:=range []struct{name,query,revoke string; column,denied,held bool; count int64; remaining [][]any}{
		{"no_where","DELETE FROM t","",false,false,false,4,nil},
		{"constant_true","DELETE FROM t WHERE TRUE","",false,false,false,4,nil},
		{"constant_false","DELETE FROM t WHERE FALSE","",false,false,false,0,original},
		{"predicate_denied","DELETE FROM t WHERE id=2","",false,true,false,0,original},
		{"column_select","DELETE FROM t AS d WHERE d.id=2","",true,false,false,2,survivors},
		{"revoke_delete","DELETE FROM t AS d WHERE d.id=$1","REVOKE DELETE ON TABLE t FROM "+role,true,false,true,2,survivors},
		{"revoke_column","DELETE FROM t AS d WHERE d.id=$1","REVOKE SELECT(id) ON TABLE t FROM "+role,true,false,true,2,survivors},
	} {
		for _, prepared:=range []bool{false,true} {
			t.Run(tc.name+map[bool]string{false:"/direct",true:"/prepared"}[prepared],func(t *testing.T){
				roleReferenceCleanup(t,[]string{role}); p:=newReferenceScenario(t)
				for _, b:=range p.backends(){
					admin,actor:=referenceLockConnections(t,p.ctx,b.db)
					run:=func(s referenceStep){referenceRunStep(t,p.ctx,admin,b.recorder,s)}
					if b.pg {run(referenceExec("GRANT USAGE ON SCHEMA public TO PUBLIC",0))}
					for _, q:=range []string{"CREATE USER "+role,"CREATE TABLE t(id INT,v INT)","GRANT DELETE ON TABLE t TO "+role}{run(referenceExec(q,0))}
					run(referenceExec("INSERT INTO t VALUES(1,10),(2,20),(2,21),(3,30)",4))
					if tc.column {run(referenceExec("GRANT SELECT(id) ON TABLE t TO "+role,0))}
					referenceRunStep(t,p.ctx,actor,b.recorder,referenceExec("SET SESSION AUTHORIZATION "+role,0))
					t.Cleanup(func(){referenceRunStep(t,p.ctx,actor,b.recorder,referenceExec("RESET SESSION AUTHORIZATION",0))})
					read:=func(want [][]any){run(referenceSelect("SELECT id,v FROM t ORDER BY v",[]string{"id","v"},[]string{"INT4","INT4"},want))}
					read(original)
					if tc.held {
						ctx:=context.WithValue(p.ctx,referenceRecorderKey{},b.recorder)
						stmt,err:=actor.PrepareContext(ctx,tc.query); if err!=nil {t.Fatal("authorized Prepare",err)}; defer stmt.Close()
						result,err:=stmt.ExecContext(ctx,2); if err!=nil {t.Fatal(err)}; referenceLockAffected(t,result,2); read(survivors)
						run(referenceExec(tc.revoke,0)); result,err=stmt.ExecContext(ctx,1); if result!=nil {t.Fatal("revoked Execute exposed result")}
						referenceAssertDiagnostic(t,err,&referenceDiagnostic{Severity:"ERROR",SeverityUnlocalized:"ERROR",Code:"42501",Message:"permission denied for table t"}); read(survivors)
					} else {
						s:=referenceExec(tc.query,tc.count); if tc.denied {s=roleGrantDenied(tc.query,"t");s.errorStage="execute"};s.prepared=prepared
						referenceRunStep(t,p.ctx,actor,b.recorder,s); read(tc.remaining)
						if tc.column {s=roleGrantDenied("DELETE FROM t WHERE v=10","t");s.prepared=prepared;s.errorStage="execute";referenceRunStep(t,p.ctx,actor,b.recorder,s);read(survivors)}
					}
				}
			})
		}
	}
}
