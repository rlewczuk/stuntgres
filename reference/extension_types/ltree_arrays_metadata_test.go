package reference_test

import (
 "database/sql"
 "testing"
 "stuntgres/reference/fixture"
)

func TestReferenceLtreeArrayMetadata(t *testing.T) {
 ltreeScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn) {
  ltreeInstall(t,p,b,c)
  run:=func(s fixture.Step){fixture.RunStep(t,p.Ctx,c,b.Recorder,s)}
  for index,tc:=range ltreeArrayCases {
   result:="16"; if tc.family()=="LTREE" {result="'ltree'::regtype"}
   function:=ltreeArrayLiteral(tc.name+"("+tc.leftType+","+tc.rightType+")","regprocedure")+"::oid"
   operator:=ltreeArrayLiteral(tc.op+"("+tc.leftType+","+tc.rightType+")","regoperator")+"::oid"
   run(fixture.Select("SELECT proargtypes[0]='"+tc.leftType+"'::regtype AS a,proargtypes[1]='"+tc.rightType+"'::regtype AS b,prorettype="+result+" AS r,proisstrict,provolatile,proparallel,proretset,prosrc,probin FROM pg_proc WHERE oid="+function,[]string{"a","b","r","proisstrict","provolatile","proparallel","proretset","prosrc","probin"},[]string{"BOOL","BOOL","BOOL","BOOL","CHAR","CHAR","BOOL","TEXT","TEXT"},[][]any{{true,true,true,true,"i","s",false,tc.name,"$libdir/ltree"}}))
   commutator:="0"; restriction,join:="0","0"
   if index<12 {
    other:=ltreeArrayCases[index^1]
    commutator=ltreeArrayLiteral(other.op+"("+other.leftType+","+other.rightType+")","regoperator")+"::oid"
    restriction,join="'matchingsel'::regproc::oid","'matchingjoinsel'::regproc::oid"
   }
   run(fixture.Select("SELECT oprleft='"+tc.leftType+"'::regtype AS a,oprright='"+tc.rightType+"'::regtype AS b,oprresult="+result+" AS r,oprcode="+function+" AS f,oprcom="+commutator+" AS c,oprnegate=0 AS n,oprrest="+restriction+" AS s,oprjoin="+join+" AS j,oprcanhash,oprcanmerge FROM pg_operator WHERE oid="+operator,[]string{"a","b","r","f","c","n","s","j","oprcanhash","oprcanmerge"},[]string{"BOOL","BOOL","BOOL","BOOL","BOOL","BOOL","BOOL","BOOL","BOOL","BOOL"},[][]any{{true,true,true,true,true,true,true,true,false,false}}))
   for _,member:=range []struct{ class,id string }{{"1255",function},{"2617",operator}} {
    run(fixture.Select("SELECT deptype FROM pg_depend WHERE classid="+member.class+" AND objid="+member.id+" AND refclassid=3079 AND refobjid=(SELECT oid FROM pg_extension WHERE extname='ltree')",[]string{"deptype"},[]string{"CHAR"},[][]any{{"e"}}))
   }
  }
  for _,name:=range []string{"ltree","lquery"} {
   run(fixture.Select("SELECT a.typelem=t.oid AS element,a.oid=t.typarray AS reciprocal FROM pg_type t JOIN pg_type a ON a.oid=t.typarray WHERE t.oid='"+name+"'::regtype",[]string{"element","reciprocal"},[]string{"BOOL","BOOL"},[][]any{{true,true}}))
  }
  for _,predicate:=range []string{"true","false"} {
   query:="SELECT '{a}'::ltree[] @> 'a.b'::ltree AS matched,'{a}'::ltree[] ?@> 'a.b'::ltree AS first,'{a}'::ltree[] AS paths,'{a.*}'::lquery[] AS queries WHERE "+predicate
   want:=[][]any{}; if predicate=="true" {want=[][]any{{true,"a","{a}","{a.*}"}}}
   step:=ltreeArraySelect(b,query,[]string{"matched","first","paths","queries"},[]string{"BOOL","LTREE","_LTREE","_LQUERY"},want)
   var retained []*sql.ColumnType
   step.CheckMetadata=func(t *testing.T,rows *sql.Rows){retained=ltreeArrayMetadata(t,b,rows,[]string{"BOOL","LTREE","_LTREE","_LQUERY"},nil)}
   run(step);ltreeArrayColumnMetadata(t,b,retained,[]string{"BOOL","LTREE","_LTREE","_LQUERY"},nil)
   run(fixture.Prepared(step));ltreeArrayColumnMetadata(t,b,retained,[]string{"BOOL","LTREE","_LTREE","_LQUERY"},nil)
  }
  for _,tc:=range ltreeArraysFirstCases(){
   for _,predicate:=range []string{"true","false"}{
    query:="SELECT "+tc.expression("'{}'::ltree[]",ltreeArrayLiteral(tc.right,tc.rightType),false)+" AS v WHERE "+predicate
    want:=[][]any{};if predicate=="true"{want=[][]any{{nil}}}
    step:=ltreeArraySelect(b,query,[]string{"v"},[]string{"LTREE"},want)
    var retained []*sql.ColumnType
    step.CheckMetadata=func(t *testing.T,rows *sql.Rows){retained=ltreeArrayMetadata(t,b,rows,[]string{"LTREE"},nil)}
    run(step);ltreeArrayColumnMetadata(t,b,retained,[]string{"LTREE"},nil)
    run(fixture.Prepared(step));ltreeArrayColumnMetadata(t,b,retained,[]string{"LTREE"},nil)
   }
  }
  step:=ltreeArraySelect(b,"SELECT NULL::ltree[] @> 'a'::ltree AS matched,NULL::ltree[] ?@> 'a'::ltree AS first",[]string{"matched","first"},[]string{"BOOL","LTREE"},[][]any{{nil,nil}})
  step.CheckMetadata=func(t *testing.T,rows *sql.Rows){ltreeArrayMetadata(t,b,rows,[]string{"BOOL","LTREE"},nil)}; run(step)
  run(fixture.Exec("CREATE TABLE la_metadata(paths ltree[] NOT NULL,queries lquery[] NOT NULL,nullable_paths ltree[],nullable_queries lquery[])",0))
  run(fixture.Exec("INSERT INTO la_metadata VALUES ('{a}','{a.*}',NULL,NULL)",1))
  run(fixture.Exec("CREATE VIEW la_metadata_view AS SELECT paths @> 'a.b'::ltree AS matched,paths ?@> 'a.b'::ltree AS first FROM la_metadata",0))
  for _,tc:=range []struct{query string;names,families []string;nullable []bool;want []any}{
   {"SELECT paths,queries,nullable_paths,nullable_queries FROM la_metadata",[]string{"paths","queries","nullable_paths","nullable_queries"},[]string{"_LTREE","_LQUERY","_LTREE","_LQUERY"},[]bool{false,false,true,true},[]any{"{a}","{a.*}",nil,nil}},
   {"SELECT matched,first FROM la_metadata_view",[]string{"matched","first"},[]string{"BOOL","LTREE"},[]bool{true,true},[]any{true,"a"}},
  } {
   for _,predicate:=range []string{"true","false"} {
    want:=[][]any{};if predicate=="true"{want=[][]any{tc.want}}
    step:=ltreeArraySelect(b,tc.query+" WHERE "+predicate,tc.names,tc.families,want)
    var retained []*sql.ColumnType
    step.CheckMetadata=func(t *testing.T,rows *sql.Rows){retained=ltreeArrayMetadata(t,b,rows,tc.families,tc.nullable)}
    run(step)
    ltreeArrayColumnMetadata(t,b,retained,tc.families,tc.nullable)
    run(fixture.Prepared(step))
    ltreeArrayColumnMetadata(t,b,retained,tc.families,tc.nullable)
   }
  }
 })
}
