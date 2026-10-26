package reference_test

import (
 "database/sql"
 "strings"
 "testing"

 "stuntgres/reference/fixture"
)

func tcIdentity(t *testing.T,p *fixture.Pair,b fixture.Backend,x fixture.Executor,r *fixture.Recorder) {
 t.Helper()
 var attributes [][]any
 for i,name:=range tcColumns {
  domain,base:="sql_identifier",int64(19)
  if i==6 { domain,base="character_data",1043 } else if i>6 { domain,base="yes_or_no",1043 }
  attributes=append(attributes,[]any{int64(i+1),name,"information_schema",domain,"d",base,int64(-1),false,int64(950),int64(0),false})
 }
 fixture.PgDescriptionExecutorRows(t,p,b,x,fixture.Select(`SELECT a.attnum,a.attname,n.nspname,t.typname,t.typtype,t.typbasetype,a.atttypmod,a.attnotnull,a.attcollation,a.attndims,t.typnotnull FROM pg_catalog.pg_attribute a JOIN pg_catalog.pg_type t ON t.oid=a.atttypid JOIN pg_catalog.pg_namespace n ON n.oid=t.typnamespace WHERE a.attrelid='information_schema.table_constraints'::regclass AND a.attnum>0 ORDER BY a.attnum`,[]string{"attnum","attname","nspname","typname","typtype","typbasetype","atttypmod","attnotnull","attcollation","attndims","typnotnull"},[]string{"INT2","NAME","NAME","NAME","CHAR","OID","INT4","BOOL","OID","INT2","BOOL"},attributes))
 fixture.RunStep(t,p.Ctx,x,r,fixture.Select(`SELECT c.reltype=t.oid AS same_type,t.typrelid=c.oid AS same_relation,a.typelem=t.oid AS array_element,t.typarray=a.oid AS array_link FROM pg_catalog.pg_class c JOIN pg_catalog.pg_type t ON t.oid=c.reltype JOIN pg_catalog.pg_type a ON a.oid=t.typarray WHERE c.oid='information_schema.table_constraints'::regclass`,[]string{"same_type","same_relation","array_element","array_link"},[]string{"BOOL","BOOL","BOOL","BOOL"},[][]any{{true,true,true,true}}))
}

func TestReferenceInfoTableConstraintsShape(t *testing.T) {
 tcScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn,r *fixture.Recorder,database string) {
  run:=func(s fixture.Step) { fixture.RunStep(t,p.Ctx,c,r,s) }
  tcIdentity(t,p,b,c,r)
  tcSeed(run)
  var uniques,others [][]any
  for _,row:=range tcSeedRows(database) { if row[10]==nil { others=append(others,row) } else { uniques=append(uniques,row) } }
  for _,s:=range []fixture.Step{
   tcProjection(b.PG,"table_schema='tc_scope'",tcSeedRows(database)),
   tcProjection(b.PG,"table_schema='tc_absent'",nil),
   tcProjection(b.PG,"table_schema='tc_scope' AND table_name='absent'",nil),
   tcProjection(b.PG,"FALSE",nil),
   tcProjection(b.PG,"table_name IS NULL",nil),
   tcProjection(b.PG,"table_schema='tc_scope' AND nulls_distinct IS NULL",others),
   tcProjection(b.PG,"table_schema='tc_scope' AND nulls_distinct IS NOT NULL",uniques),
  } {
   for _,prepared:=range []bool{false,true} { for _,star:=range []bool{false,true} {
    step:=s;step.Prepared=prepared
    if star { step.Query=strings.Replace(step.Query,strings.Join(tcColumns,","),"*",1) }
    run(step)
    rows,closeQuery,err:=fixture.TransactionQuery(p.Ctx,c,step.Query,prepared)
    if err!=nil { t.Fatal(err) }
    columns,err:=rows.ColumnTypes();if err!=nil { t.Fatal(err) }
    tcMetadata(t,columns,b.PG)
    tcDrain(t,rows,step.Rows)
    tcMetadata(t,columns,b.PG)
    if err:=closeQuery();err!=nil { t.Fatal(err) }
    tcMetadata(t,columns,b.PG)
   } }
  }
  s:=tcProjection(b.PG,"table_schema='tc_scope' AND table_name=$1",nil)
  held:=dcHold(t,p,c,s.Query)
  for _,value:=range []any{"a","absent",nil,"parent"} {
   var want [][]any
   for _,row:=range tcSeedRows(database) { if row[5]==value { want=append(want,row) } }
   step:=tcProjection(b.PG,"table_schema='tc_scope' AND table_name=$1",want);step.Args=[]any{value}
   tcRoutes(t,p,c,r,held,step)
  }
  for _,star:=range []bool{false,true} {
   query:=s.Query
   if star { query=strings.Replace(query,strings.Join(tcColumns,","),"*",1) }
   for _,prepared:=range []bool{false,true} {
    var rows *sql.Rows
    var stmt *sql.Stmt
    var err error
    if prepared {
     stmt,err=c.PrepareContext(p.Ctx,query);if err!=nil { t.Fatal(err) }
     defer stmt.Close()
     rows,err=stmt.QueryContext(p.Ctx,nil)
    } else { rows,err=c.QueryContext(p.Ctx,query,nil) }
    if err!=nil { t.Fatal(err) }
    defer rows.Close()
    columns,err:=rows.ColumnTypes();if err!=nil { t.Fatal(err) }
    tcMetadata(t,columns,b.PG)
    if rows.Next()||rows.Err()!=nil { t.Fatal("NULL predicate returned row/error",rows.Err()) }
    tcMetadata(t,columns,b.PG)
    if err:=rows.Close();err!=nil { t.Fatal(err) }
    tcMetadata(t,columns,b.PG)
    if stmt!=nil { if err:=stmt.Close();err!=nil { t.Fatal(err) } }
   }
  }
 })
}

func TestReferenceInfoTableConstraintsDiagnostics(t *testing.T) {
 tcScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn,r *fixture.Recorder,database string) {
  run:=func(s fixture.Step) { fixture.RunStep(t,p.Ctx,c,r,s) }
  tcSeed(run)
  held:=dcHold(t,p,c,tcProjection(b.PG,"table_schema='tc_scope'",nil).Query)
  tcMissing(run)
  tcRoutes(t,p,c,r,held,tcProjection(b.PG,"table_schema='tc_scope'",tcSeedRows(database)))
 })
}
