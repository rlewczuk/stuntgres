package reference_test

import (
 "database/sql"
 "testing"
 "stuntgres/reference/fixture"
)

func TestReferenceLtreeArrayFirstDiagnostics(t *testing.T) {
 for _,tc:=range ltreeArraysFirstCases(){t.Run(tc.name,func(t *testing.T){
  ltreeScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
   ltreeInstall(t,p,b,c)
   for _,direct:=range []bool{false,true}{
    for _,invalid:=range []struct{paths,code,message string}{
     {"{{a,NULL}}","2202E","array must be one-dimensional"},
     {"{{a,b}}","2202E","array must be one-dimensional"},
     {"{a,NULL}","22004","array must not contain nulls"},
     {"{NULL,a}","22004","array must not contain nulls"},
    } {
     want:=fixture.ControlsReferenceDiagnostic(invalid.code,invalid.message)
     for _,parameter:=range []bool{false,true}{
      left:=ltreeArrayLiteral(invalid.paths,"ltree[]");var args []any
      if parameter{left="$1::ltree[]";args=[]any{invalid.paths}}
      query:="SELECT "+tc.expression(left,ltreeArrayLiteral("a",tc.rightType),direct)+" AS v"
      for _,prepared:=range []bool{false,true}{ltreeError(t,p,b,c,query,prepared,true,false,want,args...)}
     }
    }
    // Input diagnostics retain codec producer locations rather than receiving
    // operator-execution cursors. This malformed path fails before invocation.
    literal:=ltreeArrayLiteral("{a..b}","ltree[]")
    query:="SELECT "+tc.expression(literal,ltreeArrayLiteral("a",tc.rightType),direct)+" AS v"
    want:=fixture.ControlsReferenceDiagnostic("42601","ltree syntax error at character 3");want.Position=ltreeSQLPosition(query,literal)
    for _,prepared:=range []bool{false,true}{ltreeError(t,p,b,c,query,prepared,true,true,want)}
    bound:=fixture.ControlsReferenceDiagnostic("42601","ltree syntax error at character 3");bound.Context="unnamed portal parameter $1 = '...'"
    query="SELECT "+tc.expression("$1::ltree[]",ltreeArrayLiteral("a",tc.rightType),direct)+" AS v"
    for _,prepared:=range []bool{false,true}{ltreeError(t,p,b,c,query,prepared,true,false,bound,"{a..b}")}
    badQuery,message:="a..b","lquery syntax error at character 3";if tc.op=="?@"{badQuery,message="a&","operand syntax error"}
    literal=ltreeArrayLiteral(badQuery,tc.rightType)
    query="SELECT "+tc.expression("'{a}'::ltree[]",literal,direct)+" AS v"
    want=fixture.ControlsReferenceDiagnostic("42601",message);want.Position=ltreeSQLPosition(query,literal)
    for _,prepared:=range []bool{false,true}{ltreeError(t,p,b,c,query,prepared,true,true,want)}
    query="SELECT "+tc.expression("'{a}'::ltree[]","$1::"+tc.rightType,direct)+" AS v"
    want.Position=fixture.Position(0);want.Context="unnamed portal parameter $1 = '...'"
    for _,prepared:=range []bool{false,true}{ltreeError(t,p,b,c,query,prepared,true,false,want,badQuery)}
   }
   run:=func(s fixture.Step){fixture.RunStep(t,p.Ctx,c,b.Recorder,s)}
   run(fixture.Exec("CREATE TABLE la_first_bad(id integer,paths ltree[],result ltree)",0))
   run(fixture.Exec("CREATE TABLE la_first_dest(id integer,result ltree)",0))
   run(fixture.Exec("INSERT INTO la_first_bad VALUES (1,'{a}','original_one'),(2,'{a,NULL}','original_two')",2))
   run(fixture.Exec("INSERT INTO la_first_dest VALUES (99,'sentinel')",1))
   query:="INSERT INTO la_first_dest SELECT id,"+tc.expression("paths",ltreeArrayLiteral("a",tc.rightType),false)+" FROM la_first_bad ORDER BY id"
   update:="UPDATE la_first_bad SET result="+tc.expression("paths",ltreeArrayLiteral("a",tc.rightType),false)
   for _,sql:=range []string{query,update}{
    statement,err:=c.PrepareContext(p.Ctx,sql);if err!=nil{t.Fatal(err)}
    held:=fixture.HeldStatement{Executor:c,Statement:statement,SQL:sql}
    bad:=fixture.ErrorDiagnostic(sql,fixture.ControlsReferenceDiagnostic("22004","array must not contain nulls"))
    run(bad);run(fixture.Prepared(bad));fixture.RunStep(t,p.Ctx,held,b.Recorder,bad)
    run(ltreeArraySelect(b,"SELECT id,result FROM la_first_dest ORDER BY id",[]string{"id","result"},[]string{"INT4","LTREE"},[][]any{{int64(99),"sentinel"}}))
    run(ltreeArraySelect(b,"SELECT id,result FROM la_first_bad ORDER BY id",[]string{"id","result"},[]string{"INT4","LTREE"},[][]any{{int64(1),"original_one"},{int64(2),"original_two"}}))
    run(fixture.Exec("UPDATE la_first_bad SET paths='{a}'::ltree[] WHERE id=2",1))
    fixture.RunStep(t,p.Ctx,held,b.Recorder,fixture.Exec(sql,2))
    if err:=statement.Close();err!=nil{t.Fatal(err)}
    if sql==query {
     run(ltreeArraySelect(b,"SELECT id,result FROM la_first_dest ORDER BY id",[]string{"id","result"},[]string{"INT4","LTREE"},[][]any{{int64(1),"a"},{int64(2),"a"},{int64(99),"sentinel"}}))
     run(fixture.Exec("DELETE FROM la_first_dest WHERE id<>99",2))
     run(fixture.Exec("UPDATE la_first_bad SET paths='{a,NULL}'::ltree[] WHERE id=2",1))
    }else{run(ltreeArraySelect(b,"SELECT id,result FROM la_first_bad ORDER BY id",[]string{"id","result"},[]string{"INT4","LTREE"},[][]any{{int64(1),"a"},{int64(2),"a"}}))}
   }
  })
 })}
 t.Run("signatures",func(t *testing.T){
  ltreeScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
   ltreeInstall(t,p,b,c)
   for _,tc:=range []struct{left,op,right,signature string}{
    {"'a'::lquery","?~","'{}'::ltree[]","lquery ?~ ltree[]"},
    {"'a'::ltxtquery","?@","'{}'::ltree[]","ltxtquery ?@ ltree[]"},
    {"'{}'::ltree[]","?~","'a'::ltxtquery","ltree[] ?~ ltxtquery"},
    {"'{}'::ltree[]","?@","'a'::lquery","ltree[] ?@ lquery"},
    {"'a'::ltree","?~","'a'::lquery","ltree ?~ lquery"},
    {"'a'::ltree","?@","'a'::ltxtquery","ltree ?@ ltxtquery"},
   }{
    query:="SELECT /* é */ "+tc.left+" "+tc.op+" "+tc.right
    want:=fixture.ControlsReferenceDiagnostic("42883","operator does not exist: "+tc.signature)
    want.Position=ltreeSQLPosition(query,tc.op+" ")
    want.Hint="No operator matches the given name and argument types. You might need to add explicit type casts."
    for _,prepared:=range []bool{false,true}{ltreeError(t,p,b,c,query,prepared,true,true,want)}
   }
  })
 })
}
