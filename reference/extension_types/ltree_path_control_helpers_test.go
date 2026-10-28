package reference_test

import (
 "database/sql"
 "testing"
 "stuntgres/reference/fixture"
)

const ltreePathSchema="lp_control"
const ltreePathProjection="id,CASE WHEN id<3 THEN subpath(p,1) ELSE p END AS sliced,CASE WHEN id<3 THEN subltree(p,0,1) ELSE p END AS cut,nlevel(p) AS depth,index(p,'Missing'::ltree) AS pos,ltree2text(p) AS txt,text2ltree(t) AS converted,lca(p,p) AS common,lca(p,p,p,p,p,p,p,p) AS common8,lca(a) AS arr,p || t AS joined,t || p AS prefixed,p || p AS doubled"
var ltreePathControlNames=[]string{"id","sliced","cut","depth","pos","txt","converted","common","common8","arr","joined","prefixed","doubled"}
var ltreePathControlFamilies=[]string{"INT4","LTREE","LTREE","INT4","INT4","TEXT","LTREE","LTREE","LTREE","LTREE","LTREE","LTREE","LTREE"}
var ltreePathControlRows=[][]any{
 {int64(1),"Child1.Child2","Top",int64(3),int64(-1),"Top.Child1.Child2","Leaf","Top.Child1","Top.Child1","Top.Child1","Top.Child1.Child2.Leaf","Leaf.Top.Child1.Child2","Top.Child1.Child2.Top.Child1.Child2"},
 {int64(2),"B.C","A",int64(3),int64(-1),"A.B.C","D","A.B","A.B","A.B","A.B.C.D","D.A.B.C","A.B.C.A.B.C"},
 {int64(3),"","",int64(0),int64(-1),"","",nil,nil,nil,"","",""},
 {int64(4),nil,nil,nil,nil,nil,nil,nil,nil,nil,nil,nil,nil},
}
func ltreePathControlSetup(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn) {
 t.Helper();run:=func(s fixture.Step){fixture.RunStep(t,p.Ctx,c,b.Recorder,s)}
 for _,q:=range []string{"CREATE SCHEMA "+ltreePathSchema,"CREATE EXTENSION ltree SCHEMA "+ltreePathSchema,"SET search_path=public,"+ltreePathSchema,"CREATE TABLE lp_paths(id integer,p ltree,t text,a ltree[])"}{run(fixture.Exec(q,0))}
 run(fixture.Exec("INSERT INTO lp_paths VALUES (1,'Top.Child1.Child2','Leaf','{Top.Child1.Child2,Top.Child1.Other}'),(2,'A.B.C','D','[7:8]={A.B.C,A.B.D}'),(3,'','','{}'),(4,NULL,NULL,NULL)",4))
 run(fixture.Exec("CREATE VIEW lp_view AS SELECT "+ltreePathProjection+" FROM lp_paths",0))
 run(fixture.Exec("CREATE VIEW lp_empty AS SELECT "+ltreePathProjection+" FROM lp_paths WHERE false",0))
 // Mutable routine presentation must not replace installed declaration authority.
 run(fixture.Exec("ALTER FUNCTION "+ltreePathSchema+".nlevel(ltree) RENAME TO lp_depth",0))
 for _,signature:=range []string{"ltree,ltree","ltree,text","text,ltree"}{
  run(fixture.Exec("ALTER OPERATOR "+ltreePathSchema+".||("+signature+") SET SCHEMA public",0))
 }
}
func ltreePathControlChecks(t *testing.T,p *fixture.Pair,b fixture.Backend,c fixture.Executor) {
 t.Helper();run:=func(s fixture.Step){fixture.RunStep(t,p.Ctx,c,b.Recorder,s)}
 run(fixture.Exec("SET search_path=public,"+ltreePathSchema,0))
 for _,name:=range []string{"lp_view","lp_empty"}{
  expected:=ltreePathControlRows;if name=="lp_empty"{expected=[][]any{}}
  s:=ltreeArraySelect(b,"SELECT * FROM "+name+" ORDER BY id",ltreePathControlNames,ltreePathControlFamilies,expected)
  s.CheckMetadata=func(t *testing.T,rows *sql.Rows){columns,err:=rows.ColumnTypes();if err!=nil{t.Fatal(err)};nullable:=make([]bool,len(columns));for i:=range nullable{nullable[i]=true};ltreePathColumnMetadata(t,b,columns,ltreePathControlNames,ltreePathControlFamilies,nullable);t.Cleanup(func(){ltreePathColumnMetadata(t,b,columns,ltreePathControlNames,ltreePathControlFamilies,nullable)})}
  run(s);run(fixture.Prepared(s))
 }
 run(ltreePathResult(b,"SELECT "+ltreePathSchema+".lp_depth('A.B.C'::ltree) AS v","INT4",int64(3)))
 for _,types:=range [][2]string{{"ltree","ltree"},{"ltree","text"},{"text","ltree"}}{
  run(ltreePathResult(b,"SELECT 'A'::"+types[0]+" OPERATOR(public.||) 'B'::"+types[1]+" AS v","LTREE","A.B"))
 }
}
func ltreePathHeldChecks(t *testing.T,p *fixture.Pair,b fixture.Backend,held fixture.HeldStatement) {
 t.Helper()
 for _,tc:=range []struct{arg any;want any}{{"Top.Child1.Child2","Child1.Child2"},{"A.B.C","B.C"},{nil,nil}}{fixture.RunStep(t,p.Ctx,held,b.Recorder,ltreePathResult(b,held.SQL,"TEXT",tc.want,tc.arg))}
}
func ltreePathDrop(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn) {
 t.Helper();for _,q:=range []string{"DROP VIEW lp_view","DROP VIEW lp_empty","DROP TABLE lp_paths","DROP EXTENSION ltree","DROP SCHEMA "+ltreePathSchema}{fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Exec(q,0))}
}
func ltreePathRetain(t *testing.T,p *fixture.Pair,b fixture.Backend) (*sql.Rows,[]*sql.ColumnType) {
 t.Helper()
 q:="SELECT "+ltreePathSchema+".lp_depth(p) AS depth,"+ltreePathSchema+".ltree2text(p) AS txt,"+ltreePathSchema+".lca(p,p) AS common FROM public.lp_paths ORDER BY id"
 rows,err:=b.DB.QueryContext(p.Ctx,q);if err!=nil{t.Fatal(err)};t.Cleanup(func(){rows.Close()})
 columns,err:=rows.ColumnTypes();if err!=nil{t.Fatal(err)}
 ltreePathColumnMetadata(t,b,columns,[]string{"depth","txt","common"},[]string{"INT4","TEXT","LTREE"},nil)
 return rows,columns
}
func ltreePathConsume(t *testing.T,rows *sql.Rows,columns []*sql.ColumnType) {
 t.Helper();want:=[][]any{{int64(3),"Top.Child1.Child2","Top.Child1"},{int64(3),"A.B.C","A.B"},{int64(0),"",nil},{nil,nil,nil}};i:=0
 for rows.Next(){var depth,text,common any;if err:=rows.Scan(&depth,&text,&common);err!=nil{t.Fatal(err)};if i>=len(want)||depth!=want[i][0]||text!=want[i][1]||common!=want[i][2]{t.Fatalf("retained path row %d: %v/%v/%v",i,depth,text,common)};i++}
 if err:=rows.Err();err!=nil{t.Fatal(err)};rows.Close();if i!=len(want){t.Fatal("retained path row count",i)}
 ltreePathColumnMetadata(t,fixture.Backend{},columns,[]string{"depth","txt","common"},[]string{"INT4","TEXT","LTREE"},nil)
}
