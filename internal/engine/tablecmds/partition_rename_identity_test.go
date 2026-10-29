package tablecmds_test

import (
 "database/sql/driver"
 "maps"
 "reflect"
 "testing"

 "stuntgres/internal/engine"
 "stuntgres/internal/engine/catalog"
 "stuntgres/internal/engine/core"
 "stuntgres/internal/engine/enginetest"
 "stuntgres/internal/engine/ids"
 "stuntgres/internal/engine/lock"
)

func partitionRenameEngine(t *testing.T)*core.Engine{
 t.Helper();e:=engine.New()
 for _,q:=range []string{
  "CREATE TABLE pr_root(k INT,bucket INT,payload TEXT DEFAULT 'default',CONSTRAINT positive CHECK(k>0)) PARTITION BY RANGE((k+1))",
  "CREATE TABLE pr_mid PARTITION OF pr_root FOR VALUES FROM(1) TO(11) PARTITION BY LIST(bucket)",
  "CREATE TABLE pr_h0 PARTITION OF pr_mid FOR VALUES IN(0)",
  "CREATE TABLE pr_h1 PARTITION OF pr_mid FOR VALUES IN(1)",
  "CREATE INDEX pr_expression_index ON pr_h0((k+10))",
  "INSERT INTO pr_root(k,bucket,payload) VALUES(1,0,'one'),(2,1,'two')",
  "COMMENT ON COLUMN pr_root.k IS 'stable key'",
 }{enginetest.Run(t,e,q)};return e
}
var partitionRenameMembers=[]string{"pr_root","pr_mid","pr_h0","pr_h1"}

func TestPartitionRenameColumnIdentity(t *testing.T){
 e:=partitionRenameEngine(t)
 type retained struct{ref ids.ObjectRef;old *catalog.Relation}
 var members []retained
 for _,name:=range partitionRenameMembers{r:=enginetest.RoleTableRef(t,e,name);members=append(members,retained{r,e.Catalog.State.MustRelation(r.Address)})}
 oldState:=e.Catalog.State
 indexRef:=enginetest.RoleTableRef(t,e,"pr_expression_index");indexDescriptor:=e.Catalog.State.Get(indexRef.Address);indexRoots:=maps.Clone(e.Storage.IndexRoots)
 held:=enginetest.Run(t,e,"SELECT * FROM pr_root ORDER BY k")
 stores:=make(map[ids.ObjectAddress]any);backings:=make(map[ids.ObjectAddress]any)
 for _,m:=range members{store:=e.Storage.Relations[m.ref.Address];stores[m.ref.Address]=store;if store!=nil&&len(store.Versions)>0{backings[m.ref.Address]=&store.Versions[0].Values[0]}}
 enginetest.Run(t,e,"ALTER TABLE pr_root RENAME k TO renamed")
 for _,m:=range members{
  next:=e.Catalog.State.MustRelation(m.ref.Address)
  if next==m.old||next.Revision()!=m.old.Revision()+1||enginetest.RoleTableRef(t,e,m.old.Name())!=m.ref{t.Fatal("identity/revision",m.old,next)}
  for i,c:=range m.old.Columns{want:=c;if c.Name=="k"{want.Name="renamed"};if !reflect.DeepEqual(want,next.Columns[i]){t.Fatal("column identity changed",c,next.Columns[i])}}
  if next.Index["renamed"]!=m.old.Index["k"]{t.Fatal("local slot changed")};if _,ok:=next.Index["k"];ok{t.Fatal("old name retained")}
  normalized:=*next;normalized.Columns=m.old.Columns;normalized.Index=m.old.Index;normalized.ObjectRevision=m.old.ObjectRevision
  if !reflect.DeepEqual(normalized,*m.old)||next.Partition!=m.old.Partition||next.Membership!=m.old.Membership{t.Fatal("rename replaced bound definitions/authority/metadata",m.old,next)}
  if oldState.MustRelation(m.ref.Address)!=m.old||m.old.Columns[m.old.Index["k"]].Name!="k"{t.Fatal("retained catalog mutated")}
  store:=e.Storage.Relations[m.ref.Address];if stores[m.ref.Address]!=store{t.Fatal("heap changed")};if backing:=backings[m.ref.Address];backing!=nil&&backing!=&store.Versions[0].Values[0]{t.Fatal("row backing changed")}
 }
 if e.Catalog.State.Get(indexRef.Address)!=indexDescriptor||!e.Storage.IndexRootsCurrent(indexRoots){t.Fatal("rename replaced retained expression index definition/root")}
 if held.Rows.Schema()[0].Name!="k"||!reflect.DeepEqual(enginetest.Tuples(held),[][]driver.Value{{int64(1),int64(0),"one"},{int64(2),int64(1),"two"}}){t.Fatal("retained reader mutated")}
 t.Run("sparse_attached",func(t *testing.T){
  e:=engine.New();for _,q:=range []string{"CREATE TABLE a(k INT,payload TEXT) PARTITION BY RANGE(k)","CREATE TABLE c(obsolete INT,payload TEXT,k INT)","ALTER TABLE c DROP obsolete","ALTER TABLE a ATTACH PARTITION c FOR VALUES FROM(0) TO(10)","INSERT INTO a VALUES(1,'one')"}{enginetest.Run(t,e,q)}
  ref:=enginetest.RoleTableRef(t,e,"c");old:=e.Catalog.State.MustRelation(ref.Address);store:=e.Storage.Relations[ref.Address];backing:=&store.Versions[0].Values[0]
  enginetest.Run(t,e,"ALTER TABLE a RENAME k TO renamed");next:=e.Catalog.State.MustRelation(ref.Address)
  if next.Index["renamed"]!=1||next.Columns[1].OriginAttribute!=3||!reflect.DeepEqual(next.Attributes,old.Attributes)||next.Membership!=old.Membership||e.Storage.Relations[ref.Address]!=store||&store.Versions[0].Values[0]!=backing||old.Columns[1].Name!="k"{t.Fatal("attached local identity/layout lost",old,next)}
 })
}

func TestPartitionRenameColumnTransactionIdentity(t *testing.T){
 e:=partitionRenameEngine(t);s:=enginetest.StartSession(t,e,nil)
 old:=e.Catalog.State;originals:=make(map[ids.ObjectAddress]*catalog.Relation)
 for _,name:=range partitionRenameMembers{r:=enginetest.RoleTableRef(t,e,name);originals[r.Address]=old.MustRelation(r.Address)}
 enginetest.SessionSQL(t,s,"BEGIN");enginetest.SessionSQL(t,s,"SAVEPOINT before_rename");enginetest.SessionSQL(t,s,"ALTER TABLE pr_root RENAME k TO first")
 check:=func(name string){t.Helper();private:=s.Transactions.Catalog;if private==nil{t.Fatal("no transaction catalog")};for address,original:=range originals{
  next,ok:=private.Descriptors[address].(*catalog.Relation);if !ok||next.Columns[0].Name!=name||next.Revision()!=original.Revision()+1{t.Fatal("private hierarchy not published together",address,next)}
  if e.Catalog.State.MustRelation(address)!=original||original.Columns[0].Name!="k"{t.Fatal("uncommitted rename escaped")}
  entry:=e.Locks.Inspect(s.Transactions.Ticket).Resources[lock.Resource{ObjectRef:enginetest.RoleTableRef(t,e,original.Name())}]
  if entry==nil||entry.Holders[s.Transactions.Ticket].Mode!=lock.AccessExclusive{t.Fatal("transaction lost hierarchy AX grant",original.Name())}
 }}
 check("first");enginetest.SessionSQL(t,s,"SAVEPOINT after_first");enginetest.SessionSQL(t,s,"ALTER TABLE pr_root RENAME first TO second");enginetest.SessionSQL(t,s,"ROLLBACK TO after_first");check("first")
 enginetest.SessionSQL(t,s,"ROLLBACK TO before_rename")
 for address,original:=range originals{if e.Catalog.State.MustRelation(address)!=original{t.Fatal("savepoint changed committed identity")}}
 for _,name:=range partitionRenameMembers{r:=enginetest.RoleTableRef(t,e,name);if entry:=e.Locks.Inspect(s.Transactions.Ticket).Resources[lock.Resource{ObjectRef:r}];entry!=nil{if _,held:=entry.Holders[s.Transactions.Ticket];held{t.Fatal("savepoint retained post-mark grant",name)}}}
 enginetest.SessionSQL(t,s,"ALTER TABLE pr_root RENAME k TO renamed");check("renamed");enginetest.SessionSQL(t,s,"ROLLBACK")
 for address,original:=range originals{if e.Catalog.State.MustRelation(address)!=original{t.Fatal("rollback replaced committed descriptor")}}
 enginetest.SessionSQL(t,s,"BEGIN");enginetest.SessionSQL(t,s,"ALTER TABLE pr_root RENAME k TO renamed");enginetest.SessionSQL(t,s,"COMMIT")
 for address,original:=range originals{next:=e.Catalog.State.MustRelation(address);if next==original||next.Columns[0].Name!="renamed"||next.Revision()!=original.Revision()+1{t.Fatal("commit missed member",next)}}
}
