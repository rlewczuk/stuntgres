package core_test

import (
 "errors"
 "reflect"
 "slices"
 "testing"

 "stuntgres/internal/engine/catalog"
 "stuntgres/internal/engine/enginetest"
 "stuntgres/internal/engine/exec"
 "stuntgres/internal/engine/expr"
 "stuntgres/internal/engine/ids"
 "stuntgres/internal/engine/plan"
 "stuntgres/internal/engine/stored"
)

func transformCapturedProcedures(t *testing.T,root *catalog.Procedure)map[*catalog.Procedure]bool {
 t.Helper()
 seen:=make(map[*catalog.Procedure]bool)
 var visit func(*catalog.Procedure)
 children:=func(node expr.Expr,next func(expr.Expr)){if composite,ok:=node.(expr.Composite);ok {for _,child:=range composite.Children(){next(child)}}}
 visit=func(p *catalog.Procedure){
  if p==nil || seen[p] {return};seen[p]=true
  if err:=stored.VisitDefinitionExpressions(p,func(root expr.Expr){expr.Walk(root,children,func(node expr.Expr){if call,ok:=node.(plan.SQLRoutineExpr);ok {visit(call.Procedure)}})});err!=nil {t.Fatal(err)}
  for _,edge:=range p.Body {visit(edge.Routine)}
 }
 visit(root);return seen
}

func TestTransformCapturedConsumerOwnership(t *testing.T) {
 e,host:=transformDefinitionOwner(t,nil)
 for _,q:=range []string{
  "CREATE FUNCTION owner_chain_a(v hstore) RETURNS hstore LANGUAGE SQL RETURN owner_transform(v)",
  "CREATE FUNCTION owner_chain_b(v hstore) RETURNS hstore LANGUAGE SQL RETURN owner_chain_a(v)",
  "CREATE FUNCTION owner_nested_pl(v hstore DEFAULT owner_chain_b('a=>7'::hstore)) RETURNS hstore LANGUAGE plpgsql AS $$ BEGIN RETURN owner_chain_b(v); END $$",
  "CREATE FUNCTION owner_nested_default(v hstore DEFAULT owner_nested_pl('a=>7'::hstore)) RETURNS hstore LANGUAGE SQL RETURN v",
  "CREATE FUNCTION owner_support(s hstore,v hstore) RETURNS hstore LANGUAGE SQL RETURN owner_transform(v)",
  "CREATE AGGREGATE owner_capture_aggregate(hstore)(SFUNC=owner_support,STYPE=hstore)",
  "CREATE FUNCTION owner_aggregate_wrapper(v hstore) RETURNS hstore LANGUAGE SQL RETURN (SELECT owner_capture_aggregate(x) FROM (VALUES(v)) q(x))",
 } {enginetest.Run(t,e,q)}
 preOwner:=e.CatalogRead()
 historical:=checkedDefaultOwnerProcedure(t,preOwner,"owner_chain_a")
 enginetest.Run(t,e,"CREATE ROLE owner_capture_actor NOSUPERUSER NOINHERIT")
 enginetest.Run(t,e,"ALTER FUNCTION owner_chain_a(hstore) OWNER TO owner_capture_actor")
 before:=e.CatalogRead()
 seed:=checkedDefaultOwnerProcedure(t,before,"owner_chain_a")
 if seed==historical || seed.Owner==historical.Owner || historical.SQLBody==nil || !exec.SameRoutineDeclaration(seed,historical) {t.Fatal("missing actual compatible historical owner COW carrier")}
 names:=[]string{"owner_transform","owner_chain_a","owner_chain_b","owner_nested_pl","owner_nested_default","owner_support","owner_aggregate_wrapper"}
 old:=make(map[string]*catalog.Procedure)
 roots:=make(map[*catalog.Procedure][]expr.Expr)
 dependencies:=make(map[*catalog.Procedure][]catalog.ExpressionDependency)
 converters:=make(map[*catalog.Procedure][]ids.ObjectAddress)
 for _,name:=range names {
  p:=checkedDefaultOwnerProcedure(t,before,name);old[name]=p
  if p.SQLBody!=nil {dependencies[p]=slices.Clone(p.SQLBody.Dependencies())}
  if p.SQLDefaults!=nil {dependencies[p]=slices.Clone(p.SQLDefaults.Dependencies())}
  for captured:=range transformCapturedProcedures(t,p) {
   roots[captured]=checkedDefaultOwnerRoots(t,captured)
   if captured.Host!=nil {converters[captured]=slices.Clone(captured.Host.Converters)}
  }
 }
 defaultBefore:=old["owner_nested_default"].SQLDefaults
 defaultRoots:=checkedDefaultOwnerRoots(t,old["owner_nested_default"])
 if len(defaultRoots)==0 {t.Fatal("missing actual nested checked default root")}
 enginetest.Run(t,e,transformOwnerReplacement)
 current:=e.CatalogRead()
 pair:=current.Get(host.Transforms.At(0).Pair).(*catalog.Transform)
 witnessedHost,witnessedDefault:=false,false
 for _,name:=range names {
  next:=checkedDefaultOwnerProcedure(t,current,name)
  previous:=old[name]
  if next==previous || next.Revision()==previous.Revision() {t.Fatal("capture reverse closure missed genuine consumer",name)}
  if name=="owner_nested_default" {
   // This fixture is RETURN v: only its actual captured default changed.
   // Keep the immutable constant body, rather than manufacturing a body copy.
   if previous.SQLBody==nil || next.SQLBody!=previous.SQLBody {t.Fatal("default-only refresh replaced unchanged body")}
   for _,dependency:=range previous.SQLBody.Dependencies() {if dependency.Ref.Address.ClassID==ids.ProcedureClassID {t.Fatal("constant-body control unexpectedly captures a routine")}}
  } else if previous.SQLBody!=nil && next.SQLBody==previous.SQLBody {t.Fatal("changed body borrowed predecessor capture graph",name)}
  if previous.SQLDefaults!=nil && next.SQLDefaults==previous.SQLDefaults {t.Fatal("default borrowed predecessor capture graph",name)}
  for captured:=range transformCapturedProcedures(t,next) {
   if roots[captured]!=nil {t.Fatal("new capture reused retained old procedure node",name,captured.Name())}
   if captured.Address()==host.Address() {
    witnessedHost=true
    if captured.Revision()!=checkedDefaultOwnerProcedure(t,current,"owner_transform").Revision() || captured.Host==host.Host || captured.Host.Layout==host.Host.Layout {t.Fatal("nested capture retained old host declaration authority")}
    for i,address:=range captured.Host.Converters {
     expected:=pair.To;if i<len(captured.Host.Layout.Inputs){expected=pair.From}
     if address!=expected || captured.Host.Pairs[i]!=pair.Address() {t.Fatal("nested capture retained old directional vectors")}
    }
   }
  }
  if next.SQLBody!=nil {if err:=next.SQLBody.Validate(current);err!=nil {t.Fatal(name,err)}}
  if next.SQLDefaults!=nil {if err:=next.SQLDefaults.Validate(current);err!=nil {t.Fatal(name,err)};witnessedDefault=true}
 }
 if !witnessedHost || !witnessedDefault {t.Fatal("missing actual transitive host/default ownership witnesses")}
 nextA:=checkedDefaultOwnerProcedure(t,current,"owner_chain_a")
 nextB:=checkedDefaultOwnerProcedure(t,current,"owner_chain_b")
 var retained *catalog.Procedure
 for captured:=range transformCapturedProcedures(t,nextB) {if captured.Address()==historical.Address() && captured.Owner==historical.Owner {retained=captured}}
 if retained==nil || retained==historical || retained.SQLBody!=nextA.SQLBody || retained.SQL!=nextA.SQL || !exec.SameRoutineDeclaration(nextA,retained) {t.Fatal("historical COW lost original-identity memo sharing/current compatibility")}
 expectedRevision:=historical.Revision();if historical.Revision()==seed.Revision() {expectedRevision=nextA.Revision()}
 if retained.Revision()!=expectedRevision {t.Fatal("historical body received another source revision's stamp")}
 found:=false
 for _,dependency:=range nextB.SQLBody.Dependencies() {if dependency.Ref.Address==retained.Address() && dependency.Purpose==catalog.DefinitionDependency {found=true;if dependency.Revision!=retained.Revision(){t.Fatal("historical dependency stamp disagrees with actual retained executable")}}}
 if !found || preOwner.Get(historical.Address())!=historical || historical.SQLBody!=seed.SQLBody {t.Fatal("historical body/source snapshot authority lost")}
 if old["owner_nested_default"].SQLDefaults!=defaultBefore || !reflect.DeepEqual(checkedDefaultOwnerRoots(t,old["owner_nested_default"]),defaultRoots) {t.Fatal("refresh mutated original checked default root")}
 for captured,saved:=range roots {if !reflect.DeepEqual(checkedDefaultOwnerRoots(t,captured),saved) {t.Fatal("refresh mutated retained expression roots")}}
 for captured,saved:=range converters {if !slices.Equal(captured.Host.Converters,saved) {t.Fatal("refresh mutated retained nested converter backing")}}
 for p,saved:=range dependencies {
  actual:=p.SQLDefaults;if actual!=nil {if !slices.Equal(actual.Dependencies(),saved){t.Fatal("old default manifest mutated")}}else if !slices.Equal(p.SQLBody.Dependencies(),saved){t.Fatal("old body manifest mutated")}
  if before.Get(p.Address())!=p {t.Fatal("refresh mutated source snapshot descriptor ownership")}
 }
}

func TestTransformRefreshSourceProvenance(t *testing.T) {
 source,original:=transformDefinitionOwner(t,nil)
 before:=source.CatalogRead()
 enginetest.Run(t,source,transformOwnerReplacement)
 current:=source.CatalogRead();pair:=current.Get(original.Transforms.At(0).Pair).(*catalog.Transform)
 foreign,_:=transformDefinitionOwner(t,nil)
 for _,read:=range []catalog.Read{foreign.CatalogRead(),catalog.Read{}} {
  result,err:=stored.RefreshTransformRoutines(read,current,pair,func()(ids.DefinitionRevision,error){t.Fatal("foreign source reached revision allocation");return 0,nil})
  if !errors.Is(err,ids.ErrCatalogReplaced) || result!=nil {t.Fatal("foreign original graph acquired local refresh authority",err)}
  if source.CatalogRead().State()!=current.State() || before.Get(original.Address())!=original {t.Fatal("rejected source mutated published/retained catalog")}
 }
}

func TestTransformIncompatibleCapturePublication(t *testing.T) {
 e,host:=transformDefinitionOwner(t,nil)
 for _,q:=range []string{
  "CREATE FUNCTION owner_cycle_a(v hstore) RETURNS hstore LANGUAGE SQL RETURN owner_transform(v)",
  "CREATE FUNCTION owner_cycle_b(v hstore) RETURNS hstore LANGUAGE SQL RETURN owner_cycle_a(v)",
 } {enginetest.Run(t,e,q)}
 original:=e.CatalogRead()
 historical:=checkedDefaultOwnerProcedure(t,original,"owner_cycle_a")
 b:=checkedDefaultOwnerProcedure(t,original,"owner_cycle_b")
 if historical.SQLBody==nil || b.SQLBody==nil {t.Fatal("cycle fixture did not capture actual SQL-standard programs")}
 enginetest.Run(t,e,"CREATE OR REPLACE FUNCTION owner_cycle_a(v hstore) RETURNS hstore LANGUAGE SQL RETURN CASE WHEN exist(v,'recurse') THEN owner_cycle_b(delete(v,'recurse')) ELSE owner_transform(v) END")
 before:=e.CatalogRead()
 a:=checkedDefaultOwnerProcedure(t,before,"owner_cycle_a")
 if exec.SameRoutineDeclaration(a,historical) || a.SQLBody==historical.SQLBody {t.Fatal("fixture lacks genuine incompatible current/historical bodies")}
 pair:=before.Get(host.Transforms.At(0).Pair).(*catalog.Transform)
 savedA,savedB:=slices.Clone(a.SQLBody.Dependencies()),slices.Clone(b.SQLBody.Dependencies())
 rootsA,rootsB:=checkedDefaultOwnerRoots(t,a),checkedDefaultOwnerRoots(t,b)
 result,err:=enginetest.Execute(e,t.Context(),enginetest.Statement(t,transformOwnerReplacement),nil)
 if result.Rows!=nil {result.Rows.Close()}
 if err==nil {t.Fatal("incompatible historical body silently canonicalized into current A-to-B-to-A cycle")}
 current:=e.CatalogRead()
 if current.State()!=before.State() || current.Epoch()!=before.Epoch() || current.Get(pair.Address())!=pair || current.Get(a.Address())!=a || current.Get(b.Address())!=b || current.Get(host.Address())!=host {t.Fatal("failed dependent rebuild partially published root/pair/consumer stamps")}
 if !slices.Equal(a.SQLBody.Dependencies(),savedA) || !slices.Equal(b.SQLBody.Dependencies(),savedB) || !reflect.DeepEqual(checkedDefaultOwnerRoots(t,a),rootsA) || !reflect.DeepEqual(checkedDefaultOwnerRoots(t,b),rootsB) {t.Fatal("rejected cycle rebuild mutated retained manifests/backing")}
 if original.Get(historical.Address())!=historical || historical.SQLBody==a.SQLBody {t.Fatal("rejected cycle rebuild replaced original executable body")}
}
