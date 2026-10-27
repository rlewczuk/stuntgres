package reference_test

import (
 "database/sql"
 "math"
 "reflect"
 "testing"
 "stuntgres/reference/fixture"
)

func fsmMetadata(s fixture.Step,pg,view bool)fixture.Step{
 names:=append([]string(nil),s.Columns...)
 s.CheckMetadata=func(t *testing.T,r *sql.Rows){t.Helper();columns,err:=r.ColumnTypes();if err!=nil || len(columns)!=len(names){t.Fatal(columns,err)};for i,c:=range columns{
  if c.Name()!=names[i] || c.DatabaseTypeName()!="TEXT" || c.ScanType()!=reflect.TypeFor[string](){t.Fatal(i,c.Name(),c.DatabaseTypeName(),c.ScanType())}
  nullable,known:=c.Nullable();want:=view && !pg;if nullable!=want || known!=want{t.Fatal(i,nullable,known,want)}
  length,known:=c.Length();if length!=math.MaxInt64 || !known{t.Fatal(i,length,known)};precision,scale,known:=c.DecimalSize();if precision!=0 || scale!=0 || known{t.Fatal(i,precision,scale,known)}
 }};return s
}
func fsmDeclaration(name,schema,owner string)fixture.Step{
 return fixture.Select("SELECT p.proname::text AS name,n.nspname::text AS schema,r.rolname::text AS owner,p.pronargs::integer AS nargs,p.prorettype::bigint AS result,p.proargtypes::text AS args,p.proallargtypes::text AS allargs,p.proargmodes::text AS modes,p.proargnames::text AS names,p.proretset AS sets,p.proisstrict AS strict,p.provolatile::text AS volatility,p.proparallel::text AS parallel,p.prosrc AS source,l.lanname::text AS language FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace JOIN pg_roles r ON r.oid=p.proowner JOIN pg_language l ON l.oid=p.prolang JOIN pg_depend d ON d.objid=p.oid JOIN pg_extension e ON e.oid=d.refobjid WHERE d.classid=1255 AND d.refclassid=3079 AND d.deptype='e' AND e.extname='pageinspect' AND p.proname='"+name+"' AND p.proargtypes::text='17'",[]string{"name","schema","owner","nargs","result","args","allargs","modes","names","sets","strict","volatility","parallel","source","language"},[]string{"TEXT","TEXT","TEXT","INT4","INT8","TEXT","TEXT","TEXT","TEXT","BOOL","BOOL","TEXT","TEXT","TEXT","TEXT"},[][]any{{name,schema,owner,int64(1),int64(25),"17",nil,nil,"{page}",false,true,"v","s","fsm_page_contents","c"}})
}
func fsmRelationalIdentity(name,schema string)fixture.Step{return fixture.Select("SELECT p.oid::bigint>0 AS valid,p.oid<>b.oid AS distinct_member FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace JOIN pg_proc b ON b.pronamespace=(SELECT oid FROM pg_namespace WHERE nspname='fsm_part') WHERE n.nspname='"+schema+"' AND p.proname='"+name+"' AND p.proargtypes::text='17' AND b.proname='brin_page_type' AND b.proargtypes::text='17'",[]string{"valid","distinct_member"},[]string{"BOOL","BOOL"},[][]any{{true,true}})}
func TestReferencePageinspectFSMMetadata(t *testing.T){fsmCase(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
 principal:="postgres";if b.PG{principal="stuntgres"};fsmBoth(t,p,b,c,fsmDeclaration("fsm_page_contents","fsm_part",principal));fsmBoth(t,p,b,c,fsmRelationalIdentity("fsm_page_contents","fsm_part"))
 for _,query:=range []string{fsmScalar,fsmSource}{st,err:=c.PrepareContext(p.Ctx,query);if err!=nil{t.Fatal(err)};held:=fixture.HeldStatement{Executor:c,Statement:st,SQL:query}
  for _,tc:=range []struct{raw []byte;want any}{{fsmSparse(),fsmSparseText},{nil,nil},{make([]byte,8192),nil},{fsmSparse(),fsmSparseText}}{s:=fsmMetadata(fsmStep(query,tc.raw,tc.want),b.PG,false);fsmBoth(t,p,b,c,s);fixture.RunStep(t,p.Ctx,held,b.Recorder,s)};if err:=st.Close();err!=nil{t.Fatal(err)}
  for _,raw:=range [][]byte{fsmSparse(),nil,make([]byte,8192)}{s:=fsmStep(query+" WHERE false",raw,nil);s.Rows=nil;fsmBoth(t,p,b,c,fsmMetadata(s,b.PG,false))}
 }
})}
