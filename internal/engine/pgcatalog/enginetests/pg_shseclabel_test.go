package enginetests_test

import (
 "context"
 "errors"
 "testing"
 "stuntgres/internal/engine/authorization"
 "stuntgres/internal/engine/catalog"
 "stuntgres/internal/engine/core"
 "stuntgres/internal/engine/ids"
 "stuntgres/internal/engine/pgcatalog"
 "stuntgres/internal/sqltype"
)

func TestPGShseclabelProducerAuthority(t *testing.T) {
 f := newDefaultUnitFixture(t)
 view := f.e.CatalogRead()
 in := policyUnitInput(t,f,view)
 in.Source = view.MustRelation(ids.ObjectAddress{ClassID:ids.RelationClassID,ObjectID:pgcatalog.ShseclabelOID})
 in.Ref = view.Owner().Domain.Ref(view.Epoch(),in.Source.Address())
 acl:=in.Source.Security.ACL
 if !acl.Explicit || len(acl.Entries)!=2{t.Fatal("shared label bootstrap ACL",acl)}
 var ownerEntry,publicEntry bool
 for _,entry:=range acl.Entries {
  if entry.Grantor!=in.Source.Owner || entry.GrantOptions!=0{t.Fatal("shared label grant authority",entry)}
  if entry.Grantee.Public {
   publicEntry=entry.Privileges==authorization.SelectPrivilege
  } else {
   ownerEntry=entry.Grantee.Role==in.Source.Owner && entry.Privileges==authorization.TableGrantPrivileges
  }
 }
 if !ownerEntry || !publicEntry{t.Fatal("shared label owner/PUBLIC privileges",acl)}
 produce := registeredCatalogProducer(pgcatalog.ShseclabelOID)
 rows, err := core.CaptureCatalogSourceRows(in,produce)
 if err!=nil || rows==nil || rows.Anonymous==nil || len(rows.Anonymous)!=0 { t.Fatal(rows,err) }
 reject := func(bad pgcatalog.Input) { t.Helper(); rows,err:=produce(bad);if rows!=nil || err==nil {t.Fatal("accepted malformed input",rows,err)} }
 bad:=in;bad.Owner=nil;reject(bad)
 bad=in;bad.Catalog=catalog.Read{};reject(bad)
 bad=in;bad.Roles=authorization.Read{};reject(bad)
 bad=in;bad.Ref.Epoch++;reject(bad)
 bad=in;bad.Ref=new(ids.Domain).Ref(in.Ref.Epoch,in.Ref.Address);reject(bad)
 bad=in;bad.Ref.Address.ObjectID++;reject(bad)
 bad=in;clone:=*in.Source;bad.Source=&clone;reject(bad)
 for _, shape:=range []string{"width","name","type","nullable","unknown","typmod","dimensions","collation","kind","foreign_type"} {
  t.Run(shape,func(t *testing.T){
   state:=catalog.CloneState(view.State());source:=*in.Source;source.Columns=append([]catalog.Column(nil),source.Columns...)
   switch shape {
   case "width":source.Columns=source.Columns[:3]
   case "name":source.Columns[0].Name="objsubid"
   case "type":source.Columns[0].Usage.Type,_=view.LookupType(sqltype.Int4)
   case "nullable":source.Columns[0].Nullable=true
   case "unknown":source.Columns[0].NullabilityKnown=false
   case "typmod":source.Columns[0].Usage.Typmod=1
   case "dimensions":source.Columns[0].DeclaredDimensions=1
   case "collation":source.Columns[2].Usage.Collation.ID=0
   case "kind":source.RelationKind=catalog.ViewRelation
   case "foreign_type":other:=newDefaultUnitFixture(t);source.Columns[0].Usage.Type,_=other.e.CatalogRead().LookupType(sqltype.Oid)
   }
   state.SetDescriptor(source.Address(),&source);bad:=in;bad.Source=&source;bad.Catalog=catalog.NewRead(in.Owner,state);reject(bad)
  })
 }
 ctx,cancel:=context.WithCancel(t.Context());cancel();bad=in;bad.Context=ctx
 if rows,err:=produce(bad);rows!=nil || !errors.Is(err,context.Canceled){t.Fatal(rows,err)}
}
