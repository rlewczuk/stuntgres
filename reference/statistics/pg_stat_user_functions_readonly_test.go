package reference_test

import (
	"database/sql"
	"fmt"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func userFunctionWriteDiagnostic(op string) fixture.Diagnostic {
	verb:=map[string]string{"INSERT":"insert into","UPDATE":"update","DELETE":"delete from"}[op]
	action:=map[string]string{"INSERT":"inserting into","UPDATE":"updating","DELETE":"deleting from"}[op]
	return fixture.Diagnostic{Severity:"ERROR",SeverityUnlocalized:"ERROR",Code:"55000",Message:fmt.Sprintf("cannot %s view %q",verb,"pg_stat_user_functions"),Detail:"Views that do not select from a single table or view are not automatically updatable.",Hint:"To enable "+action+" the view, provide an INSTEAD OF "+op+" trigger or an unconditional ON "+op+" DO INSTEAD rule."}
}

func TestReferencePGStatUserFunctionsReadOnly(t *testing.T) {
	fixture.StatisticsScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
		xactFunctionCreate(t,p,b,c,"uf_readonly","RETURN 7;")
		fixture.StatisticsExec(t,p,b,c,"CREATE TABLE uf_sentinel(n integer)",0);fixture.StatisticsExec(t,p,b,c,"INSERT INTO uf_sentinel VALUES(37)",1)
		w,retire:=fixture.StatisticsReportingConn(t,p,b);xactFunctionBegin(t,p,b,w);for range 2{xactFunctionCall(t,p,b,w,"uf_readonly",7)};fixture.StatisticsExec(t,p,b,w,"COMMIT",0);retire();userFunctionAwait(t,p,b,"uf_readonly",2);fixture.StatisticsExec(t,p,b,c,"BEGIN",0);before:=userFunctionGet(t,p,c,"uf_readonly")
		for _,tc:=range []struct{q,op string}{
			{"INSERT INTO pg_stat_user_functions(funcname) VALUES('x')","INSERT"},
			{"INSERT INTO pg_stat_user_functions(funcname) SELECT 'x' WHERE FALSE","INSERT"},
			{"UPDATE pg_stat_user_functions SET calls=9 WHERE funcname='uf_readonly'","UPDATE"},
			{"UPDATE pg_stat_user_functions SET calls=9 WHERE FALSE","UPDATE"},
			{"DELETE FROM pg_stat_user_functions WHERE funcname='uf_readonly'","DELETE"},
			{"DELETE FROM pg_stat_user_functions WHERE FALSE","DELETE"},
			{"TRUNCATE pg_stat_user_functions","TRUNCATE"},
		}{for _,prepared:=range []bool{false,true}{
			want:=userFunctionWriteDiagnostic(tc.op);if tc.op=="TRUNCATE"{want=xactFunctionDiagnostic("42809",`"pg_stat_user_functions" is not a table`)}
			step:=fixture.ErrorDiagnostic(tc.q,want);step.Prepared=prepared;step.ErrorStage="execute";if prepared&&tc.op!="TRUNCATE"{step.ErrorStage="prepare"}
			fixture.StatisticsExec(t,p,b,c,"SAVEPOINT reject_write",0);fixture.RunStep(t,p.Ctx,c,b.Recorder,step);fixture.StatisticsExec(t,p,b,c,"ROLLBACK TO reject_write",0);fixture.StatisticsExec(t,p,b,c,"RELEASE reject_write",0)
			if got:=userFunctionGet(t,p,c,"uf_readonly");got!=before{t.Fatal("rejected write mutated pending",got,before)}
		}}
		pool:=p.Native;if b.PG{pool=p.CopyPostgres};copyConn,err:=pool.Conn(p.Ctx);if err!=nil{t.Fatal(err)};defer copyConn.Close()
		result,err:=fixture.CopyFrom(p.Ctx,copyConn,b.PG,"COPY pg_catalog.pg_stat_user_functions(funcname) FROM STDIN",strings.NewReader("x\n"))
		want:=xactFunctionDiagnostic("42809",`cannot copy to view "pg_stat_user_functions"`);want.Hint="To enable copying to a view, provide an INSTEAD OF INSERT trigger.";fixture.AssertDiagnostic(t,err,&want)
		if result.Count!=0||result.CommandTag!=""{t.Fatal("failed COPY count/tag",result)}
		if got:=userFunctionGet(t,p,c,"uf_readonly");got!=before{t.Fatal("COPY mutated pending",got,before)}
		fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Select("SELECT n FROM uf_sentinel",[]string{"n"},[]string{"INT4"},[][]any{{int64(37)}}))
		fixture.StatisticsExec(t,p,b,c,"ROLLBACK",0)
	})
}