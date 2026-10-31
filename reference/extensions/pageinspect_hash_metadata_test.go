package reference_test

import (
 "database/sql"
 "testing"
 "stuntgres/reference/fixture"
)
func hashMetadataLifetime(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn,s fixture.Step,prepared bool){
 t.Helper();var rows *sql.Rows;var err error
 if prepared{st,e:=c.PrepareContext(p.Ctx,s.Query);if e!=nil{t.Fatal(e)};defer st.Close();rows,err=st.QueryContext(p.Ctx,s.Args...)}else{rows,err=c.QueryContext(p.Ctx,s.Query,s.Args...)}
 if err!=nil{t.Fatal(err)};defer rows.Close();columns,err:=rows.ColumnTypes();if err!=nil{t.Fatal(err)}
 check:=func(){fixture.HashCheckColumns(t,columns,s.Columns,s.Types,b.PG,false)};check();count:=0
 for rows.Next(){values:=make([]any,len(s.Columns));dest:=make([]any,len(values));for i:=range values{dest[i]=&values[i]};if err:=rows.Scan(dest...);err!=nil{t.Fatal(err)};count++}
 if err:=rows.Err();err!=nil{t.Fatal(err)};if count!=len(s.Rows){t.Fatal("hash metadata row count",count,len(s.Rows))};check();if err:=rows.Close();err!=nil{t.Fatal(err)};check()
 // Replacement results cannot mutate retained descriptors.
 next,err:=c.QueryContext(p.Ctx,hashQuery(2),fixture.HashPage(2));if err!=nil{t.Fatal(err)};next.Close();check()
}
func TestReferencePageinspectHashMetadata(t *testing.T){hashCase(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
 for i,name:=range fixture.HashMembers{
  steps:=[]fixture.Step{hashStep(i,fixture.HashOrdinary(i),fixture.HashRows(i)),hashStep(i,nil,fixture.HashNullRows(i))}
  empty:=hashStep(i,fixture.HashOrdinary(i),nil);empty.Query="SELECT * FROM hash_part."+name+"($1::bytea) WHERE false";steps=append(steps,empty)
  if i==0{steps=append(steps,hashStep(0,make([]byte,8192),[][]any{{"unused"}}))};if i==2{steps=append(steps,hashStep(2,fixture.HashPage(2),nil))}
  for _,s:=range steps{hashBoth(t,p,b,c,s);for _,prepared:=range []bool{false,true}{hashMetadataLifetime(t,p,b,c,s,prepared)}}
  if i==1||i==3{hashBoth(t,p,b,c,fixture.Select("SELECT hash_part."+name+"(NULL::bytea)",[]string{name},[]string{"RECORD"},[][]any{{nil}}))}
 }
 for _,prepared:=range []bool{false,true}{
  q:="SELECT procid FROM hash_part.hash_metapage_info($1)";var value uint32
  if prepared{st,err:=c.PrepareContext(p.Ctx,q);if err!=nil{t.Fatal(err)};err=st.QueryRowContext(p.Ctx,fixture.HashMeta()).Scan(&value);st.Close();if err!=nil{t.Fatal(err)}}else if err:=c.QueryRowContext(p.Ctx,q,fixture.HashMeta()).Scan(&value);err!=nil{t.Fatal(err)}
  if value!=4000000000{t.Fatal("unsigned OID scan",value)}
 }
})}
