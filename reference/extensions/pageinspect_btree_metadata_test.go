package reference_test

import (
 "database/sql"
 "math"
 "reflect"
 "testing"
 "stuntgres/reference/fixture"
)
func btMetadata(s fixture.Step,pg,view bool)fixture.Step{
 names,types:=s.Columns,s.Types;s.CheckMetadata=func(t *testing.T,r *sql.Rows){t.Helper();columns,err:=r.ColumnTypes();if err!=nil || len(columns)!=len(names){t.Fatal(columns,err)}
  for i,c:=range columns{if c.Name()!=names[i] || c.DatabaseTypeName()!=types[i]{t.Fatal(i,c.Name(),c.DatabaseTypeName())};var scan reflect.Type
   switch types[i]{case "INT2":scan=reflect.TypeFor[int64]();if pg{scan=reflect.TypeFor[int16]()};case "BOOL":scan=reflect.TypeFor[bool]();case "TEXT":scan=reflect.TypeFor[string]();case "TID","_TID","RECORD":scan=reflect.TypeFor[string]();if pg{scan=reflect.TypeFor[any]()};default:t.Fatal("unmapped metadata",types[i])}
   if c.ScanType()!=scan{t.Fatal(i,c.ScanType(),scan)};n,k:=c.Nullable();want:=view && !pg;if n!=want || k!=want{t.Fatal(i,n,k,want)}
   length,known:=c.Length();if types[i]=="TEXT"{if length!=math.MaxInt64 || !known{t.Fatal(i,length,known)}}else if length!=0 || known{t.Fatal(i,length,known)}
   precision,scale,known:=c.DecimalSize();if precision!=0 || scale!=0 || known{t.Fatal(i,precision,scale,known)}
  }
 };return s
}
func btDeclaration(name,schema,owner string)fixture.Step{
 s:=fixture.Select("SELECT p.proname::text AS name,n.nspname::text AS schema,r.rolname::text AS owner,p.pronargs::integer AS nargs,p.prorettype::bigint AS result,p.proargtypes::text AS args,p.proallargtypes::text AS allargs,p.proargmodes::text AS modes,p.proargnames::text AS names,p.proretset AS sets,p.proisstrict AS strict,p.provolatile::text AS volatility,p.proparallel::text AS parallel,p.prosrc AS source FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace JOIN pg_roles r ON r.oid=p.proowner JOIN pg_depend d ON d.objid=p.oid JOIN pg_extension e ON e.oid=d.refobjid WHERE d.classid=1255 AND d.refclassid=3079 AND d.deptype='e' AND e.extname='pageinspect' AND p.proname='"+name+"' AND p.proargtypes::text='17'",[]string{"name","schema","owner","nargs","result","args","allargs","modes","names","sets","strict","volatility","parallel","source"},[]string{"TEXT","TEXT","TEXT","INT4","INT8","TEXT","TEXT","TEXT","TEXT","BOOL","BOOL","TEXT","TEXT","TEXT"},[][]any{{name,schema,owner,int64(1),int64(2249),"17","{17,21,27,21,16,16,25,16,27,1010}","{i,o,o,o,o,o,o,o,o,o}","{page,itemoffset,ctid,itemlen,nulls,vars,data,dead,htid,tids}",true,true,"v","s","bt_page_items_bytea"}});s.Bag=true;return s
}
func TestReferencePageinspectBtreeMetadata(t *testing.T){btCase(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
 principal:="postgres";if b.PG{principal="stuntgres"};btBoth(t,p,b,c,btDeclaration("bt_page_items","bt_part",principal))
 btBoth(t,p,b,c,btRelationalIdentity())
 btBoth(t,p,b,c,fixture.Select("SELECT oid::bigint AS id,typelem::bigint AS element FROM pg_type WHERE typname='_tid'",[]string{"id","element"},[]string{"INT8","INT8"},[][]any{{int64(1010),int64(27)}}))
 st,err:=c.PrepareContext(p.Ctx,btQuery);if err!=nil{t.Fatal(err)};defer st.Close();held:=fixture.HeldStatement{Executor:c,Statement:st,SQL:btQuery}
 for _,s:=range []fixture.Step{btStep(btOrdinary(),btOrdinaryRows),btStep(btPosting(),btPostingRows),btStep(nil,nil),btStep(make([]byte,8192),btNullRows),btStep(btPage(1,0,0),nil),btStep(btPage(5,0,0),nil)}{s=btMetadata(s,b.PG,false);btBoth(t,p,b,c,s);fixture.RunStep(t,p.Ctx,held,b.Recorder,s)}
 s:=btStep(btOrdinary(),nil);s.Query="SELECT * FROM bt_part.bt_page_items($1) WHERE false";btBoth(t,p,b,c,btMetadata(s,b.PG,false))
 btBoth(t,p,b,c,btMetadata(fixture.Select("SELECT bt_part.bt_page_items($1) WHERE false",[]string{"bt_page_items"},[]string{"RECORD"},nil,btOrdinary()),b.PG,false))
})}

func btRelationalIdentity()fixture.Step{
 return fixture.Select("SELECT p.oid::bigint>0 AS valid,p.oid<>b.oid AS distinct_member FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace JOIN pg_proc b ON b.pronamespace=n.oid WHERE n.nspname='bt_part' AND p.proname='bt_page_items' AND p.proargtypes::text='17' AND b.proname='brin_page_type' AND b.proargtypes::text='17'",[]string{"valid","distinct_member"},[]string{"BOOL","BOOL"},[][]any{{true,true}})
}
