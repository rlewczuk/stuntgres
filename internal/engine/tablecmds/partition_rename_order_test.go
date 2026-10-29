package tablecmds_test

import (
 "context"
 "errors"
 "testing"

 "stuntgres/internal/engine"
 "stuntgres/internal/engine/catalog"
 "stuntgres/internal/engine/enginetest"
 "stuntgres/internal/engine/ids"
 "stuntgres/internal/engine/lock"
 "stuntgres/internal/engine/partition"
 "stuntgres/internal/engine/table"
 "stuntgres/internal/sqlstate"
)

func partitionRenameDiagnostic(t *testing.T,err error,code,message string){t.Helper();want:=sqlstate.Error{Severity:"ERROR",SeverityUnlocalized:"ERROR",Code:code,Message:message};var got *sqlstate.Error;if !errors.As(err,&got)||*got!=want{t.Fatalf("diagnostic got %#v (%v), want %#v",got,err,want)}}

func TestPartitionRenameColumnEligibility(t *testing.T){
 for _,tc:=range []struct{q,code,message string}{
  {"ALTER TABLE ONLY pr_root RENAME missing TO payload","42P16",`inherited column "missing" must be renamed in child tables too`},
  {"ALTER TABLE ONLY pr_mid RENAME k TO payload","42P16",`inherited column "k" must be renamed in child tables too`},
  {"ALTER TABLE pr_mid RENAME k TO renamed","42P16",`cannot rename inherited column "k"`},
  {"ALTER TABLE pr_h0 RENAME k TO payload","42P16",`cannot rename inherited column "k"`},
  {"ALTER TABLE pr_h0 RENAME missing TO payload","42703",`column "missing" does not exist`},
 }{t.Run(tc.q,func(t *testing.T){e:=partitionRenameEngine(t);old:=e.Catalog.State;_,err:=enginetest.Execute(e,context.Background(),enginetest.Statement(t,tc.q),nil);partitionRenameDiagnostic(t,err,tc.code,tc.message);for _,name:=range partitionRenameMembers{r:=enginetest.RoleTableRef(t,e,name);if old.Get(r.Address)!=e.Catalog.State.Get(r.Address){t.Fatal("failed eligibility published",name)}}})}
}

func TestPartitionRenameColumnOrder(t *testing.T){
 e:=engine.New()
 for _,q:=range []string{
  "CREATE ROLE pr_actor LOGIN","CREATE ROLE pr_denied LOGIN","GRANT USAGE ON SCHEMA public TO pr_actor",
  "CREATE TABLE pr_root(k INT,payload TEXT) PARTITION BY RANGE(k)",
  "CREATE TABLE pr_mid PARTITION OF pr_root FOR VALUES FROM(0) TO(10) PARTITION BY RANGE(k)",
  "CREATE TABLE pr_deep PARTITION OF pr_mid FOR VALUES FROM(0) TO(10)",
  "CREATE TABLE pr_side PARTITION OF pr_root FOR VALUES FROM(10) TO(20)",
  "ALTER TABLE pr_root OWNER TO pr_actor","ALTER TABLE pr_mid OWNER TO pr_actor",
  "ALTER TABLE pr_deep OWNER TO pr_denied","ALTER TABLE pr_side OWNER TO pr_denied",
 }{enginetest.Run(t,e,q)}
 t.Run("selected_topology",func(t *testing.T){
  view:=e.CatalogRead()
  relation:=func(name string)*catalog.Relation{return view.MustRelation(enginetest.RoleTableRef(t,e,name).Address)}
  root,mid,deep,side:=relation("pr_root"),relation("pr_mid"),relation("pr_deep"),relation("pr_side")
  checkOrder:=func(got,want []*catalog.Relation){t.Helper();if len(got)!=len(want){t.Fatalf("closure length got %d want %d",len(got),len(want))};for i:=range want{if got[i]!=want[i]{t.Fatalf("closure[%d] got %s want %s",i,got[i].Name(),want[i].Name())}}}
  // Reverse and repeat root children to distinguish OID order and identity
  // deduplication from the selector's borrowed, immutable input order.
  allChildren:=func(parent *catalog.Relation)[]ids.ObjectAddress{
   if parent==root{return []ids.ObjectAddress{side.Address(),mid.Address(),mid.Address()}}
   return partition.EffectiveChildren(view,parent,nil)
  }
  checkOrder(table.PartitionRenameOrder(view,root,allChildren),[]*catalog.Relation{root,mid,side,deep})
  selectedChildren:=func(parent *catalog.Relation)[]ids.ObjectAddress{
   if parent==mid||parent==deep{t.Fatal("visited omitted subtree")}
   if parent==root{return []ids.ObjectAddress{side.Address()}}
   return partition.EffectiveChildren(view,parent,nil)
  }
  checkOrder(table.PartitionRenameOrder(view,root,selectedChildren),[]*catalog.Relation{root,side})
  partitionRenameDiagnostic(t,table.PartitionRenameOnly(selectedChildren(root),"k"),"42P16",`inherited column "k" must be renamed in child tables too`)
  pendingOnly:=func(*catalog.Relation)[]ids.ObjectAddress{return nil}
  checkOrder(table.PartitionRenameOrder(view,root,pendingOnly),[]*catalog.Relation{root})
  if err:=table.PartitionRenameOnly(pendingOnly(root),"k");err!=nil{t.Fatal("ONLY with no selected children",err)}
  if len(root.Partition.Children)!=2||len(mid.Partition.Children)!=1||deep.Membership.Parent!=mid.Address(){t.Fatal("selection changed canonical membership")}
 })
 old:=e.Catalog.State;op,done:=enginetest.RoleOperation(t,e,"pr_actor");defer done()
 // The deep leaf has a lower OID than the side branch. BFS must deny side first.
 _,err:=enginetest.ExecuteFrame(e,op,enginetest.Statement(t,"ALTER TABLE pr_root RENAME k TO renamed"),nil)
 partitionRenameDiagnostic(t,err,"42501","must be owner of table pr_side")
 for _,name:=range []string{"pr_root","pr_mid","pr_deep","pr_side"}{r:=enginetest.RoleTableRef(t,e,name);if old.Get(r.Address)!=e.Catalog.State.Get(r.Address){t.Fatal("ordered validation partially published",name)}}
}

func TestPartitionRenameColumnAuthority(t *testing.T){
 e:=partitionRenameEngine(t);for _,q:=range []string{"CREATE ROLE pr_actor LOGIN","CREATE ROLE pr_denied LOGIN","GRANT USAGE ON SCHEMA public TO pr_actor","ALTER TABLE pr_root OWNER TO pr_actor","ALTER TABLE pr_mid OWNER TO pr_denied"}{enginetest.Run(t,e,q)}
 before:=e.Catalog.State;op,done:=enginetest.RoleOperation(t,e,"pr_actor");defer done()
 _,err:=enginetest.ExecuteFrame(e,op,enginetest.Statement(t,"ALTER TABLE pr_root RENAME missing TO payload"),nil)
 partitionRenameDiagnostic(t,err,"42501","must be owner of table pr_mid")
 for _,name:=range partitionRenameMembers{ref:=enginetest.RoleTableRef(t,e,name);if e.Catalog.State.Get(ref.Address)!=before.Get(ref.Address){t.Fatal("authority failure changed member")}}
}

func TestPartitionRenameColumnLockClosure(t *testing.T){
 e:=partitionRenameEngine(t);holder:=enginetest.StartSession(t,e,nil);worker:=enginetest.StartSession(t,e,nil)
 ref:=enginetest.TransactionBegin(t,holder);defer enginetest.TransactionEnd(t,holder,ref,false)
 target:=enginetest.RoleTableRef(t,e,"pr_h1")
 if err:=e.Locks.Acquire(context.Background(),holder.Transactions.Ticket,lock.Resource{ObjectRef:target},lock.AccessShare);err!=nil{t.Fatal(err)}
 originals:=make(map[ids.ObjectAddress]*catalog.Relation)
 for _,name:=range partitionRenameMembers{r:=enginetest.RoleTableRef(t,e,name);originals[r.Address]=e.Catalog.State.MustRelation(r.Address)}
 ctx,cancel:=context.WithCancel(context.Background());defer cancel()
 done:=enginetest.LockAsync(e,worker,ctx,"ALTER TABLE pr_root RENAME k TO renamed",false);joined:=false
 defer func(){cancel();if !joined{enginetest.LockAwait(t,done,context.Canceled)}}()
 enginetest.LockPending(t,e,1)
 state:=e.Locks.Inspect()
 for _,name:=range []string{"pr_root","pr_mid","pr_h0"}{r:=enginetest.RoleTableRef(t,e,name);entry:=state.Resources[lock.Resource{ObjectRef:r}];found:=false;if entry!=nil{for _,hold:=range entry.Holders{if hold.Mode==lock.AccessExclusive{found=true}}};if !found{t.Fatal("closure did not lock earlier BFS member",name)}}
 e.Mu.Lock();unchanged:=true;for address,original:=range originals{if e.Catalog.State.MustRelation(address)!=original{unchanged=false}};e.Mu.Unlock();if !unchanged{t.Fatal("candidate published before full closure")}
 cancel();enginetest.LockAwait(t,done,context.Canceled);joined=true
 for address,original:=range originals{if e.Catalog.State.MustRelation(address)!=original{t.Fatal("cancel changed descriptor")}}
}
