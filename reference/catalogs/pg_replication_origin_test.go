package reference_test

import (
 "database/sql"
 "fmt"
 "reflect"
 "stuntgres/reference/fixture"
 "testing"
)

func originCreate(t *testing.T,p *fixture.Pair,b fixture.Backend,e fixture.Executor,name string,prepared bool) {
 t.Helper()
 subscriptionExec(t,p,b,e,"CREATE SUBSCRIPTION "+name+" CONNECTION '' PUBLICATION remote_origin WITH (connect=false,slot_name=NONE)",prepared)
}
func originQuery(name string) string { return "SELECT o.roident,o.roname FROM pg_catalog.pg_replication_origin o JOIN pg_catalog.pg_subscription s ON o.roname='pg_' || s.oid::text WHERE s.subname='"+name+"'" }
func originIdentity(t *testing.T,p *fixture.Pair,e fixture.Executor,name string) (int64,string) {
 t.Helper()
 rows,err := e.QueryContext(p.Ctx,originQuery(name)); if err != nil { t.Fatal(err) }; defer rows.Close()
 var id int64; var canonical string
 if !rows.Next() { t.Fatal("missing registered origin",name,rows.Err()) }
 if err := rows.Scan(&id,&canonical); err != nil { t.Fatal(err) }
 if id <= 0 || id >= 65535 || canonical == "" { t.Fatal("invalid registered identity",id,canonical) }
 if rows.Next() || rows.Err() != nil { t.Fatal("origin cardinality",rows.Err()) }
 oid := subscriptionNumber(t,p,e,"SELECT oid::bigint FROM pg_subscription WHERE subname='"+name+"'")
 if canonical != fmt.Sprint("pg_",oid) { t.Fatal("origin not linked to subscription",canonical,oid) }
 return id,canonical
}
func originCheck(t *testing.T,p *fixture.Pair,b fixture.Backend,e fixture.Executor,name string,id int64,canonical string) {
 t.Helper()
 var want [][]any
 if id != 0 { want = [][]any{{id,canonical}} }
 for _,prepared := range []bool{false,true} {
  step := fixture.Select(originQuery(name),[]string{"roident","roname"},[]string{"OID","TEXT"},want)
  step.Prepared = prepared
  fixture.OidReferenceRows(t,p.Ctx,b,e,step)
 }
}
func originBag(t *testing.T,p *fixture.Pair,e fixture.Executor) map[int64]string {
 t.Helper()
 rows,err := e.QueryContext(p.Ctx,"SELECT roident,roname FROM pg_replication_origin ORDER BY roident"); if err != nil { t.Fatal(err) }; defer rows.Close()
 result := make(map[int64]string)
 for rows.Next() { var id int64; var name string; if err := rows.Scan(&id,&name); err != nil { t.Fatal(err) }; if id <= 0 || id >= 65535 || result[id] != "" { t.Fatal("invalid origin bag",id,name) }; result[id]=name }
 if err := rows.Err(); err != nil { t.Fatal(err) }; return result
}
func originFirstFree(bag map[int64]string) int64 { for id:=int64(1);id<65535;id++ { if _,ok:=bag[id]; !ok { return id } }; return 0 }
func originEmpty(pg bool,query string) fixture.Step {
 step := fixture.Select(query,[]string{"roident","roname"},[]string{"OID","TEXT"},nil)
 step.CheckMetadata = func(t *testing.T,rows *sql.Rows) {
  columns,err := rows.ColumnTypes(); if err != nil || len(columns)!=2 { t.Fatal(columns,err) }
  for i,c := range columns { nullable,known:=c.Nullable(); if known==pg || !pg && nullable { t.Fatal("origin nullability",i,nullable,known) } }
 }
 return step
}
func TestReferenceReplicationOriginCatalog(t *testing.T) {
 fixture.RoleReferenceCleanup(t,[]string{"origin_public_reader"})
 p:=subscriptionScenario(t,"origin_catalog_shape")
 for _,b:=range p.Backends() { t.Run(b.Name,func(t *testing.T) {
  for _,source:=range []string{"pg_catalog.pg_replication_origin","pg_replication_origin"} {
   step:=originEmpty(b.PG,"SELECT * FROM "+source+" WHERE FALSE")
   subscriptionRun(t,p,b,b.DB,step); subscriptionRun(t,p,b,b.DB,fixture.Prepared(step))
  }
  fixture.OidReferenceRows(t,p.Ctx,b,b.DB,fixture.Select("SELECT oid,relname,relnatts,relkind,relisshared,relhasindex,reltablespace FROM pg_class WHERE oid=6000",[]string{"oid","relname","relnatts","relkind","relisshared","relhasindex","reltablespace"},[]string{"OID","NAME","INT2","CHAR","BOOL","BOOL","OID"},[][]any{{int64(6000),"pg_replication_origin",int64(2),"r",true,true,int64(1664)}}))
  fixture.OidReferenceRows(t,p.Ctx,b,b.DB,fixture.Select("SELECT attnum,attname,atttypid,attnotnull,atttypmod,attndims,attcollation FROM pg_attribute WHERE attrelid=6000 AND attnum>0 ORDER BY attnum",[]string{"attnum","attname","atttypid","attnotnull","atttypmod","attndims","attcollation"},[]string{"INT2","NAME","OID","BOOL","INT4","INT2","OID"},[][]any{{int64(1),"roident",int64(26),true,int64(-1),int64(0),int64(0)},{int64(2),"roname",int64(25),true,int64(-1),int64(0),int64(950)}}))
  originCreate(t,p,b,b.DB,"origin_catalog_shape",false)
  id,name:=originIdentity(t,p,b.DB,"origin_catalog_shape")
  admin,reader:=fixture.LockConnections(t,p.Ctx,b.DB)
  subscriptionExec(t,p,b,admin,"CREATE ROLE origin_public_reader",false)
  subscriptionExec(t,p,b,reader,"SET ROLE origin_public_reader",false)
  defer reader.ExecContext(p.Ctx,"RESET ROLE")
  query:=fmt.Sprintf("SELECT * FROM pg_replication_origin WHERE roname='%s'",name)
  stmt,err:=reader.PrepareContext(p.Ctx,query); if err != nil { t.Fatal(err) }; defer stmt.Close()
  held:=fixture.HeldStatement{Executor:reader,Statement:stmt,SQL:query}
  check:=func(e fixture.Executor) { fixture.OidReferenceRows(t,p.Ctx,b,e,fixture.Select(query,[]string{"roident","roname"},[]string{"OID","TEXT"},[][]any{{id,name}})) }
  check(reader); check(held)
  // Admission is current even on the same held prepared definition.
  defer admin.ExecContext(p.Ctx,"GRANT SELECT ON pg_catalog.pg_replication_origin TO PUBLIC")
  subscriptionExec(t,p,b,admin,"REVOKE SELECT ON pg_catalog.pg_replication_origin FROM PUBLIC",false)
  for _,e:=range []fixture.Executor{reader,held} {
   denied:=fixture.ErrorDiagnostic(query,fixture.ControlsReferenceDiagnostic("42501","permission denied for table pg_replication_origin"))
   denied.SelectRows=true;denied.ErrorStage="execute"
   subscriptionRun(t,p,b,e,denied)
  }
  subscriptionExec(t,p,b,admin,"GRANT SELECT ON pg_catalog.pg_replication_origin TO PUBLIC",false)
  check(held)
  subscriptionExec(t,p,b,reader,"RESET ROLE",false)
  subscriptionExec(t,p,b,admin,"DROP SUBSCRIPTION origin_catalog_shape",false)
  subscriptionExec(t,p,b,admin,"DROP ROLE origin_public_reader",false)
 }) }
}
func TestReferenceReplicationOriginLifecycle(t *testing.T) {
 fixture.RoleReferenceCleanup(t,[]string{"origin_lifetime_owner","origin_lifetime_target"})
 p:=subscriptionScenario(t,"origin_catalog_a","origin_catalog_b","origin_catalog_renamed","origin_default_slot")
 for _,b:=range p.Backends() { t.Run(b.Name,func(t *testing.T) {
  initial:=originBag(t,p,b.DB)
  originCreate(t,p,b,b.DB,"origin_catalog_a",false)
  id,name:=originIdentity(t,p,b.DB,"origin_catalog_a")
  if id!=originFirstFree(initial) { t.Fatal("not first free",id,initial) }
  originCreate(t,p,b,b.DB,"origin_catalog_b",true)
  second,secondName:=originIdentity(t,p,b.DB,"origin_catalog_b")
  if second==id || secondName==name { t.Fatal("duplicate identity") }
  for _,q:=range []string{"ALTER SUBSCRIPTION origin_catalog_a RENAME TO origin_catalog_renamed","ALTER SUBSCRIPTION origin_catalog_renamed SET (binary=true,origin=none)","ALTER SUBSCRIPTION origin_catalog_renamed SET PUBLICATION changed_remote WITH (refresh=false)","ALTER SUBSCRIPTION origin_catalog_renamed CONNECTION 'application_name=changed'","ALTER SUBSCRIPTION origin_catalog_renamed SKIP (lsn='A/1234')","CREATE ROLE origin_lifetime_owner","CREATE ROLE origin_lifetime_target","ALTER SUBSCRIPTION origin_catalog_renamed OWNER TO origin_lifetime_owner","REASSIGN OWNED BY origin_lifetime_owner TO origin_lifetime_target"} {
   subscriptionExec(t,p,b,b.DB,q,true)
   originCheck(t,p,b,b.DB,"origin_catalog_renamed",id,name)
  }
  // Shared subscriptions are not DROP OWNED targets. Their registrations
  // survive both rollback and committed DROP OWNED; detached DROP removes them.
  subscriptionExec(t,p,b,b.DB,"ALTER SUBSCRIPTION origin_catalog_renamed OWNER TO origin_lifetime_target",false)
  originCheck(t,p,b,b.DB,"origin_catalog_renamed",id,name)
  tx,err:=b.DB.BeginTx(p.Ctx,nil);if err!=nil { t.Fatal(err) };defer tx.Rollback()
  subscriptionExec(t,p,b,tx,"DROP OWNED BY origin_lifetime_target",false)
  originCheck(t,p,b,tx,"origin_catalog_renamed",id,name)
  if err:=tx.Rollback();err!=nil { t.Fatal(err) }
  originCheck(t,p,b,b.DB,"origin_catalog_renamed",id,name)
  subscriptionExec(t,p,b,b.DB,"DROP OWNED BY origin_lifetime_target",false)
  originCheck(t,p,b,b.DB,"origin_catalog_renamed",id,name)
  dropping,err:=b.DB.BeginTx(p.Ctx,nil);if err!=nil { t.Fatal(err) };defer dropping.Rollback()
  subscriptionExec(t,p,b,dropping,"DROP SUBSCRIPTION origin_catalog_renamed",false)
  originCheck(t,p,b,dropping,"origin_catalog_renamed",0,"")
  if err:=dropping.Rollback();err!=nil { t.Fatal(err) }
  originCheck(t,p,b,b.DB,"origin_catalog_renamed",id,name)
  subscriptionExec(t,p,b,b.DB,"DROP SUBSCRIPTION origin_catalog_renamed",false)
  originCheck(t,p,b,b.DB,"origin_catalog_renamed",0,"")
  originCheck(t,p,b,b.DB,"origin_catalog_b",second,secondName)
  free:=originFirstFree(originBag(t,p,b.DB))
  originCreate(t,p,b,b.DB,"origin_catalog_a",true)
  reused,_:=originIdentity(t,p,b.DB,"origin_catalog_a"); if reused!=free { t.Fatal("committed drop did not free lowest identity",reused,free) }
  subscriptionExec(t,p,b,b.DB,"DROP SUBSCRIPTION origin_catalog_a",false)
  subscriptionExec(t,p,b,b.DB,"DROP SUBSCRIPTION origin_catalog_b",false)
  subscriptionExec(t,p,b,b.DB,"DROP ROLE origin_lifetime_owner",false)
  subscriptionExec(t,p,b,b.DB,"DROP ROLE origin_lifetime_target",false)
  subscriptionExec(t,p,b,b.DB,"CREATE SUBSCRIPTION origin_default_slot CONNECTION '' PUBLICATION p WITH (connect=false)",false)
  defaultID,defaultName:=originIdentity(t,p,b.DB,"origin_default_slot")
  blocked,err:=b.DB.BeginTx(p.Ctx,nil);if err!=nil { t.Fatal(err) };defer blocked.Rollback()
  subscriptionError(t,p,b,blocked,"DROP SUBSCRIPTION origin_default_slot","25001","DROP SUBSCRIPTION cannot run inside a transaction block",false)
  if err:=blocked.Rollback();err!=nil { t.Fatal(err) }
  if !b.PG { subscriptionError(t,p,b,b.DB,"DROP SUBSCRIPTION origin_default_slot","0A000","subscription replication slot removal is not available",false) }
  originCheck(t,p,b,b.DB,"origin_default_slot",defaultID,defaultName)
  subscriptionDetach(t,p,b,b.DB,"origin_default_slot")
  if !reflect.DeepEqual(initial,originBag(t,p,b.DB)) { t.Fatal("orphan origin after generic deletion") }
 }) }
}
func TestReferenceReplicationOriginErrors(t *testing.T) {
 fixture.RoleReferenceCleanup(t,[]string{"origin_denied_actor"})
 p:=subscriptionScenario(t,"origin_errors","origin_denied","origin_bad")
 for _,b:=range p.Backends() { t.Run(b.Name,func(t *testing.T) {
  originCreate(t,p,b,b.DB,"origin_errors",false)
  before:=originBag(t,p,b.DB)
  admin,actor:=fixture.LockConnections(t,p.Ctx,b.DB)
  subscriptionExec(t,p,b,admin,"CREATE ROLE origin_denied_actor",false)
  subscriptionExec(t,p,b,actor,"SET ROLE origin_denied_actor",false)
  defer actor.ExecContext(p.Ctx,"RESET ROLE")
  denied:=fixture.ControlsReferenceDiagnostic("42501","permission denied to create subscription")
  denied.Detail=`Only roles with privileges of the "pg_create_subscription" role may create subscriptions.`
  for _,prepared:=range []bool{false,true} {
   step:=fixture.ErrorDiagnostic("CREATE SUBSCRIPTION origin_denied CONNECTION '' PUBLICATION p WITH (connect=false,slot_name=NONE)",denied)
   step.Prepared=prepared;step.ErrorStage="execute"
   subscriptionRun(t,p,b,actor,step)
   subscriptionError(t,p,b,actor,"DROP SUBSCRIPTION origin_errors","42501","must be owner of subscription origin_errors",prepared)
  }
  subscriptionExec(t,p,b,actor,"RESET ROLE",false)
  subscriptionExec(t,p,b,admin,"DROP ROLE origin_denied_actor",false)
  if !reflect.DeepEqual(before,originBag(t,p,b.DB)) { t.Fatal("privilege failure changed origins") }
  for _,prepared:=range []bool{false,true} {
   subscriptionError(t,p,b,b.DB,"CREATE SUBSCRIPTION origin_errors CONNECTION '' PUBLICATION p WITH (connect=false,slot_name=NONE)","42710",`subscription "origin_errors" already exists`,prepared)
   subscriptionError(t,p,b,b.DB,"DROP SUBSCRIPTION origin_missing","42704",`subscription "origin_missing" does not exist`,prepared)
   subscriptionError(t,p,b,b.DB,"CREATE SUBSCRIPTION origin_bad CONNECTION 'testconn' PUBLICATION p WITH (connect=false)","42601","invalid connection string syntax: missing \"=\" after \"testconn\" in connection info string\n",prepared)
   subscriptionError(t,p,b,b.DB,"CREATE SUBSCRIPTION origin_bad CONNECTION '' PUBLICATION p WITH (connect=false,unknown=true)","42601",`unrecognized subscription parameter: "unknown"`,prepared)
   subscriptionError(t,p,b,b.DB,"ALTER SUBSCRIPTION origin_errors SET (binary='1')","42601","binary requires a Boolean value",prepared)
   if !reflect.DeepEqual(before,originBag(t,p,b.DB)) { t.Fatal("failed command changed registration") }
  }
  subscriptionExec(t,p,b,b.DB,"DROP SUBSCRIPTION origin_errors",false)
 }) }
}
