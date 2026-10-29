package reference_test

import (
 "database/sql"
 "testing"

 "stuntgres/reference/fixture"
)

// database/sql does not permit asking a closed Rows for fresh ColumnTypes.
// Previously returned descriptors must nevertheless remain usable and unchanged.
func ginMetadataLifetime(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn,s fixture.Step,prepared bool) {
 t.Helper();var rows *sql.Rows;var err error
 if prepared {st,e:=c.PrepareContext(p.Ctx,s.Query);if e!=nil {t.Fatal(e)};defer st.Close();rows,err=st.QueryContext(p.Ctx,s.Args...)} else {rows,err=c.QueryContext(p.Ctx,s.Query,s.Args...)}
 if err!=nil {t.Fatal(err)};defer rows.Close()
 columns,err:=rows.ColumnTypes();if err!=nil {t.Fatal(err)};fixture.GINCheckColumns(t,columns,s.Columns,s.Types,b.PG,false)
 count:=0
 for rows.Next() {values:=make([]any,len(s.Columns));dest:=make([]any,len(values));for i:=range values {dest[i]=&values[i]};if err:=rows.Scan(dest...);err!=nil {t.Fatal(err)};count++}
 if err:=rows.Err();err!=nil {t.Fatal(err)};if count!=len(s.Rows) {t.Fatal("metadata lifetime row count",count,len(s.Rows))}
 fixture.GINCheckColumns(t,columns,s.Columns,s.Types,b.PG,false)
 if err:=rows.Close();err!=nil {t.Fatal(err)};fixture.GINCheckColumns(t,columns,s.Columns,s.Types,b.PG,false)
}
func ginIdentities() fixture.Step {
 return fixture.Select("SELECT m.oid::bigint>0 AS meta_valid,o.oid::bigint>0 AS opaque_valid,l.oid::bigint>0 AS leaf_valid,m.oid<>o.oid AS mo,m.oid<>l.oid AS ml,o.oid<>l.oid AS ol,m.proowner=o.proowner AS same_mo,m.proowner=l.proowner AS same_ml FROM pg_proc m JOIN pg_namespace n ON n.oid=m.pronamespace JOIN pg_proc o ON o.pronamespace=n.oid JOIN pg_proc l ON l.pronamespace=n.oid WHERE n.nspname='gin_part' AND m.proname='gin_metapage_info' AND o.proname='gin_page_opaque_info' AND l.proname='gin_leafpage_items' AND m.proargtypes::text='17' AND o.proargtypes::text='17' AND l.proargtypes::text='17'",[]string{"meta_valid","opaque_valid","leaf_valid","mo","ml","ol","same_mo","same_ml"},[]string{"BOOL","BOOL","BOOL","BOOL","BOOL","BOOL","BOOL","BOOL"},[][]any{{true,true,true,true,true,true,true,true}})
}
func TestReferencePageinspectGINMetadata(t *testing.T) {ginCase(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn) {
 principal:="postgres";if b.PG {principal="stuntgres"}
 for i,name:=range fixture.GINMembers {
  ginBoth(t,p,b,c,fixture.GINDeclaration(i,name,"gin_part",principal))
  null:=fixture.GINNullRows(i);if i==2 {null=nil}
  steps:=[]fixture.Step{ginStep(i,ginOrdinary(i),ginOrdinaryRows(i)),ginStep(i,nil,null),ginStep(i,make([]byte,8192),fixture.GINNullRows(i))}
  empty:=ginStep(i,ginOrdinary(i),nil);empty.Query="SELECT * FROM gin_part."+name+"($1) WHERE false";steps=append(steps,empty)
  if i==2 {steps=append(steps,ginStep(i,fixture.GINLeaf(),nil))}
  for _,s:=range steps {ginBoth(t,p,b,c,fixture.GINMetadata(s,b.PG,false));for _,prepared:=range []bool{false,true} {ginMetadataLifetime(t,p,b,c,s,prepared)}}
  if i<2 {for _,s:=range []fixture.Step{
   fixture.Select("SELECT gin_part."+name+"($1)",[]string{name},[]string{"RECORD"},[][]any{{[]string{"(4294967295,17,-1,19,23,29,31,37,41,2)",`(4294967295,65535,"{data,leaf,deleted,meta,list,list_fullrow,incomplete_split,compressed,ff00}")`}[i]}},ginOrdinary(i)),
   fixture.Select("SELECT gin_part."+name+"($1)",[]string{name},[]string{"RECORD"},[][]any{{nil}},nil),
   fixture.Select("SELECT gin_part."+name+"($1)",[]string{name},[]string{"RECORD"},[][]any{{nil}},make([]byte,8192)),
   fixture.Select("SELECT gin_part."+name+"($1) WHERE false",[]string{name},[]string{"RECORD"},nil,ginOrdinary(i)),
  } {ginBoth(t,p,b,c,fixture.GINMetadata(s,b.PG,false));for _,prepared:=range []bool{false,true} {ginMetadataLifetime(t,p,b,c,s,prepared)}}}
 }
 ginBoth(t,p,b,c,ginIdentities())
 ginBoth(t,p,b,c,fixture.Select("SELECT p.proname::text AS name,p.proargtypes::text AS args FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace WHERE n.nspname='gin_part' AND p.proname IN ('gin_metapage_info','gin_page_opaque_info','gin_leafpage_items') ORDER BY p.proname",[]string{"name","args"},[]string{"TEXT","TEXT"},[][]any{{"gin_leafpage_items","17"},{"gin_metapage_info","17"},{"gin_page_opaque_info","17"}}))
 ginBoth(t,p,b,c,fixture.Select("SELECT oid::bigint AS id,typelem::bigint AS element FROM pg_type WHERE typname IN ('_tid','_text') ORDER BY oid",[]string{"id","element"},[]string{"INT8","INT8"},[][]any{{int64(1009),int64(25)},{int64(1010),int64(27)}}))
})}
