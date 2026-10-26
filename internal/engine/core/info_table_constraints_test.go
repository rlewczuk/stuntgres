package core_test

import (
 "context"
 "errors"
 "reflect"
 "testing"

 "stuntgres/internal/engine"
 "stuntgres/internal/engine/authorization"
 "stuntgres/internal/engine/catalog"
 "stuntgres/internal/engine/core"
 "stuntgres/internal/engine/ids"
 "stuntgres/internal/engine/pgcatalog"
 "stuntgres/internal/sqltype"
)

type tcUnitFixture struct {
 f defaultUnitFixture
 state *catalog.State
 table *catalog.Relation
}

func newTCUnitFixture(t *testing.T) tcUnitFixture {
 t.Helper()
 f:=newDefaultUnitFixture(t)
 state:=catalog.CloneState(f.e.Catalog.State)
 integer,_:=f.e.Catalog.Read(state).LookupType(sqltype.Int4)
 table:=&catalog.Relation{
  ObjectAddress:ids.ObjectAddress{ClassID:ids.RelationClassID,ObjectID:22500},ObjectNamespace:f.schema,ObjectName:"actual",ObjectRevision:1,Owner:f.owner,RelationKind:catalog.TableRelation,
  Columns:[]catalog.Column{{Name:"a",Usage:integer.Usage()},{Name:"b",Usage:integer.Usage()}},Attributes:[]int32{1,2},
  Security:catalog.RelationSecurity{ACL:authorization.ACLValue{Explicit:true}},
  Constraints:catalog.RelationConstraints{
   NotNull:[]catalog.NotNullConstraint{{OID:22501,Name:"nn",Attribute:1}},
   Check:[]catalog.CheckConstraint{{OID:22502,Name:"same",NotValid:true}},
   Unique:[]catalog.UniqueConstraint{{OID:22503,Name:"primary",Primary:true},{OID:22504,Name:"unique"},{OID:22505,Name:"not_distinct",NullsNotDistinct:true}},
   ForeignKeys:[]catalog.ForeignKeyConstraint{{OID:22506,Name:"foreign",NotValid:true}},
   Exclusion:[]catalog.ExclusionConstraint{{OID:22507,Name:"excluded"}},
  },
 }
 state.SetDescriptor(table.Address(),table)
 physical:=*table;physical.ObjectAddress.ObjectID=22520;physical.ObjectName="physical_only"
 physical.Constraints=catalog.RelationConstraints{PhysicalNotNull:map[int32]catalog.PhysicalNotNullProof{1:{}}}
 state.SetDescriptor(physical.Address(),&physical)
 return tcUnitFixture{f,state,table}
}

func (f tcUnitFixture) input(t *testing.T,state *catalog.State,actor authorization.PrincipalRef) pgcatalog.Input {
 t.Helper()
 view:=f.f.e.Catalog.Read(state)
 auth:=dtpUnitAuthority(t,f.f,view);auth.State.CurrentUser=actor
 source:=view.MustRelation(ids.ObjectAddress{ClassID:ids.RelationClassID,ObjectID:pgcatalog.InfoTableConstraintsOID})
 return pgcatalog.Input{Context:t.Context(),Catalog:view,Roles:auth.Roles,Owner:view.Owner(),Ref:view.Owner().Domain.Ref(view.Epoch(),source.Address()),Source:source,Runtime:core.NewCatalogRuntime(core.CatalogRuntimeFields{Auth:auth,Database:"captured_database"})}
}

func tcUnitExpected() map[[11]string]int {
 want:=map[[11]string]int{}
 for _,r:=range [][3]string{{"nn","CHECK","<NULL>"},{"same","CHECK","<NULL>"},{"primary","PRIMARY KEY","<NULL>"},{"unique","UNIQUE","YES"},{"not_distinct","UNIQUE","NO"},{"foreign","FOREIGN KEY","<NULL>"}} {
  want[[11]string{"captured_database","default_schema",r[0],"captured_database","default_schema","actual",r[1],"NO","NO","YES",r[2]}]++
 }
 return want
}

func tcUnitBag(t *testing.T,rows [][]sqltype.Value) map[[11]string]int {
 t.Helper()
 bag:=map[[11]string]int{}
 for _,row:=range rows {
  if len(row)!=11 { t.Fatal("row width",len(row)) }
  var key [11]string
  for i,value:=range row {
   if value.IsNull() { if i!=10 { t.Fatal("unexpected NULL",i) };key[i]="<NULL>";continue }
   base,err:=value.BaseValue();if err!=nil { t.Fatal(err) }
   key[i],err=base.Text();if err!=nil { t.Fatal(err) }
  }
  bag[key]++
 }
 return bag
}

func tcUnitCheck(t *testing.T,in pgcatalog.Input,want map[[11]string]int) [][]sqltype.Value {
 t.Helper()
 produce,ok:=pgcatalog.ProducerFor(pgcatalog.InfoTableConstraintsOID);if !ok { t.Fatal("missing producer") }
 captured,err:=core.CaptureCatalogSourceRows(in,produce);if err!=nil { t.Fatal(err) };defer captured.Close()
 if captured.Ref!=in.Ref||captured.View!=nil||captured.Anonymous==nil { t.Fatal("capture identity/empty source",captured) }
 for _,row:=range captured.Anonymous { for i,value:=range row {
  column:=in.Source.Columns[i]
  id:=sqltype.ID(13248);if i==6 { id=13246 } else if i>6 { id=13256 }
  owned,_:=in.Catalog.LookupType(id);physical,_:=in.Catalog.LookupType(owned.BaseType().ID())
  if value.Type()!=owned||owned.BaseType()!=physical||!value.ValidResult(column.Usage) { t.Fatal("foreign generation or invalid domain",i,value) }
 } }
 if got:=tcUnitBag(t,captured.Anonymous);!reflect.DeepEqual(got,want) { t.Fatal("complete bag",got,want) }
 return captured.Anonymous
}

func TestInfoTableConstraintsSchema(t *testing.T) {
 f:=newTCUnitFixture(t);in:=f.input(t,f.state,f.f.owner);source:=in.Source
 if source.Address().ObjectID!=16240||source.Namespace().ObjectID!=13229||source.RowType!=13434||!source.VirtualView { t.Fatal("canonical source",source) }
 row:=in.Catalog.MustType(ids.ObjectAddress{ClassID:ids.TypeClassID,ObjectID:13434})
 array:=in.Catalog.MustType(ids.ObjectAddress{ClassID:ids.TypeClassID,ObjectID:13433})
 if row.OwningRelation!=source.Address()||row.Array!=13433||array.SQLType.Element()!=row.SQLType||array.OwningRelation!=source.Address() { t.Fatal("row/array authority",row,array) }
 names:=[]string{"constraint_catalog","constraint_schema","constraint_name","table_catalog","table_schema","table_name","constraint_type","is_deferrable","initially_deferred","enforced","nulls_distinct"}
 if len(source.Columns)!=len(names) { t.Fatal("column count",len(source.Columns)) }
 for i,column:=range source.Columns {
  id,base:=sqltype.ID(13248),sqltype.Name;if i==6 { id,base=13246,sqltype.VarChar } else if i>6 { id,base=13256,sqltype.VarChar }
  owned,_:=in.Catalog.LookupType(id);physical,_:=in.Catalog.LookupType(base)
  if column.Name!=names[i]||column.Usage!=owned.Usage()||owned.BaseType()!=physical||column.Usage.Typmod != -1||column.Usage.Collation.ID!=950||column.DeclaredDimensions!=0||!column.Nullable||!column.NullabilityKnown||column.OriginTable!=16240||column.OriginAttribute!=int16(i+1)||source.Attributes[i]!=int32(i+1) { t.Fatal("schema/domain/origin",i,column) }
  attr:=source.ColumnMeta.CatalogAttributes[i]
  if attr.NotNull||attr.Collation!=950||attr.Dimensions!=0 { t.Fatal("attribute metadata",i,attr) }
 }
 if len(source.Security.ACL.Entries)!=1||!source.Security.ACL.Entries[0].Grantee.Public||source.Security.ACL.Entries[0].Privileges!=authorization.SelectPrivilege { t.Fatal("PUBLIC SELECT",source.Security.ACL) }
 tcUnitCheck(t,in,tcUnitExpected())
}

func TestInfoTableConstraintsProducer(t *testing.T) {
 f:=newTCUnitFixture(t);all:=tcUnitExpected()
 for _,actor:=range []authorization.PrincipalRef{f.f.owner,f.f.member,f.f.op.Session.Authorization.State.CurrentUser} { tcUnitCheck(t,f.input(t,f.state,actor),all) }
 for _,kind:=range []catalog.RelationKind{catalog.TableRelation,catalog.PartitionedRelation,catalog.ViewRelation,catalog.ForeignRelation,catalog.MaterializedViewRelation,catalog.IndexRelation,catalog.SequenceRelation,catalog.CompositeRelation} {
  next:=catalog.CloneState(f.state);table:=*f.table;table.RelationKind=kind
  if kind==catalog.IndexRelation { table.SQLIndex=&catalog.IndexDefinition{} }
  next.SetDescriptor(table.Address(),&table)
  want:=map[[11]string]int{};if kind==catalog.TableRelation||kind==catalog.PartitionedRelation { want=all }
  tcUnitCheck(t,f.input(t,next,f.f.owner),want)
 }
 empty:=catalog.CloneState(f.state);table:=*f.table;table.Constraints=catalog.RelationConstraints{};empty.SetDescriptor(table.Address(),&table)
 tcUnitCheck(t,f.input(t,empty,f.f.owner),map[[11]string]int{})
 // Validation has no effect on the independently enforced flag.
 next:=catalog.CloneState(f.state);table=*f.table
 table.Constraints.Check=append([]catalog.CheckConstraint(nil),table.Constraints.Check...);table.Constraints.Check[0].NotValid=false
 table.Constraints.ForeignKeys=append([]catalog.ForeignKeyConstraint(nil),table.Constraints.ForeignKeys...);table.Constraints.ForeignKeys[0].NotValid=false
 next.SetDescriptor(table.Address(),&table);tcUnitCheck(t,f.input(t,next,f.f.owner),all)
}

func TestInfoTableConstraintsVisibility(t *testing.T) {
 f:=newTCUnitFixture(t);empty:=map[[11]string]int{};all:=tcUnitExpected()
 tcUnitCheck(t,f.input(t,f.state,f.f.reader),empty)
 for _,column:=range []bool{false,true} {
  for _,right:=range []authorization.Privileges{authorization.SelectPrivilege,authorization.MaintainPrivilege,authorization.InsertPrivilege,authorization.UpdatePrivilege,authorization.DeletePrivilege,authorization.TruncatePrivilege,authorization.ReferencesPrivilege,authorization.TriggerPrivilege} {
   // Only real column-grant kinds can be installed on column ObjectRefs.
   if column&&right!=authorization.SelectPrivilege&&right!=authorization.InsertPrivilege&&right!=authorization.UpdatePrivilege&&right!=authorization.ReferencesPrivilege { continue }
   state:=catalog.CloneState(f.state);table:=*f.table
   acl:=authorization.ACLValue{Explicit:true,Entries:[]authorization.ACLEntry{{Grantor:f.f.owner,Grantee:authorization.ACLGrantee{Role:f.f.reader},Privileges:right}}}
   if column { table.Security.ColumnACL=map[int32]authorization.ACLValue{2:acl} } else { table.Security.ACL=acl }
   state.SetDescriptor(table.Address(),&table)
   want:=all;if right==authorization.SelectPrivilege||right==authorization.MaintainPrivilege { want=empty }
   tcUnitCheck(t,f.input(t,state,f.f.reader),want)
   if column {
    table.ColumnMeta.Dropped=map[int32]catalog.AttributeMetadata{2:{}}
    state=catalog.CloneState(state);state.SetDescriptor(table.Address(),&table)
    tcUnitCheck(t,f.input(t,state,f.f.reader),empty)
   }
  }
 }
 {
  state:=catalog.CloneState(f.state);table:=*f.table
  table.Attributes=[]int32{-1,0,1,2}
  acl:=authorization.ACLValue{Explicit:true,Entries:[]authorization.ACLEntry{{Grantor:f.f.owner,Grantee:authorization.ACLGrantee{Role:f.f.reader},Privileges:authorization.InsertPrivilege}}}
  table.Security.ColumnACL=map[int32]authorization.ACLValue{-1:acl,0:acl}
  state.SetDescriptor(table.Address(),&table)
  tcUnitCheck(t,f.input(t,state,f.f.reader),empty)
 }
 // Namespace privileges are irrelevant; an explicit empty owner ACL still
 // exposes all rows to the owner and the inheriting owner role.
 tcUnitCheck(t,f.input(t,f.state,f.f.owner),all)
 tcUnitCheck(t,f.input(t,f.state,f.f.member),all)
 state:=catalog.CloneState(f.state);table:=*f.table
 table.Security.ACL=authorization.ACLValue{Explicit:true,Entries:[]authorization.ACLEntry{{Grantor:f.f.owner,Grantee:authorization.ACLGrantee{Public:true},Privileges:authorization.InsertPrivilege}}}
 state.SetDescriptor(table.Address(),&table);tcUnitCheck(t,f.input(t,state,f.f.reader),all)
 // A session token, not an allocator-dependent schema name, owns TEMP scope.
 ns:=*f.f.e.Catalog.Read(state).MustNamespace(f.f.schema)
 ns.Temporary=f.f.e.Catalog.Domain.Session(99);state=catalog.CloneState(state);state.SetDescriptor(ns.Address(),&ns)
 in:=f.input(t,state,f.f.owner);tcUnitCheck(t,in,empty)
 runtime:=in.Runtime.(core.EngineCatalogRuntime)
 in.Runtime=core.NewCatalogRuntime(core.CatalogRuntimeFields{Auth:runtime.Auth,Database:"captured_database",Temporary:ns.Temporary})
 tcUnitCheck(t,in,all)
}

func TestInfoTableConstraintsCapture(t *testing.T) {
 f:=newTCUnitFixture(t);old:=f.input(t,f.state,f.f.owner);all:=tcUnitExpected()
 retained:=tcUnitCheck(t,old,all)
 changed:=catalog.CloneState(f.state);table:=*f.table;table.Owner=f.f.reader;table.Constraints.Check=nil;changed.SetDescriptor(table.Address(),&table)
 tcUnitCheck(t,f.input(t,changed,f.f.owner),map[[11]string]int{})
 nextWant:=tcUnitExpected();for key:=range nextWant { if key[2]=="same" { delete(nextWant,key) } }
 tcUnitCheck(t,f.input(t,changed,f.f.reader),nextWant)
 tcUnitCheck(t,old,all)
 if !reflect.DeepEqual(tcUnitBag(t,retained),all)||retained[0][0].Type()!=old.Source.Columns[0].Usage.Type { t.Fatal("retained cells/generation changed") }
 // Replace the live descriptor generation coherently. Retained cells keep the
 // old captured domain and backing; fresh rows use the new canonical domain.
 replacement:=catalog.CloneState(f.state);foreign:=engine.New()
 for _,id:=range []sqltype.ID{sqltype.Name,sqltype.VarChar,13248,13246,13256} {
  address:=ids.ObjectAddress{ClassID:ids.TypeClassID,ObjectID:ids.OID(id)}
  descriptor:=*old.Catalog.MustType(address)
  descriptor.SQLType,_=foreign.CatalogRead().LookupType(id)
  replacement.SetDescriptor(address,&descriptor)
 }
 replacedSource:=*old.Source;replacedSource.Columns=append([]catalog.Column(nil),old.Source.Columns...)
 for i:=range replacedSource.Columns {
  id:=sqltype.ID(13248);if i==6 { id=13246 } else if i>6 { id=13256 }
  typ,_:=foreign.CatalogRead().LookupType(id);replacedSource.Columns[i].Usage=typ.Usage()
 }
 replacement.SetDescriptor(replacedSource.Address(),&replacedSource)
 fresh:=tcUnitCheck(t,f.input(t,replacement,f.f.owner),all)
 if fresh[0][0].Type()==retained[0][0].Type()||retained[0][0].Type()!=old.Source.Columns[0].Usage.Type||!reflect.DeepEqual(tcUnitBag(t,retained),all) { t.Fatal("retained domain generation/backing") }
 produce,_:=pgcatalog.ProducerFor(pgcatalog.InfoTableConstraintsOID)
 for _,mutate:=range []func(*pgcatalog.Input){
  func(in *pgcatalog.Input) { in.Owner=&engine.New().Catalog.Owner },
  func(in *pgcatalog.Input) { in.Catalog=engine.New().CatalogRead() },
  func(in *pgcatalog.Input) { in.Roles=engine.New().RoleStore.Published.Root.Read(authorization.PrincipalRef{}) },
  func(in *pgcatalog.Input) { in.Ref.Epoch++ },
  func(in *pgcatalog.Input) { in.Ref.Epoch=0 },
  func(in *pgcatalog.Input) { in.Ref.Address.SubID=1 },
  func(in *pgcatalog.Input) { in.Ref=new(ids.Domain).Ref(in.Ref.Epoch,in.Ref.Address) },
  func(in *pgcatalog.Input) { copy:=*in.Source;in.Source=&copy },
  func(in *pgcatalog.Input) { in.Source=nil },
  func(in *pgcatalog.Input) { r:=in.Runtime.(core.EngineCatalogRuntime);r.Auth.RoleRecord=nil;in.Runtime=r },
  func(in *pgcatalog.Input) { r:=in.Runtime.(core.EngineCatalogRuntime);r.Auth.State.CurrentUser=authorization.PrincipalRef{};in.Runtime=r },
 } { in:=old;mutate(&in);if rows,err:=produce(in);rows!=nil||err==nil { t.Fatal("invalid capture accepted",rows,err) } }
 for _,kind:=range []string{"width","domain","generation","base-generation","typmod","collation","namespace","owner","definition"} {
  t.Run(kind,func(t *testing.T) {
   state:=catalog.CloneState(f.state)
   if kind=="namespace"||kind=="owner"||kind=="definition" {
    table:=*f.table
    switch kind { case "namespace":table.ObjectNamespace.ObjectID=999999;case "owner":table.Owner=authorization.PrincipalRef{};case "definition":table.Constraints.Check=[]catalog.CheckConstraint{{Name:"invalid"}} }
    state.SetDescriptor(table.Address(),&table)
   } else if kind=="base-generation" {
    d:=*old.Catalog.MustType(ids.ObjectAddress{ClassID:ids.TypeClassID,ObjectID:ids.OID(sqltype.Name)})
    d.SQLType,_=engine.New().CatalogRead().LookupType(sqltype.Name);state.SetDescriptor(d.Address(),&d)
   } else {
    source:=*old.Source;source.Columns=append([]catalog.Column(nil),source.Columns...)
    switch kind {
    case "width":source.Columns=source.Columns[:10]
    case "domain":source.Columns[0].Usage.Type,_=old.Catalog.LookupType(sqltype.Name)
    case "generation":typ:=source.Columns[0].Usage.Type;var err error;source.Columns[0].Usage.Type,err=typ.RebindDomain(typ.DomainBase(),typ.Name(),typ.SQLName());if err!=nil { t.Fatal(err) }
    case "typmod":source.Columns[0].Usage.Typmod=7
    case "collation":source.Columns[0].Usage.Collation.ID=0
    }
    state.SetDescriptor(source.Address(),&source)
   }
   if rows,err:=produce(f.input(t,state,f.f.owner));rows!=nil||err==nil { t.Fatal("invalid source accepted",rows,err) }
  })
 }
 ctx,cancel:=context.WithCancel(t.Context());cancel();in:=old;in.Context=ctx
 if rows,err:=produce(in);rows!=nil||!errors.Is(err,context.Canceled) { t.Fatal("canceled source",rows,err) }
 // Deterministically cancel after enumeration/first emission, not by timing.
 position:=0;for _,d:=range old.Catalog.Descriptors() { position++;if d.Address()==f.table.Address() { break } }
 in=old;in.Context=&dtpCancelContext{Context:t.Context(),remaining:position+3}
 if rows,err:=produce(in);rows!=nil||!errors.Is(err,context.Canceled) { t.Fatal("partial capture escaped",rows,err) }
}

func TestInfoTableConstraintsPairedCapture(t *testing.T) {
 f:=newTCUnitFixture(t)
 old:=f.input(t,f.state,f.f.member)
 retained:=tcUnitCheck(t,old,tcUnitExpected())
 changed:=catalog.CloneState(f.state)
 table:=*f.table;table.Constraints.Check=nil;changed.SetDescriptor(table.Address(),&table)
 roles:=cloneRoleFixture(f.f.e.RoleStore.Published)
 var edges []authorization.Membership
 for _,edge:=range fixtureEdges(roles) {
  if edge.Role!=f.f.owner||edge.Member!=f.f.member { edges=append(edges,edge) }
 }
 setFixtureEdges(roles,edges)
 f.f.e.Mu.Lock()
 f.f.e.RoleStore.PublishLocked(roles)
 f.f.e.Catalog.PublishLocked(changed)
 f.f.e.Mu.Unlock()
 tcUnitCheck(t,f.input(t,changed,f.f.member),map[[11]string]int{})
 ownerWant:=tcUnitExpected()
 for key:=range ownerWant { if key[2]=="same" { delete(ownerWant,key) } }
 tcUnitCheck(t,f.input(t,changed,f.f.owner),ownerWant)
 // Old authority and cells belong to the old paired roots, even after both
 // current roots advance. There is no current-role lookup during traversal.
 tcUnitCheck(t,old,tcUnitExpected())
 if !reflect.DeepEqual(tcUnitBag(t,retained),tcUnitExpected())||old.Source!=old.Catalog.MustRelation(old.Source.Address()) { t.Fatal("paired capture lost its original backing") }
}
