package reference_test

import (
 "database/sql"
 "reflect"
 "strings"
 "testing"

 "stuntgres/reference/fixture"
)

func ginCheckColumns(t *testing.T,columns []*sql.ColumnType,names,types []string,pg,view bool) {
 t.Helper();if len(columns)!=len(names) {t.Fatal("column count",len(columns),names)}
 for i,c:=range columns {
  if c.Name()!=names[i] || c.DatabaseTypeName()!=types[i] {t.Fatal(i,c.Name(),c.DatabaseTypeName(),names[i],types[i])}
  var scan reflect.Type
  switch types[i] {
  case "INT8":scan=reflect.TypeFor[int64]()
  case "INT4":scan=reflect.TypeFor[int64]();if pg {scan=reflect.TypeFor[int32]()}
  case "INT2":scan=reflect.TypeFor[int64]();if pg {scan=reflect.TypeFor[int16]()}
  case "TID","_TID","_TEXT","RECORD":scan=reflect.TypeFor[string]();if pg {scan=reflect.TypeFor[any]()}
  default:t.Fatal("unmapped GIN type",types[i])
  }
  if c.ScanType()!=scan {t.Fatal(i,"ScanType",c.ScanType(),scan)}
  n,k:=c.Nullable();want:=view && !pg;if n!=want || k!=want {t.Fatal(i,"Nullable",n,k,"want",want,want)}
  length,known:=c.Length();if length!=0 || known {t.Fatal(i,"Length",length,known)}
  precision,scale,known:=c.DecimalSize();if precision!=0 || scale!=0 || known {t.Fatal(i,"DecimalSize",precision,scale,known)}
 }
}
func ginMetadata(s fixture.Step,pg,view bool) fixture.Step {
 s.CheckMetadata=func(t *testing.T,r *sql.Rows) {columns,err:=r.ColumnTypes();if err!=nil {t.Fatal(err)};ginCheckColumns(t,columns,s.Columns,s.Types,pg,view)}
 return s
}
// database/sql does not permit asking a closed Rows for fresh ColumnTypes.
// Previously returned descriptors must nevertheless remain usable and unchanged.
func ginMetadataLifetime(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn,s fixture.Step,prepared bool) {
 t.Helper();var rows *sql.Rows;var err error
 if prepared {st,e:=c.PrepareContext(p.Ctx,s.Query);if e!=nil {t.Fatal(e)};defer st.Close();rows,err=st.QueryContext(p.Ctx,s.Args...)} else {rows,err=c.QueryContext(p.Ctx,s.Query,s.Args...)}
 if err!=nil {t.Fatal(err)};defer rows.Close()
 columns,err:=rows.ColumnTypes();if err!=nil {t.Fatal(err)};ginCheckColumns(t,columns,s.Columns,s.Types,b.PG,false)
 count:=0
 for rows.Next() {values:=make([]any,len(s.Columns));dest:=make([]any,len(values));for i:=range values {dest[i]=&values[i]};if err:=rows.Scan(dest...);err!=nil {t.Fatal(err)};count++}
 if err:=rows.Err();err!=nil {t.Fatal(err)};if count!=len(s.Rows) {t.Fatal("metadata lifetime row count",count,len(s.Rows))}
 ginCheckColumns(t,columns,s.Columns,s.Types,b.PG,false)
 if err:=rows.Close();err!=nil {t.Fatal(err)};ginCheckColumns(t,columns,s.Columns,s.Types,b.PG,false)
}
func ginDeclaration(i int,name,schema,owner string) fixture.Step {
 allargs:=[]string{"{17,20,20,23,20,20,20,20,20,20,23}","{17,20,23,1009}","{17,27,21,1010}"}[i]
 modes:="{i"+strings.Repeat(",o",len(ginNames[i]))+"}"
 names:="{page,"+strings.Join(ginNames[i],",")+"}"
 rows:=float64(0);if i==2 {rows=1000}
 return fixture.Select("SELECT p.proname::text AS name,n.nspname::text AS schema,r.rolname::text AS owner,p.pronargs::integer AS nargs,p.prorettype::bigint AS result,p.proargtypes::text AS args,p.proallargtypes::text AS allargs,p.proargmodes::text AS modes,p.proargnames::text AS names,p.proretset AS sets,p.prorows AS rows,p.proisstrict AS strict,p.provolatile::text AS volatility,p.proparallel::text AS parallel,p.prosrc AS source,l.lanname::text AS language FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace JOIN pg_roles r ON r.oid=p.proowner JOIN pg_language l ON l.oid=p.prolang JOIN pg_depend d ON d.objid=p.oid JOIN pg_extension e ON e.oid=d.refobjid WHERE d.classid=1255 AND d.refclassid=3079 AND d.deptype='e' AND e.extname='pageinspect' AND p.proname='"+name+"' AND p.proargtypes::text='17'",[]string{"name","schema","owner","nargs","result","args","allargs","modes","names","sets","rows","strict","volatility","parallel","source","language"},[]string{"TEXT","TEXT","TEXT","INT4","INT8","TEXT","TEXT","TEXT","TEXT","BOOL","FLOAT4","BOOL","TEXT","TEXT","TEXT","TEXT"},[][]any{{name,schema,owner,int64(1),int64(2249),"17",allargs,modes,names,i==2,rows,true,"v","s",ginMembers[i],"c"}})
}
func ginIdentities() fixture.Step {
 return fixture.Select("SELECT m.oid::bigint>0 AS meta_valid,o.oid::bigint>0 AS opaque_valid,l.oid::bigint>0 AS leaf_valid,m.oid<>o.oid AS mo,m.oid<>l.oid AS ml,o.oid<>l.oid AS ol,m.proowner=o.proowner AS same_mo,m.proowner=l.proowner AS same_ml FROM pg_proc m JOIN pg_namespace n ON n.oid=m.pronamespace JOIN pg_proc o ON o.pronamespace=n.oid JOIN pg_proc l ON l.pronamespace=n.oid WHERE n.nspname='gin_part' AND m.proname='gin_metapage_info' AND o.proname='gin_page_opaque_info' AND l.proname='gin_leafpage_items' AND m.proargtypes::text='17' AND o.proargtypes::text='17' AND l.proargtypes::text='17'",[]string{"meta_valid","opaque_valid","leaf_valid","mo","ml","ol","same_mo","same_ml"},[]string{"BOOL","BOOL","BOOL","BOOL","BOOL","BOOL","BOOL","BOOL"},[][]any{{true,true,true,true,true,true,true,true}})
}
func TestReferencePageinspectGINMetadata(t *testing.T) {ginCase(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn) {
 principal:="postgres";if b.PG {principal="stuntgres"}
 for i,name:=range ginMembers {
  ginBoth(t,p,b,c,ginDeclaration(i,name,"gin_part",principal))
  null:=ginNullRows(i);if i==2 {null=nil}
  steps:=[]fixture.Step{ginStep(i,ginOrdinary(i),ginOrdinaryRows(i)),ginStep(i,nil,null),ginStep(i,make([]byte,8192),ginNullRows(i))}
  empty:=ginStep(i,ginOrdinary(i),nil);empty.Query="SELECT * FROM gin_part."+name+"($1) WHERE false";steps=append(steps,empty)
  if i==2 {steps=append(steps,ginStep(i,ginLeaf(),nil))}
  for _,s:=range steps {ginBoth(t,p,b,c,ginMetadata(s,b.PG,false));for _,prepared:=range []bool{false,true} {ginMetadataLifetime(t,p,b,c,s,prepared)}}
  if i<2 {for _,s:=range []fixture.Step{
   fixture.Select("SELECT gin_part."+name+"($1)",[]string{name},[]string{"RECORD"},[][]any{{[]string{"(4294967295,17,-1,19,23,29,31,37,41,2)",`(4294967295,65535,"{data,leaf,deleted,meta,list,list_fullrow,incomplete_split,compressed,ff00}")`}[i]}},ginOrdinary(i)),
   fixture.Select("SELECT gin_part."+name+"($1)",[]string{name},[]string{"RECORD"},[][]any{{nil}},nil),
   fixture.Select("SELECT gin_part."+name+"($1)",[]string{name},[]string{"RECORD"},[][]any{{nil}},make([]byte,8192)),
   fixture.Select("SELECT gin_part."+name+"($1) WHERE false",[]string{name},[]string{"RECORD"},nil,ginOrdinary(i)),
  } {ginBoth(t,p,b,c,ginMetadata(s,b.PG,false));for _,prepared:=range []bool{false,true} {ginMetadataLifetime(t,p,b,c,s,prepared)}}}
 }
 ginBoth(t,p,b,c,ginIdentities())
 ginBoth(t,p,b,c,fixture.Select("SELECT p.proname::text AS name,p.proargtypes::text AS args FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace WHERE n.nspname='gin_part' AND p.proname IN ('gin_metapage_info','gin_page_opaque_info','gin_leafpage_items') ORDER BY p.proname",[]string{"name","args"},[]string{"TEXT","TEXT"},[][]any{{"gin_leafpage_items","17"},{"gin_metapage_info","17"},{"gin_page_opaque_info","17"}}))
 ginBoth(t,p,b,c,fixture.Select("SELECT oid::bigint AS id,typelem::bigint AS element FROM pg_type WHERE typname IN ('_tid','_text') ORDER BY oid",[]string{"id","element"},[]string{"INT8","INT8"},[][]any{{int64(1009),int64(25)},{int64(1010),int64(27)}}))
})}
