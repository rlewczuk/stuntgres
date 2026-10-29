package core_test

import (
 "context"
 "errors"
 "maps"
 "testing"
 "stuntgres/internal/engine"
 "stuntgres/internal/engine/authorization"
 "stuntgres/internal/engine/catalog"
 "stuntgres/internal/engine/core"
 "stuntgres/internal/engine/enginetest"
 "stuntgres/internal/engine/ids"
 "stuntgres/internal/engine/pgcatalog"
 "stuntgres/internal/sqltype"
)

func originInput(t *testing.T,e *core.Engine,body func(pgcatalog.Input,core.AuthorizationView)) {
 t.Helper()
 op,done,err:=enginetest.Operation(e,context.Background());if err!=nil { t.Fatal(err) };defer done()
 e.Mu.Lock();defer e.Mu.Unlock()
 auth,err:=e.AuthorizationLocked(op);if err!=nil { t.Fatal(err) }
 address:=ids.ObjectAddress{ClassID:ids.RelationClassID,ObjectID:6000}
 source,ok:=auth.Catalog.Get(address).(*catalog.Relation);if !ok { t.Fatal("missing origin relation") }
 body(pgcatalog.Input{Context:op.Context(),Owner:auth.Owner,Catalog:auth.Catalog,Roles:auth.Roles,Ref:auth.Ref(address),Source:source,Runtime:core.NewCatalogRuntime(core.CatalogRuntimeFields{Engine:e,Auth:auth})},auth)
}
func TestPGReplicationOriginCaptureOwnership(t *testing.T) {
 e,foreign:=engine.New(),engine.New()
 produce,ok:=pgcatalog.ProducerFor(6000);if !ok { t.Fatal("missing origin producer") }
 var other pgcatalog.Input;originInput(t,foreign,func(in pgcatalog.Input,_ core.AuthorizationView) { other=in })
 originInput(t,e,func(in pgcatalog.Input,auth core.AuthorizationView) {
  source:=in.Source
  if source.Name()!="pg_replication_origin" || source.RelationKind!=catalog.CatalogRelation || !catalog.SharedRelation(source) || !source.History.HasIndexes || len(source.Columns)!=2 { t.Fatal("origin descriptor",source) }
  row := in.Catalog.MustType(ids.ObjectAddress{ClassID:ids.TypeClassID,ObjectID:10086})
  array := in.Catalog.MustType(ids.ObjectAddress{ClassID:ids.TypeClassID,ObjectID:10085})
  if source.RowType!=10086 || row.OwningRelation!=source.Address() || array.OwningRelation!=source.Address() || row.Array!=10085 || array.SQLType.Element()!=row.SQLType || row.Owner!=source.Owner || array.Owner!=source.Owner { t.Fatal("origin row/array authority") }
  for i,c:=range source.Columns {
   oid:=sqltype.Oid;if i==1 { oid=sqltype.Text }
   typ,_:=in.Catalog.LookupType(oid)
   if c.Usage.Type!=typ || c.OriginTable!=6000 || c.OriginAttribute!=int16(i+1) || !c.NullabilityKnown || c.Nullable || c.Usage.Typmod!=-1 { t.Fatal("origin attribute authority",i,c) }
   collation:=uint32(0);if i==1 { collation=950 }
   if c.Usage.Collation.ID!=collation || source.ColumnMeta.CatalogAttributes[i].Dimensions!=0 || !source.ColumnMeta.CatalogAttributes[i].NotNull { t.Fatal("origin collation/dimensions",i,c) }
  }
  public:=false
  for _,entry:=range source.Security.ACL.Entries { if entry.Grantee==(authorization.ACLGrantee{Public:true}) && entry.Privileges==authorization.SelectPrivilege { public=true } }
  if !public { t.Fatal("missing public catalog SELECT") }
  descriptors,names,roles:=maps.Collect(in.Catalog.Descriptors()),maps.Collect(in.Catalog.State().Names()),e.RoleStore.Published
  rows,err:=produce(in);if err!=nil || rows==nil || len(rows)!=0 { t.Fatal(rows,err) }
  for _,mutate:=range []func(*pgcatalog.Input){
   func(b *pgcatalog.Input){b.Context=nil},func(b *pgcatalog.Input){b.Owner=other.Owner},
   func(b *pgcatalog.Input){b.Ref=other.Owner.Domain.Ref(in.Ref.Epoch,in.Ref.Address)},
   func(b *pgcatalog.Input){b.Ref.Epoch--},func(b *pgcatalog.Input){b.Ref.Address.SubID=1},
   func(b *pgcatalog.Input){b.Roles=other.Roles},func(b *pgcatalog.Input){b.Roles=authorization.Read{}},
   func(b *pgcatalog.Input){b.Runtime=nil},func(b *pgcatalog.Input){b.Runtime=other.Runtime},
   func(b *pgcatalog.Input){b.Runtime=core.NewCatalogRuntime(core.CatalogRuntimeFields{Auth:auth})},
   func(b *pgcatalog.Input){b.Source=nil},func(b *pgcatalog.Input){d:=*b.Source;b.Source=&d},
   func(b *pgcatalog.Input){b.Catalog=in.Catalog.WithState(catalog.CloneState(in.Catalog.State()))},
  } {
   bad:=in;mutate(&bad)
   rows,err:=produce(bad);subscriptionInvariant(t,rows==nil,err)
   wrapped,err:=core.CaptureCatalogSourceRows(bad,produce);subscriptionInvariant(t,wrapped==nil,err)
  }
  // A coherent unpublished capture is legitimate; a source with a foreign
  // owner-bound type remains forged even when paired with its private root.
  private:=in;private.Catalog=in.Catalog.WithState(catalog.CloneState(in.Catalog.State()))
  paired:=auth;paired.Recapture(private.Catalog)
  private.Roles=paired.Roles
  private.Runtime=core.NewCatalogRuntime(core.CatalogRuntimeFields{Engine:e,Auth:paired})
  if _,err:=produce(private);err!=nil { t.Fatal("coherent private capture rejected",err) }
  d:=*source;d.Columns=append([]catalog.Column(nil),source.Columns...);d.Columns[0].Usage.Type,_=other.Catalog.LookupType(sqltype.Oid)
  state:=catalog.CloneState(in.Catalog.State());state.SetDescriptor(d.Address(),&d)
  private.Catalog=in.Catalog.WithState(state);private.Source=&d
  paired.Recapture(private.Catalog);private.Roles=paired.Roles
  private.Runtime=core.NewCatalogRuntime(core.CatalogRuntimeFields{Engine:e,Auth:paired})
  rows,err=produce(private);subscriptionInvariant(t,rows==nil,err)
  ctx,cancel:=context.WithCancel(in.Context);cancel();private=in;private.Context=ctx
  rows,err=produce(private);if rows!=nil || !errors.Is(err,context.Canceled) { t.Fatal(rows,err) }
  if !maps.Equal(descriptors,maps.Collect(in.Catalog.Descriptors())) || !maps.Equal(names,maps.Collect(in.Catalog.State().Names())) || roles!=e.RoleStore.Published { t.Fatal("capture mutated roots") }
 })
}
func TestReplicationOriginRegistrationAuthority(t *testing.T) {
 e:=engine.New();read:=e.CatalogRead();state:=catalog.CloneState(read.State())
 first:=&catalog.Subscription{ObjectAddress:ids.ObjectAddress{ClassID:ids.SubscriptionClassID,ObjectID:4294967294},ObjectName:"a",ReplicationOriginID:7}
 second:=*first;second.ObjectAddress.ObjectID--;second.ObjectName="b"
 state.SetDescriptor(first.Address(),first);state.SetDescriptor(second.Address(),&second)
 if err:=catalog.ValidateReplicationOrigins(read.WithState(state));err==nil { t.Fatal("duplicate origin registration admitted") }
 second.ReplicationOriginID=8;state.SetDescriptor(second.Address(),&second)
 if err:=catalog.ValidateReplicationOrigins(read.WithState(state));err!=nil { t.Fatal(err) }
 if first.ReplicationOriginName()!="pg_4294967294" { t.Fatal("origin name did not use unsigned immutable subscription address") }
}
