package core_test

import (
 "reflect"
 "slices"
 "testing"

 "stuntgres/internal/engine/catalog"
 "stuntgres/internal/engine/core"
 "stuntgres/internal/engine/enginetest"
 "stuntgres/internal/engine/expr"
 "stuntgres/internal/engine/ids"
 "stuntgres/internal/engine/plan"
 "stuntgres/internal/engine/stored"
)

func transformStoredFixture(t *testing.T)(*core.Engine,*catalog.Procedure) {
 t.Helper()
 e,_:=transformDefinitionOwner(t,nil)
 for _,q:=range []string{
  "ALTER FUNCTION owner_transform(hstore) STABLE",
  "CREATE VIEW owner_stored_view AS SELECT owner_transform('a=>7,b=>NULL'::hstore) AS v",
  "CREATE TABLE owner_stored_defaults(v integer DEFAULT (owner_transform('a=>7'::hstore)->'a')::integer CHECK(v <= (owner_transform('a=>7'::hstore)->'a')::integer))",
 } {enginetest.Run(t,e,q)}
 return e,checkedDefaultOwnerProcedure(t,e.CatalogRead(),"owner_transform")
}
func transformStoredRelation(t *testing.T,read catalog.Read,name string)*catalog.Relation {
 t.Helper()
 for _,d:=range read.Descriptors(){if r,ok:=d.(*catalog.Relation);ok && r.Name()==name {return r}}
 t.Fatal("missing actual stored consumer",name);return nil
}
func TestTransformStoredCarrierOwnership(t *testing.T) {
 e,host:=transformStoredFixture(t)
 before:=e.CatalogRead()
 oldView:=transformStoredRelation(t,before,"owner_stored_view")
 oldTable:=transformStoredRelation(t,before,"owner_stored_defaults")
 presentation:=stored.ViewOf(oldView)
 oldRoot:=presentation.Query().Root
 oldDeps:=slices.Clone(presentation.Dependencies())
 oldChecks:=slices.Clone(oldTable.Constraints.Check)
 var oldDefault catalog.ColumnDefault
 for _,d:=range oldTable.ColumnMeta.Defaults {oldDefault=d}
 if oldDefault.Expression==nil || len(oldChecks)!=1 {t.Fatal("missing actual default/CHECK fixture")}
 checkDeps:=slices.Clone(oldChecks[0].Dependencies)
 enginetest.Run(t,e,transformOwnerReplacement)
 current:=e.CatalogRead()
 nextHost:=checkedDefaultOwnerProcedure(t,current,"owner_transform")
 nextView:=transformStoredRelation(t,current,"owner_stored_view")
 nextTable:=transformStoredRelation(t,current,"owner_stored_defaults")
 if nextView==oldView || nextTable==oldTable || stored.ViewOf(nextView)==presentation || stored.ViewOf(nextView).Query().Root==oldRoot {t.Fatal("stored carrier refresh borrowed original root/descriptor")}
 if len(nextTable.ColumnMeta.Defaults)!=1 || len(nextTable.Constraints.Check)!=1 {t.Fatal("refresh lost actual default/CHECK")}
 var nextDefault catalog.ColumnDefault
 for _,d:=range nextTable.ColumnMeta.Defaults {nextDefault=d}
 if nextDefault.Expression==nil || nextTable.Constraints.Check[0].Expression==nil {t.Fatal("refresh erased actual default/CHECK expression")}
 if &nextTable.Constraints.Check[0]==&oldTable.Constraints.Check[0] {t.Fatal("stored CHECK borrowed original backing")}
 seen:=false
 check:=func(node expr.Expr){if call,ok:=node.(plan.SQLRoutineExpr);ok && call.Procedure.Address()==host.Address(){seen=true;if call.Procedure==host || call.Procedure.Revision()!=nextHost.Revision() || call.Procedure.Host!=nextHost.Host {t.Fatal("stored consumer lost shared current host layout/stamp")}}}
 plan.VisitCTEQuery(stored.ViewOf(nextView).Query().Root,func(plan.Node){},check,true)
 plan.WalkExpression(nextDefault.Expression,check)
 plan.WalkExpression(nextTable.Constraints.Check[0].Expression,check)
 if !seen {t.Fatal("no actual mapped SQL routine carriers")}
 for _,dependencies:=range [][]catalog.ExpressionDependency{stored.ViewOf(nextView).Dependencies(),nextTable.Constraints.Check[0].Dependencies} {
  found:=false
  for _,d:=range dependencies {if d.Ref.Address==host.Address(){found=true;if d.Revision!=nextHost.Revision() || d.Purpose!=catalog.DefinitionDependency {t.Fatal("stored manifest failed exact current stamp/purpose refresh")}}}
  if !found {t.Fatal("stored consumer lost actual routine dependency")}
 }
 for _,d:=range []catalog.Descriptor{nextView,nextTable} {if err:=current.Owner().Provider(d.Address().ClassID).Validate(current,d);err!=nil {t.Fatal("mapped carrier validation",err)}}
 if before.Get(oldView.Address())!=oldView || before.Get(oldTable.Address())!=oldTable || presentation.Query().Root!=oldRoot || !slices.Equal(presentation.Dependencies(),oldDeps) || !slices.Equal(oldChecks[0].Dependencies,checkDeps) || !reflect.DeepEqual(oldTable.Constraints.Check,oldChecks) {t.Fatal("refresh mutated retained stored graph/manifest/backing")}
 for _,d:=range oldTable.ColumnMeta.Defaults {if !reflect.DeepEqual(d,oldDefault) {t.Fatal("refresh mutated retained default root")}}
}

func TestTransformStoredCarrierRejection(t *testing.T) {
 e,host:=transformStoredFixture(t)
 source:=e.CatalogRead()
 original:=transformStoredRelation(t,source,"owner_stored_view")
 originalRoot:=stored.ViewOf(original).Query().Root
 originalDeps:=slices.Clone(stored.ViewOf(original).Dependencies())
 enginetest.Run(t,e,transformOwnerReplacement)
 published:=e.CatalogRead()
 pair:=published.Get(host.Transforms.At(0).Pair).(*catalog.Transform)
 for _,kind:=range []string{"zero-revision","wrong-purpose","foreign-reference"} {
  t.Run(kind,func(t *testing.T){
   sourceState,targetState:=catalog.CloneState(source.State()),catalog.CloneState(published.State())
   // Reconstruct only a private pre-refresh boundary; no malformed source is
   // ever published into the engine or used to repair a corrupt current root.
   for address,d:=range source.Descriptors(){targetState.SetDescriptor(address,d)}
   targetState.SetDescriptor(pair.Address(),pair)
   parts:=stored.ViewOf(original).Parts()
   parts.Dependencies=slices.Clone(parts.Dependencies)
   found:=false
   for i:=range parts.Dependencies {d:=&parts.Dependencies[i];if d.Ref.Address==host.Address(){found=true;switch kind {case "zero-revision":d.Revision=0;case "wrong-purpose":d.Purpose=catalog.AggregateSupportDependency;case "foreign-reference":d.Ref=new(ids.Domain).Ref(d.Ref.Epoch,d.Ref.Address)}}}
   if !found {t.Fatal("missing actual view host dependency to corrupt")}
   forged:=*original;forged.View=stored.ViewPayload(stored.NewView(parts))
   sourceState.SetDescriptor(forged.Address(),&forged);targetState.SetDescriptor(forged.Address(),&forged)
   e.Mu.Lock()
   result,err:=stored.RefreshTransformRoutines(source.WithState(sourceState),published.WithState(targetState),pair,e.Catalog.RevisionLocked)
   e.Mu.Unlock()
   if err==nil || result!=nil {t.Fatal("invalid stored source repaired or partially returned",kind)}
   if e.CatalogRead().State()!=published.State() || published.Get(pair.Address())!=pair || source.Get(original.Address())!=original || stored.ViewOf(original).Query().Root!=originalRoot || !slices.Equal(stored.ViewOf(original).Dependencies(),originalDeps) {t.Fatal("downstream carrier rejection changed published/retained root, pair or backing")}
  })
 }
}
