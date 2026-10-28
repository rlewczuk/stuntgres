package reference_test

import (
 "database/sql"
 "strings"
 "testing"
 "stuntgres/reference/fixture"
)

func ltreeLtxtqueryForms(path,query string)[]string{return []string{path+" @ "+query,query+" @ "+path,"ltxtq_exec("+path+","+query+")","ltxtq_rexec("+query+","+path+")"}}
func ltreeLtxtqueryMismatch(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
 t.Helper()
 for _,tc:=range []struct{q,message string}{
  {"/*é*/ SELECT 'a'::ltree @ 'a'::text","operator does not exist: ltree @ text"},
  {"/*é*/ SELECT 'a'::text @ 'a'::ltree","operator does not exist: text @ ltree"},
 }{
  want:=fixture.ControlsReferenceDiagnostic("42883",tc.message);want.Position=ltreeSQLPosition(tc.q,"@");want.Hint="No operator matches the given name and argument types. You might need to add explicit type casts."
  for _,prepared:=range []bool{false,true}{ltreeError(t,p,b,c,tc.q,prepared,true,true,want)}
 }
}
func TestReferenceLtreeLtxtqueryDiagnostics(t *testing.T){
 for _,tc:=range []struct{name,input,code,message,detail string;path bool}{
  {"empty","","42601","operand syntax error","",false},{"not","!","42601","operand syntax error","",false},{"and","a&","42601","operand syntax error","",false},{"star","*","42601","operand syntax error","",false},{"query_unicode","é","42601","operand syntax error","",false},
  {"dot","a.b","42601","syntax error","",false},{"paren","(a","42601","syntax error","",false},{"tab","a\t","42601","syntax error","",false},
  {"at_modifier","foo@bar","42601","modifiers syntax error","",false},{"star_modifier","foo*bar","42601","modifiers syntax error","",false},{"percent_modifier","foo%bar","42601","modifiers syntax error","",false},
  {"word256",strings.Repeat("a",256),"22023","operand is too long","",false},{"not33",strings.Repeat("!",33)+"a","XX000","stack too short","",false},
  {"path_syntax","a..b","42601","ltree syntax error at character 3","",true},{"path_trailing","a.","42601","ltree syntax error","Unexpected end of input.",true},{"path_unicode","é","42601","ltree syntax error at character 1","",true},
  {"path_long",strings.Repeat("a",1001),"42622","label string is too long","Label length is 1001, must be at most 1000, at character 1002.",true},
  {"path_deep",strings.Repeat("a.",65535)+"a","54000","number of ltree labels (65536) exceeds the maximum allowed (65535)","",true},
 }{t.Run(tc.name,func(t *testing.T){
  ltreeScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
   ltreeInstall(t,p,b,c)
   lit:=queryIntLiteral(tc.input)
   for _,nullOpposite:=range []bool{false,true}{
    path,query:="'a'::ltree",lit+"::ltxtquery"
    if nullOpposite{path="NULL::ltree"}
    if tc.path{path,query=lit+"::ltree","'a'::ltxtquery";if nullOpposite{query="NULL::ltxtquery"}}
    for _,form:=range ltreeLtxtqueryForms(path,query){
     q:="/*é*/ SELECT "+form+" AS v";want:=fixture.ControlsReferenceDiagnostic(tc.code,tc.message);want.Detail=tc.detail;want.Position=ltreeSQLPosition(q,lit)
     for _,prepared:=range []bool{false,true}{ltreeError(t,p,b,c,q,prepared,true,true,want)}
    }
   }
   for _,form:=range ltreeLtxtqueryForms("$1::ltree","$2::ltxtquery"){
    q:="SELECT "+form+" AS v";statement,err:=c.PrepareContext(p.Ctx,q);if err!=nil{t.Fatal(err)}
    held:=fixture.HeldStatement{Executor:c,Statement:statement,SQL:q}
    for _,bad:=range []any{tc.input,[]byte(tc.input)}{for _,nullOpposite:=range []bool{false,true}{
     args:=[]any{"a",bad};parameter:=2;if nullOpposite{args[0]=nil};if tc.path{args=[]any{bad,"a"};parameter=1;if nullOpposite{args[1]=nil}}
     want:=queryIntParameterDiagnostic(tc.code,tc.message,tc.input,parameter);want.Detail=tc.detail
     s:=fixture.ErrorDiagnostic(q,want,args...);s.SelectRows=true;s.ErrorStage="execute";ltreeLtxtqueryRun(t,p,b,held,s)
     ltreeLtxtqueryRun(t,p,b,held,ltreeArrayResult(b,q,true,"a","a"))
    }}
    statement.Close()
   }
   if tc.name=="at_modifier"{ltreeLtxtqueryMismatch(t,p,b,c)}
  })
 })}
 t.Run("atomic_mutations",ltreeLtxtqueryAtomic)
}
func ltreeLtxtqueryAtomic(t *testing.T){
 ltreeScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
  ltreeInstall(t,p,b,c);run:=func(s fixture.Step){ltreeLtxtqueryRun(t,p,b,c,s)}
  run(fixture.Exec("CREATE TABLE lxq_atomic(id integer,v boolean)",0))
  for _,tc:=range []struct{path bool;bad,code,message string}{{false,"foo@bar","42601","modifiers syntax error"},{true,"a..b","42601","ltree syntax error at character 3"}}{
   expression:="'a'::ltree @ $1::ltxtquery";later:="'a'::ltree @ $2::ltxtquery"
   if tc.path{expression="$1::ltree @ 'a'::ltxtquery";later="$2::ltree @ 'a'::ltxtquery"}
   q:="INSERT INTO lxq_atomic VALUES (1,"+expression+"),(2,"+later+")";statement,err:=c.PrepareContext(p.Ctx,q);if err!=nil{t.Fatal(err)}
   held:=fixture.HeldStatement{Executor:c,Statement:statement,SQL:q};want:=queryIntParameterDiagnostic(tc.code,tc.message,tc.bad,2)
   fail:=fixture.ErrorDiagnostic(q,want,"a",tc.bad);fail.ErrorStage="execute";ltreeLtxtqueryRun(t,p,b,held,fail)
   run(fixture.Select("SELECT count(*) AS n FROM lxq_atomic",[]string{"n"},[]string{"INT8"},[][]any{{int64(0)}}))
   ltreeLtxtqueryRun(t,p,b,held,fixture.Exec(q,2,"a","a"));statement.Close()
   run(fixture.Select("SELECT id,v FROM lxq_atomic ORDER BY id",[]string{"id","v"},[]string{"INT4","BOOL"},[][]any{{int64(1),true},{int64(2),true}}))
   q="UPDATE lxq_atomic SET v=CASE WHEN id=1 THEN "+expression+" ELSE "+later+" END"
   statement,err=c.PrepareContext(p.Ctx,q);if err!=nil{t.Fatal(err)};held=fixture.HeldStatement{Executor:c,Statement:statement,SQL:q}
   fail=fixture.ErrorDiagnostic(q,want,"z",tc.bad);fail.ErrorStage="execute";ltreeLtxtqueryRun(t,p,b,held,fail)
   run(fixture.Select("SELECT id,v FROM lxq_atomic ORDER BY id",[]string{"id","v"},[]string{"INT4","BOOL"},[][]any{{int64(1),true},{int64(2),true}}))
   ltreeLtxtqueryRun(t,p,b,held,fixture.Exec(q,2,"z","z"));statement.Close()
   run(fixture.Select("SELECT id,v FROM lxq_atomic ORDER BY id",[]string{"id","v"},[]string{"INT4","BOOL"},[][]any{{int64(1),false},{int64(2),false}}))
   run(fixture.Exec("DELETE FROM lxq_atomic",2))
  }
 })
}
