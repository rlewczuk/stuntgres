package reference_test

import (
 "database/sql"
 "testing"
 "stuntgres/reference/fixture"
)

func TestReferenceLtreePathPrepared(t *testing.T) {
 ltreeScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn) {
  ltreeInstall(t,p,b,c);run:=func(s fixture.Step){fixture.RunStep(t,p.Ctx,c,b.Recorder,s)}
  run(fixture.Exec("CREATE TABLE lp_atomic(id integer,p ltree)",0))
  run(fixture.Exec("INSERT INTO lp_atomic VALUES (1,'A.B.C'),(2,'X.Y.Z')",2))
  query:="SELECT subpath($1::ltree,$2::integer) AS v"
  stmt,err:=c.PrepareContext(p.Ctx,query);if err!=nil{t.Fatal(err)};defer stmt.Close()
  held:=fixture.HeldStatement{Executor:c,Statement:stmt,SQL:query}
  for _,input:=range []struct{path any;offset int;want any}{{"A.B.C",1,"B.C"},{"X.Y.Z",-1,"Z"},{nil,0,nil},{"A.B.C",0,"A.B.C"}}{fixture.RunStep(t,p.Ctx,held,b.Recorder,ltreePathResult(b,query,"LTREE",input.want,input.path,input.offset))}
  bad:=fixture.ErrorDiagnostic(query,fixture.ControlsReferenceDiagnostic("22023","invalid positions"),"A.B.C",3);bad.SelectRows=true;bad.ErrorStage="execute"
  fixture.RunStep(t,p.Ctx,held,b.Recorder,bad)
  fixture.RunStep(t,p.Ctx,held,b.Recorder,ltreePathResult(b,query,"LTREE","B.C","A.B.C",1))
  original:=[]byte("A.B.C")
  rows,err:=stmt.QueryContext(p.Ctx,original,1);if err!=nil{t.Fatal(err)}
  original[2]='X'
  if !rows.Next(){t.Fatal("owned row missing",rows.Err())};var value any;if err:=rows.Scan(&value);err!=nil{t.Fatal(err)}
  if fixture.Normalize(value)!="B.C"{t.Fatal("bound byte slice changed retained result",value)};rows.Close()
  for _,q:=range []string{
   "INSERT INTO lp_atomic VALUES (3,subpath('A.B.C'::ltree,$1::integer)),(4,subpath('A'::ltree,$1::integer))",
   "INSERT INTO lp_atomic SELECT id+10,subpath(p,CASE WHEN id=1 THEN 1 ELSE $1::integer END) FROM lp_atomic ORDER BY id",
   "UPDATE lp_atomic SET p=subpath(p,CASE WHEN id=1 THEN 1 ELSE $1::integer END)",
  } {
   offset:=3;if q=="INSERT INTO lp_atomic VALUES (3,subpath('A.B.C'::ltree,$1::integer)),(4,subpath('A'::ltree,$1::integer))"{offset=1}
   s:=fixture.ErrorDiagnostic(q,fixture.ControlsReferenceDiagnostic("22023","invalid positions"),offset)
   for _,prepared:=range []bool{false,true}{s.Prepared=prepared;s.ErrorStage="execute";run(s)}
   run(ltreeSelect(b,"SELECT id,p FROM lp_atomic ORDER BY id",[]string{"id","p"},[][]any{{int64(1),"A.B.C"},{int64(2),"X.Y.Z"}}))
  }
  run(fixture.Exec("CREATE TABLE lp_selected(p ltree)",0))
  run(fixture.Exec("INSERT INTO lp_selected SELECT subpath(p,1) FROM lp_atomic WHERE id=1",1))
  run(ltreeSelect(b,"SELECT p FROM lp_selected",[]string{"p"},[][]any{{"B.C"}}))
  for _,q:=range []string{
   "SELECT ltree2text(subpath(text2ltree($1::text),1)) AS v",
   "SELECT v FROM subpath($1::ltree,1) AS f(v)",
  }{family:="LTREE";if q=="SELECT ltree2text(subpath(text2ltree($1::text),1)) AS v"{family="TEXT"};s:=ltreePathResult(b,q,family,"B.C","A.B.C");run(s);run(fixture.Prepared(s))}
  run(fixture.Select("SELECT id FROM lp_atomic WHERE nlevel(p)=3 ORDER BY id",[]string{"id"},[]string{"INT4"},[][]any{{int64(1)},{int64(2)}}))
  ltreePathSourceChecks(t,p,b,c)
 })
}

func TestReferenceLtreePathMetadata(t *testing.T) {
 ltreeScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn) {
  ltreeInstall(t,p,b,c);run:=func(s fixture.Step){fixture.RunStep(t,p.Ctx,c,b.Recorder,s)}
  run(fixture.Exec("CREATE TABLE lp_metadata(id integer,p ltree)",0));run(fixture.Exec("INSERT INTO lp_metadata VALUES (1,'A.B.C'),(2,''),(3,NULL)",3))
  run(fixture.Exec("CREATE VIEW lp_metadata_view AS SELECT id,nlevel(p) AS depth,ltree2text(p) AS txt,lca(p,p) AS common FROM lp_metadata",0))
  names:=[]string{"id","depth","txt","common"};families:=[]string{"INT4","INT4","TEXT","LTREE"}
  values:=[][]any{{int64(1),int64(3),"A.B.C","A.B"},{int64(2),int64(0),"",nil},{int64(3),nil,nil,nil}}
  for _,stored:=range []bool{false,true}{for _,empty:=range []bool{false,true}{
   q:="SELECT id,nlevel(p) AS depth,ltree2text(p) AS txt,lca(p,p) AS common FROM lp_metadata"
   if stored{q="SELECT id,depth,txt,common FROM lp_metadata_view"};want:=values;if empty{q+=" WHERE false";want=[][]any{}};q+=" ORDER BY id"
   s:=ltreeArraySelect(b,q,names,families,want)
   s.CheckMetadata=func(t *testing.T,rows *sql.Rows){columns,err:=rows.ColumnTypes();if err!=nil{t.Fatal(err)};nullable:=[]bool(nil);if stored{nullable=[]bool{true,true,true,true}};ltreePathColumnMetadata(t,b,columns,names,families,nullable);t.Cleanup(func(){ltreePathColumnMetadata(t,b,columns,names,families,nullable)})}
   run(s);run(fixture.Prepared(s))
  }}
 })
}
