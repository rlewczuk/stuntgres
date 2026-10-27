package reference_test

import (
 "database/sql"
 "encoding/hex"
 "testing"

 "stuntgres/reference/fixture"
)

func TestReferencePageinspectGINBinding(t *testing.T) {
 t.Run("missing_installation",func(t *testing.T) {ginCaseSetup(t,false,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn) {
  for i,name:=range ginMembers {ginBoth(t,p,b,c,ginMissing(ginQuery(i),"gin_part."+name+"(unknown)",ginOrdinary(i)))}
 })})
 t.Run("installed",func(t *testing.T) {ginCase(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn) {
  run:=func(s fixture.Step) {fixture.RunStep(t,p.Ctx,c,b.Recorder,s)}
  run(fixture.Exec("CREATE DOMAIN gin_part.image AS bytea",0))
  run(fixture.Exec("CREATE TABLE gin_binding_images(id integer,raw bytea)",0))
  ginBoth(t,p,b,c,fixture.Select("SELECT id FROM gin_binding_images ORDER BY id",[]string{"id"},[]string{"INT4"},nil))
  for i,name:=range ginMembers {
   raw,want:=ginOrdinary(i),ginOrdinaryRows(i)
   // No embedded rows exist when the independently supplied page returns data.
   ginBoth(t,p,b,c,ginStep(i,raw,want))
   for _,arg:=range []string{"page => $1","$1::bytea","$1::bytea::gin_part.image"} {
    s:=ginStep(i,raw,want);s.Query="SELECT * FROM gin_part."+name+"("+arg+")";if i==2 {s.Query+=" ORDER BY first_tid"};ginBoth(t,p,b,c,s)
   }
   literal:=ginStep(i,raw,want);literal.Args=nil;literal.Query="SELECT * FROM gin_part."+name+"('\\x"+hex.EncodeToString(raw)+"'::bytea)";if i==2 {literal.Query+=" ORDER BY first_tid"};ginBoth(t,p,b,c,literal)
   badQuery:="SELECT * FROM gin_part."+name+"(1)";ginBoth(t,p,b,c,ginMissing(badQuery,"gin_part."+name+"(integer)"))
   st,err:=c.PrepareContext(p.Ctx,ginQuery(i));if err!=nil {t.Fatal(err)}
   held:=fixture.HeldStatement{Executor:c,Statement:st,SQL:ginQuery(i)}
   domainQuery:="SELECT * FROM gin_part."+name+"($1::bytea::gin_part.image)";if i==2 {domainQuery+=" ORDER BY first_tid"}
   domainStatement,err:=c.PrepareContext(p.Ctx,domainQuery);if err!=nil {t.Fatal(err)}
   domainHeld:=fixture.HeldStatement{Executor:c,Statement:domainStatement,SQL:domainQuery}
   boundary:=ginOrdinary(i);boundaryRows:=ginOrdinaryRows(i)
   switch i {case 0:boundary[72]=1;row:=append([]any(nil),ginMetaRows[0]...);row[9]=int64(1);boundaryRows=[][]any{row}
   case 1:boundary=ginPage(0);boundaryRows=[][]any{{int64(4294967295),int64(65535),"{}"}}
   case 2:boundary=ginLeaf(ginSegment{0,0,nil});boundaryRows=[][]any{{"(0,0)",int64(0),`{"(0,0)"}`}}}
   null:=ginNullRows(i);if i==2 {null=nil}
   steps:=[]fixture.Step{ginStep(i,raw,want),ginStep(i,boundary,boundaryRows),ginStep(i,nil,null),ginStep(i,make([]byte,8192),ginNullRows(i))}
   if i==2 {steps=append(steps,ginStep(i,ginLeaf(),nil))};steps=append(steps,ginStep(i,raw,want))
   for _,s:=range steps {
    fixture.RunStep(t,p.Ctx,held,b.Recorder,s)
    domainStep:=s;domainStep.Query=domainQuery
    fixture.RunStep(t,p.Ctx,domainHeld,b.Recorder,ginMetadata(domainStep,b.PG,false))
   }
   if err:=st.Close();err!=nil {t.Fatal(err)}
   if err:=domainStatement.Close();err!=nil {t.Fatal(err)}
   run(fixture.Exec("INSERT INTO gin_binding_images VALUES($1,$2)",1,i,raw))
   from:=ginStep(i,raw,want);from.Args=nil;from.Query="SELECT f.* FROM gin_binding_images AS g CROSS JOIN LATERAL gin_part."+name+"(g.raw) AS f WHERE g.id="+[]string{"0","1","2"}[i];if i==2 {from.Query+=" ORDER BY first_tid"};ginBoth(t,p,b,c,from)
  }
  for i:=range 2 {
   name:=ginMembers[i];want:=[]string{"(4294967295,17,-1,19,23,29,31,37,41,2)",`(4294967295,65535,"{data,leaf,deleted,meta,list,list_fullrow,incomplete_split,compressed,ff00}")`}[i]
   for _,tc:=range []struct{raw []byte;want any}{{ginOrdinary(i),want},{nil,nil},{make([]byte,8192),nil}} {
    ginBoth(t,p,b,c,fixture.Select("SELECT gin_part."+name+"($1)",[]string{name},[]string{"RECORD"},[][]any{{tc.want}},tc.raw))
    ginBoth(t,p,b,c,fixture.Select("SELECT gin_part."+name+"($1) IS NULL AS is_null",[]string{"is_null"},[]string{"BOOL"},[][]any{{tc.want==nil}},tc.raw))
   }
  }
  target:=fixture.Select("SELECT gin_part.gin_leafpage_items($1)",[]string{"gin_leafpage_items"},[]string{"RECORD"},[][]any{
   {`("(8,41)",3,"{""(8,41)"",""(8,43)"",""(8,44)"",""(8,171)""}")`},
   {`("(65538,513)",2,"{""(65538,513)"",""(65539,1)""}")`},
   {`("(90000,7)",0,"{""(90000,7)""}")`},
  },ginPosting());target.Bag=true;ginBoth(t,p,b,c,ginMetadata(target,b.PG,false))
  for _,tc:=range []struct{raw []byte;rows [][]any}{{nil,nil},{make([]byte,8192),[][]any{{nil}}},{ginLeaf(),nil}} {s:=target;s.Args=[]any{tc.raw};s.Rows=tc.rows;ginBoth(t,p,b,c,ginMetadata(s,b.PG,false))}
  emptyTarget:=target;emptyTarget.Query+=" WHERE false";emptyTarget.Rows=nil;ginBoth(t,p,b,c,ginMetadata(emptyTarget,b.PG,false))
 })})
}
