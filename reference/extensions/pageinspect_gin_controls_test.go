package reference_test

import (
 "context"
 "database/sql"
 "errors"
 "strings"
 "testing"

 "stuntgres"
 "stuntgres/reference/fixture"
)

func ginControls(t *testing.T,smoke bool) {
 fixture.RoleReferenceCleanup(t,[]string{"gin_owner","gin_reader"})
 ginCase(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn) {
  run:=func(s fixture.Step) {t.Helper();fixture.RunStep(t,p.Ctx,c,b.Recorder,s)}
  for _,q:=range []string{
   "CREATE ROLE gin_owner SUPERUSER","CREATE SCHEMA gin_moved","CREATE TABLE gin_images(meta bytea,opaque bytea,leaf bytea)",
  } {run(fixture.Exec(q,0))}
  run(fixture.Exec("INSERT INTO gin_images VALUES($1,$2,$3)",1,fixture.GINMeta(),fixture.GINPage(0xffff),fixture.GINPosting()))
  for _,q:=range []string{
   "CREATE VIEW gin_meta_view AS SELECT (gin_part.gin_metapage_info(meta)).* FROM gin_images",
   "CREATE VIEW gin_opaque_view AS SELECT (gin_part.gin_page_opaque_info(opaque)).* FROM gin_images",
   "CREATE VIEW gin_leaf_view AS SELECT l.* FROM gin_images CROSS JOIN LATERAL gin_part.gin_leafpage_items(leaf) AS l",
  } {run(fixture.Exec(q,0))}
  held:=make([]fixture.HeldStatement,len(fixture.GINMembers))
  for i:=range fixture.GINMembers {
   view:=ginViewStep(i,ginOrdinaryRows(i));st,err:=c.PrepareContext(p.Ctx,view.Query);if err!=nil {t.Fatal(err)};defer st.Close()
   held[i]=fixture.HeldStatement{Executor:c,Statement:st,SQL:view.Query}
   fixture.RunStep(t,p.Ctx,held[i],b.Recorder,fixture.GINMetadata(view,b.PG,true))
   rawStatement,err:=c.PrepareContext(p.Ctx,ginQuery(i));if err!=nil {t.Fatal(err)}
   rawHeld:=fixture.HeldStatement{Executor:c,Statement:rawStatement,SQL:ginQuery(i)}
   null:=fixture.GINNullRows(i);if i==2 {null=nil}
   changed,changedRows:=ginChanged(i)
   steps:=[]fixture.Step{ginStep(i,ginOrdinary(i),ginOrdinaryRows(i)),ginStep(i,changed,changedRows),ginStep(i,nil,null),ginStep(i,make([]byte,8192),fixture.GINNullRows(i))}
   if i==2 {steps=append(steps,ginStep(i,fixture.GINLeaf(),nil))};steps=append(steps,ginStep(i,ginOrdinary(i),ginOrdinaryRows(i)))
   for _,s:=range steps {s=fixture.GINMetadata(s,b.PG,false);ginBoth(t,p,b,c,s);fixture.RunStep(t,p.Ctx,rawHeld,b.Recorder,s)}
   if smoke {
    bad:=ginError(i,[]byte{1,2,3},"22023","invalid page size","Expected 8192 bytes, got 3.")
    ginBoth(t,p,b,c,bad);fixture.RunStep(t,p.Ctx,rawHeld,b.Recorder,bad)
    if i==2 {
     carry:=fixture.GINMetadata(ginStep(2,fixture.GINLeaf(fixture.GINSegment{4294967294,2047,[]byte{0x82,0x10}}),[][]any{{"(4294967294,2047)",int64(2),`{"(4294967294,2047)","(0,1)"}`}}),b.PG,false)
     ginBoth(t,p,b,c,carry);fixture.RunStep(t,p.Ctx,rawHeld,b.Recorder,carry)
    }
    fixture.RunStep(t,p.Ctx,rawHeld,b.Recorder,ginStep(i,ginOrdinary(i),ginOrdinaryRows(i)))
   }
   if err:=rawStatement.Close();err!=nil {t.Fatal(err)}
  }
  if smoke {ginAuthority(t,p,b,c)}
  if !smoke {run(fixture.Exec("CREATE ROLE gin_reader",0))}
  run(fixture.Exec("GRANT USAGE ON SCHEMA public,gin_part,gin_moved TO gin_reader",0))
  run(fixture.Exec("GRANT SELECT ON gin_meta_view,gin_opaque_view,gin_leaf_view TO gin_reader",0))
  ginControlCheck(t,p,b,c,false);ginStoredInputs(t,p,b,c,held)
  defaultDump:="";if !b.PG {defaultDump=fixture.ExtensionDump(t,p.Ctx,p.Native)}
  // Capture genuine procedure identities, not allocator values from another DB.
  identities:=make([]int64,len(fixture.GINMembers))
  for i,name:=range fixture.GINMembers {
   query:="SELECT p.oid::bigint FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace WHERE n.nspname='gin_part' AND p.proname='"+name+"' AND p.proargtypes::text='17'"
   if err:=c.QueryRowContext(p.Ctx,query).Scan(&identities[i]);err!=nil || identities[i]<=0 {t.Fatal("initial GIN identity",identities[i],err)}
   for _,q:=range []string{
    "ALTER FUNCTION gin_part."+name+"(bytea) OWNER TO gin_owner",
    "ALTER FUNCTION gin_part."+name+"(bytea) RENAME TO "+ginMovedNames[i],
    "ALTER FUNCTION gin_part."+ginMovedNames[i]+"(bytea) SET SCHEMA gin_moved",
   } {run(fixture.Exec(q,0))}
   ginBoth(t,p,b,c,ginMissing(ginQuery(i),"gin_part."+name+"(unknown)",ginOrdinary(i)))
   ginBoth(t,p,b,c,fixture.Select("SELECT p.oid::bigint AS id FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace WHERE n.nspname='gin_moved' AND p.proname='"+ginMovedNames[i]+"' AND p.proargtypes::text='17'",[]string{"id"},[]string{"INT8"},[][]any{{identities[i]}}))
  }
  checkOriginal:=func() {
   ginControlCheck(t,p,b,c,true)
   for i:=range held {fixture.RunStep(t,p.Ctx,held[i],b.Recorder,fixture.GINMetadata(ginViewStep(i,ginOrdinaryRows(i)),b.PG,true))}
  }
  checkOriginal();ginStoredInputs(t,p,b,c,held);checkOriginal()
  ginHeldViewAuthority(t,p,b,c,held);checkOriginal()
  if b.PG {return}
  // Public adapter cancellation is separate from decoder work cancellation.
  canceled,cancel:=context.WithCancel(p.Ctx);cancel()
  for i,name:=range ginMovedNames {
   rows,err:=c.QueryContext(canceled,"SELECT * FROM gin_moved."+name+"($1)",ginOrdinary(i))
   if rows!=nil {rows.Close();t.Fatal("pre-canceled GIN query published Rows")}
   if !errors.Is(err,context.Canceled) {t.Fatal("pre-canceled GIN query",err)}
   var state interface{SQLState() string};if errors.As(err,&state) {t.Fatal("context error invented SQLSTATE",err)}
  }
  dump:=fixture.ExtensionDump(t,p.Ctx,p.Native)
  other,destination,r:=pgcryptoControlDestination(t,p,"gin_restore")
  for _,q:=range []string{"CREATE ROLE gin_owner SUPERUSER","CREATE ROLE gin_reader"} {if _,err:=other.ExecContext(p.Ctx,q);err!=nil {t.Fatal(err)}}
  restored:=fixture.Backend{DB:other,Recorder:r}
  if err:=stuntgres.Restore(p.Ctx,other,strings.NewReader(defaultDump));err!=nil {t.Fatal(err)}
  ginControlCheck(t,p,restored,destination,false);ginStoredInputs(t,p,restored,destination,nil)
  if err:=stuntgres.Restore(p.Ctx,other,strings.NewReader(dump));err!=nil {t.Fatal(err)}
  ginControlCheck(t,p,restored,destination,true);ginStoredInputs(t,p,restored,destination,nil)
  if redump:=fixture.ExtensionDump(t,p.Ctx,other);redump!=dump {t.Fatal("symbolic GIN replay changed")}
  consumeOld:=ginRetainViews(t,p,b,c)
  ginStoredInputs(t,p,b,c,held)
  if err:=stuntgres.Reset(p.Ctx,p.Native);err!=nil {t.Fatal(err)}
  for i,name:=range ginMovedNames {rows,err:=c.QueryContext(p.Ctx,"SELECT * FROM gin_moved."+name+"($1)",ginOrdinary(i));if err==nil {rows.Close();t.Fatal("reset retained callable GIN provider",name)}}
  consumeOld()
  run(fixture.Select("SELECT extname::text AS name FROM pg_extension WHERE extname='pageinspect'",[]string{"name"},[]string{"TEXT"},nil))
  if err:=stuntgres.Restore(p.Ctx,p.Native,strings.NewReader(dump));err!=nil {t.Fatal(err)}
  checkOriginal();ginStoredInputs(t,p,b,c,held);checkOriginal()
  ginHeldViewAuthority(t,p,b,c,held);checkOriginal()
  before:=fixture.ExtensionDump(t,p.Ctx,p.Native)
  for _,bad:=range []string{"CREATE EXTENSION missing_pageinspect_provider;",ginBadOwner(t,dump)} {
   if err:=stuntgres.Restore(p.Ctx,p.Native,strings.NewReader(bad));err==nil {t.Fatal("invalid GIN restore accepted")}
   if fixture.ExtensionDump(t,p.Ctx,p.Native)!=before {t.Fatal("failed restore replaced live GIN state")}
   checkOriginal()
  }
 })
}
func TestReferencePageinspectGINControls(t *testing.T) {ginControls(t,false)}
func TestReferencePageinspectGINFeatureSmoke(t *testing.T) {ginControls(t,true)}
