package reference_test

import (
 "database/sql"
 "testing"
 "stuntgres/reference/fixture"
)

func ltreeArraysFirstCases() []ltreeArrayCase {
 return ltreeArrayCases[len(ltreeArrayCases)-2:]
}

func TestReferenceLtreeArrayFirstMatching(t *testing.T) {
 ltreeScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn) {
  ltreeInstall(t,p,b,c)
  ltreeArraysFirstWitnesses(t,p,b,c)
 })
 t.Run("recreation",ltreeArraysFirstRecreation)
}

// Shared by the focused behavior test and the actual feature smoke. Installation
// is owned by the caller so smoke also retains its stored-view/control scenario.
func ltreeArraysFirstWitnesses(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn) {
 t.Helper()
 run:=func(s fixture.Step){fixture.RunStep(t,p.Ctx,c,b.Recorder,s)}
 run(fixture.Exec("CREATE TABLE la_first_paths(id integer,paths ltree[])",0))
 run(fixture.Exec("CREATE TABLE la_first_owned(paths ltree[],q lquery,x ltxtquery)",0))
 paths,q,x:=[]byte("[7:9]={Other,Top.Z,Top.A}"),[]byte("Top.*"),[]byte("Top")
 run(fixture.Exec("INSERT INTO la_first_owned VALUES ($1,$2,$3)",1,paths,q,x))
 clear(paths);clear(q);clear(x)
 run(ltreeArraySelect(b,"SELECT paths ?~ q AS first_q,paths ?@ x AS first_x FROM la_first_owned",[]string{"first_q","first_x"},[]string{"LTREE","LTREE"},[][]any{{"Top.Z","Top.Z"}}))
 run(fixture.Exec("INSERT INTO la_first_paths VALUES (1,'[7:9]={Other,Top.Z,Top.A}'),(2,'{Other}'),(3,'{}'),(4,NULL)",4))
 for _,tc:=range ltreeArraysFirstCases() {
  query:=tc.right
  for _,direct:=range []bool{false,true} {
   for _,witness:=range []struct{paths,q string;want any}{
    {"[7:9]={Other,Top.Z,Top.A}",query,"Top.Z"},
    {"[7:9]={Other,Top.A,Top.Z}",query,"Top.A"},
    {"[-3:-1]={Other,Top.Z,Top.A}",query,"Top.Z"},
    {"{Top.Z,Top.Z,Top.A}",query,"Top.Z"},
    {"{Other}",query,nil},{"{}",query,nil},
   } {
    querySQL:="SELECT "+tc.expression(ltreeArrayLiteral(witness.paths,tc.leftType),ltreeArrayLiteral(witness.q,tc.rightType),direct)+" AS v"
    step:=ltreeArraySelect(b,querySQL,[]string{"v"},[]string{"LTREE"},[][]any{{witness.want}})
    step.CheckMetadata=func(t *testing.T,rows *sql.Rows){ltreeArrayMetadata(t,b,rows,[]string{"LTREE"},nil)}
    run(step);run(fixture.Prepared(step))
   }
   emptyQuery:="*";if tc.op=="?@"{emptyQuery="!foo"}
   querySQL:="SELECT "+tc.expression(ltreeArrayLiteral(`{"",a}`,tc.leftType),ltreeArrayLiteral(emptyQuery,tc.rightType),direct)+" AS v"
   step:=ltreeArraySelect(b,querySQL,[]string{"v"},[]string{"LTREE"},[][]any{{""}})
   step.CheckScan=func(t *testing.T,rows *sql.Rows,_ int){var value sql.NullString;if err:=rows.Scan(&value);err!=nil || !value.Valid || value.String!=""{t.Fatal("empty path is not SQL NULL",value,err)}}
   run(step);run(fixture.Prepared(step))
   querySQL="SELECT id,"+tc.expression("paths",ltreeArrayLiteral(query,tc.rightType),direct)+" AS v FROM la_first_paths ORDER BY id"
   table:=ltreeArraySelect(b,querySQL,[]string{"id","v"},[]string{"INT4","LTREE"},[][]any{{int64(1),"Top.Z"},{int64(2),nil},{int64(3),nil},{int64(4),nil}})
   run(table);run(fixture.Prepared(table))
   tableStatement,err:=c.PrepareContext(p.Ctx,querySQL);if err!=nil{t.Fatal(err)}
   fixture.RunStep(t,p.Ctx,fixture.HeldStatement{Executor:c,Statement:tableStatement,SQL:querySQL},b.Recorder,table)
   if err:=tableStatement.Close();err!=nil{t.Fatal(err)}
   // Literal unknowns use the selected operator/routine's ordinary coercion.
   run(ltreeArraySelect(b,"SELECT "+tc.expression("'{Top.Z,Top.A}'::ltree[]","'"+query+"'",direct)+" AS v",[]string{"v"},[]string{"LTREE"},[][]any{{"Top.Z"}}))
   querySQL="SELECT "+tc.expression("$1::ltree[]","$2::"+tc.rightType,direct)+" AS v"
   statement,err:=c.PrepareContext(p.Ctx,querySQL);if err!=nil{t.Fatal(err)}
   held:=fixture.HeldStatement{Executor:c,Statement:statement,SQL:querySQL}
   for _,args:=range [][]any{{[]byte(tc.left),[]byte(tc.right)},{nil,tc.right},{tc.left,nil},{"{{a,NULL}}",nil},{"{a,NULL}",nil}} {
    var want any="Top.Z";if args[0]==nil || args[1]==nil{want=nil}
    s:=ltreeArraySelect(b,querySQL,[]string{"v"},[]string{"LTREE"},[][]any{{want}},args...)
    s.CheckMetadata=func(t *testing.T,rows *sql.Rows){ltreeArrayMetadata(t,b,rows,[]string{"LTREE"},nil)}
    run(s);run(fixture.Prepared(s));fixture.RunStep(t,p.Ctx,held,b.Recorder,s)
   }
   bad:=fixture.ErrorDiagnostic(querySQL,fixture.ControlsReferenceDiagnostic("22004","array must not contain nulls"),"{a,NULL}","a")
   bad.SelectRows,bad.ErrorStage=true,"execute"
   fixture.RunStep(t,p.Ctx,held,b.Recorder,bad)
   fixture.RunStep(t,p.Ctx,held,b.Recorder,ltreeArraySelect(b,querySQL,[]string{"v"},[]string{"LTREE"},[][]any{{"Top.Z"}},tc.left,tc.right))
   if err:=statement.Close();err!=nil{t.Fatal(err)}
   // Empty result sets still expose the selected LTREE result family.
   run(ltreeArraySelect(b,"SELECT "+tc.expression("paths",ltreeArrayLiteral(query,tc.rightType),direct)+" AS v FROM la_first_paths WHERE false",[]string{"v"},[]string{"LTREE"},[][]any{}))
  }
 }
 for _,witness:=range []struct{expression string;want any}{
  {"'{Top,Europe,Europe.Top}'::ltree[] ?@ 'Top & Europe'::ltxtquery","Europe.Top"},
  {"'{Top,Europe}'::ltree[] ?@ 'Top & Europe'::ltxtquery",nil},
  {"'{Europe.Top,Top}'::ltree[] ?~ 'Top'::lquery","Top"},
  {"'{Europe.Top,Top}'::ltree[] ?@ 'Top'::ltxtquery","Europe.Top"},
  {`'{"",a}'::ltree[] ?~ '*{0}'::lquery`,""},
 } {run(ltreeArraySelect(b,"SELECT "+witness.expression+" AS v",[]string{"v"},[]string{"LTREE"},[][]any{{witness.want}}))}
}

func ltreeArraysFirstRecreation(t *testing.T) {
 for _,tc:=range ltreeArraysFirstCases(){t.Run(tc.name,func(t *testing.T){
  ltreeScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
   ltreeInstall(t,p,b,c)
   parameterExpr:=tc.expression("$1::text::ltree[]","$2::text::"+tc.rightType,false)
   literalExpr:=tc.expression(ltreeArrayLiteral(tc.left,tc.leftType),ltreeArrayLiteral(tc.right,tc.rightType),false)
   type retained struct{query string;statement *sql.Stmt;args []any;family string}
   var statements []retained
   for _,input:=range []struct{expression string;args []any}{{parameterExpr,[]any{tc.left,tc.right}},{literalExpr,nil}}{
    for _,family:=range []string{"LTREE","TEXT"}{
     expression:=input.expression;if family=="TEXT"{expression="("+expression+")::text"}
     query:="SELECT "+expression+" AS v"
     statement,err:=c.PrepareContext(p.Ctx,query);if err!=nil{t.Fatal(err)};defer statement.Close()
     held:=fixture.HeldStatement{Executor:c,Statement:statement,SQL:query}
     fixture.RunStep(t,p.Ctx,held,b.Recorder,ltreeArraySelect(b,query,[]string{"v"},[]string{family},[][]any{{"Top.Z"}},input.args...))
     statements=append(statements,retained{query,statement,input.args,family})
    }
   }
   for _,command:=range []string{"DROP EXTENSION ltree","CREATE EXTENSION ltree"}{fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Exec(command,0))}
   for _,saved:=range statements{
    held:=fixture.HeldStatement{Executor:c,Statement:saved.statement,SQL:saved.query}
    success:=ltreeArraySelect(b,saved.query,[]string{"v"},[]string{saved.family},[][]any{{"Top.Z"}},saved.args...)
    if saved.family=="LTREE"{
     // Recreated extension results have a new nominal identity even though the
     // source text and result spelling are unchanged.
     failure:=fixture.ErrorDiagnostic(saved.query,fixture.ControlsReferenceDiagnostic("0A000","cached plan must not change result type"),saved.args...)
     failure.SelectRows,failure.ErrorStage=true,"execute"
     fixture.RunStep(t,p.Ctx,held,b.Recorder,failure)
     fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Prepared(success))
    }else{
     // Stable text output keeps the held result contract while its installed
     // operands and selected extractor rebind to the current installation.
     fixture.RunStep(t,p.Ctx,held,b.Recorder,success)
    }
   }
  })
 })}
}
