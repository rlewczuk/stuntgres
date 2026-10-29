package core

import (
 "context"
 "errors"
 "testing"
 "stuntgres/internal/engine/catalog"
 "stuntgres/internal/engine/ids"
 "stuntgres/internal/engine/session"
 "stuntgres/internal/sqlstate"
)

func TestReplicationOriginAllocation(t *testing.T) {
 var b replicationOriginOccupancy
 id,err:=b.allocate(t.Context());if err!=nil || id!=1 { t.Fatal(id,err) }
 b.add(3)
 id,err=b.allocate(t.Context());if err!=nil || id!=2 { t.Fatal(id,err) }
 b[0] &^= uint64(1)<<1
 id,err=b.allocate(t.Context());if err!=nil || id!=1 { t.Fatal("not first-free reuse",id,err) }
 for i:=ids.OID(1);i<65535;i++ { b.add(i) }
 before:=b
 id,err=b.allocate(t.Context())
 var diagnostic *sqlstate.Error
 if id!=0 || !errors.As(err,&diagnostic) || diagnostic.Code!="54000" || diagnostic.Message!="could not find free replication origin ID" || b!=before { t.Fatal("exhaustion",id,err) }
 ctx,cancel:=context.WithCancel(t.Context());cancel()
 if id,err=b.allocate(ctx);id!=0 || !errors.Is(err,context.Canceled) || b!=before { t.Fatal("canceled allocation mutated occupancy",id,err) }
}

func TestReplicationOriginReservationLifecycle(t *testing.T) {
 e:=&Engine{Cluster:&DatabaseCluster{replicationOrigins:make(map[ids.OID]replicationOriginReservation)}}
 domain:=&ids.Domain{}
 owner:=&session.TransactionOwner{}
 owner.Ref=domain.Tx(domain.Session(1),1,1,owner,1)
 address:=ids.ObjectAddress{ClassID:ids.SubscriptionClassID,ObjectID:70000}
 d:=&catalog.Subscription{ObjectAddress:address,ReplicationOriginID:1}
 stage:=&CatalogStage{Owner:e,replicationOrigins:[]ids.OID{1}}
 e.Cluster.replicationOrigins[1]=replicationOriginReservation{Ref:owner.Ref,Address:address,Stage:stage}
 e.transferReplicationOriginsLocked(stage,owner.Ref)
 if stage.replicationOrigins!=nil || e.Cluster.replicationOrigins[1].Stage!=nil { t.Fatal("stage did not transfer") }
 journal:=session.NewJournal();journal.Descriptors[address]=d
 owner.Catalog=journal
 e.reconcileReplicationOriginsLocked(owner)
 if len(e.Cluster.replicationOrigins)!=1 { t.Fatal("current journal lost exclusion") }
 // Keep the actual ancestor journal, not a reconstruction after rollback.
 mark:=session.SavepointMark{Catalog:journal}
 owner.Savepoints=[]session.SavepointMark{mark}
 owner.Catalog=journal.Clone();owner.Catalog.Descriptors[address]=nil
 e.reconcileReplicationOriginsLocked(owner)
 if len(e.Cluster.replicationOrigins)!=1 { t.Fatal("revivable create/drop lost exclusion") }
 owner.Catalog=mark.Catalog;owner.Savepoints=nil
 e.reconcileReplicationOriginsLocked(owner)
 if len(e.Cluster.replicationOrigins)!=1 || journal.Descriptors[address]!=d { t.Fatal("ancestor journal changed") }
 owner.Catalog=journal.Clone();owner.Catalog.Descriptors[address]=nil
 // A procedural mark retains the actual outer statement journal separately.
 owner.Savepoints=[]session.SavepointMark{{StatementCatalog:journal}}
 e.reconcileReplicationOriginsLocked(owner)
 if len(e.Cluster.replicationOrigins)!=1 { t.Fatal("exception mark lost enclosing statement") }
 owner.Savepoints=nil
 e.reconcileStatementReplicationOriginsLocked(owner,journal)
 if len(e.Cluster.replicationOrigins)!=1 { t.Fatal("exception release lost live statement") }
 e.reconcileReplicationOriginsLocked(owner)
 if len(e.Cluster.replicationOrigins)!=0 { t.Fatal("unreachable registration retained") }
 // Stale stage cleanup cannot remove a newer entry reusing its ID.
 stage.replicationOrigins=[]ids.OID{1}
 fresh:=&CatalogStage{Owner:e,replicationOrigins:[]ids.OID{1}}
 nextRef:=domain.Tx(domain.Session(2),1,1,nil,2)
 e.Cluster.replicationOrigins[1]=replicationOriginReservation{Ref:nextRef,Address:address,Stage:fresh}
 e.releaseStageReplicationOriginsLocked(stage)
 if e.Cluster.replicationOrigins[1].Stage!=fresh { t.Fatal("stale cleanup removed new owner") }
 e.transferReplicationOriginsLocked(fresh,nextRef)
 e.prepareReplicationOriginsLocked(nextRef,true)
 e.releaseReplicationOriginsLocked(nextRef)
 if len(e.Cluster.replicationOrigins)!=1 { t.Fatal("session disposal lost prepared exclusion") }
 e.prepareReplicationOriginsLocked(nextRef,false)
 e.releaseReplicationOriginsLocked(nextRef)
 if len(e.Cluster.replicationOrigins)!=0 { t.Fatal("prepared settlement leaked exclusion") }
}
