package enginetests_test

import (
 "context"
 "fmt"
 "reflect"
 "testing"
 "stuntgres/internal/engine"
 "stuntgres/internal/engine/analyze"
 "stuntgres/internal/engine/authorization"
 "stuntgres/internal/engine/catalog"
 "stuntgres/internal/engine/core"
 "stuntgres/internal/engine/enginetest"
 "stuntgres/internal/engine/exec"
 "stuntgres/internal/engine/expr"
 "stuntgres/internal/engine/extension"
 "stuntgres/internal/engine/functions"
 "stuntgres/internal/engine/ids"
 "stuntgres/internal/engine/plan"
 "stuntgres/internal/engine/session"
 "stuntgres/internal/engine/stored"
 "stuntgres/internal/engine/typedef"
 "stuntgres/internal/sqltype"
)

func ltreeArrayOwnershipBinding(t *testing.T,e *core.Engine,s *session.Session,direct bool) *plan.ProjectNode {
 t.Helper()
 query:="SELECT paths @> target,target <@ paths,paths <@ target,target @> paths,paths ~ q,q ~ paths,target ? queries,queries ? target,paths ? queries,queries ? paths,paths @ x,x @ paths,paths ?@> target,paths ?<@ target FROM la_owner"
 if direct {query="SELECT public._ltree_isparent(paths,target),public._ltree_r_isparent(target,paths),public._ltree_risparent(paths,target),public._ltree_r_risparent(target,paths),public._ltq_regex(paths,q),public._ltq_rregex(q,paths),public.lt_q_regex(target,queries),public.lt_q_rregex(queries,target),public._lt_q_regex(paths,queries),public._lt_q_rregex(queries,paths),public._ltxtq_exec(paths,x),public._ltxtq_rexec(x,paths),public._ltree_extract_isparent(paths,target),public._ltree_extract_risparent(paths,target) FROM la_owner"}
 ast:=enginetest.Statement(t,query)
 op,err:=s.Acquire(context.Background());if err!=nil{t.Fatal(err)};defer op.Done()
 e.Mu.Lock();auth,err:=e.AuthorizationLocked(op);if err!=nil{e.Mu.Unlock();t.Fatal(err)}
 binding,err:=analyze.Describe(op.Context(),e.AnalysisHost(op),ast,core.AuthorityOf(auth),ids.ObjectRef{},sqltype.InputEnvironment{},analyze.Expected{},nil,nil,false);e.Mu.Unlock()
 if err!=nil{t.Fatal(err)}
 project,ok:=binding.Query.Root.(*plan.ProjectNode);if !ok || len(project.Expressions)!=14{t.Fatal("selected projection absent")};return project
}
func ltreeArrayOwnershipCall(t *testing.T,node expr.Expr) functions.LtreeArray {
 t.Helper();if selected,ok:=node.(plan.SelectedOperatorExpr);ok{if selected.RoutineReference!=nil || selected.Operator==nil || selected.Ref.Address!=selected.Operator.Address(){t.Fatal("duplicate operator authority")};node=selected.Provider}
 call,ok:=node.(functions.LtreeArray);if !ok{t.Fatalf("selected provider absent: %T",node)};return call
}
func ltreeArrayOwnershipSetup(t *testing.T,e *core.Engine) *session.Session {
 t.Helper();s:=enginetest.StartSession(t,e,nil);enginetest.SessionSQL(t,s,"CREATE EXTENSION ltree");enginetest.SessionSQL(t,s,"CREATE TABLE la_owner(paths ltree[],queries lquery[],target ltree,q lquery,x ltxtquery)");return s
}
func TestLtreeArrayExpression(t *testing.T) {
 e:=engine.New();s:=ltreeArrayOwnershipSetup(t,e);view:=e.CatalogRead()
 for _,direct:=range []bool{false,true}{
 project:=ltreeArrayOwnershipBinding(t,e,s,direct)
 identity:=func(d *sqltype.Descriptor)*sqltype.Descriptor{return d}
 for i,node:=range project.Expressions {
  call:=ltreeArrayOwnershipCall(t,node)
  if call.Operation!=sqltype.LtreeArrayOperation(i) || call.Member!=uint8(9+i) || call.Info.Occurrence==0 || call.Ref.Domain()!=view.Owner().Domain{t.Fatal("selected occurrence/role lost")}
  for _,rebuild:=range []func(expr.Info,[]expr.Expr)(expr.Expr,error){call.Rebuild,call.Substitute} {
   next,err:=rebuild(call.Info,call.Children());if err!=nil || !reflect.DeepEqual(next,call){t.Fatal("rebuild changed immutable occurrence",err)}
  }
  for _,kind:=range []expr.CaptureKind{expr.CaptureFunction,expr.CaptureOperator}{capture,ok,err:=call.Capture(kind);if err!=nil || !ok || capture.Count!=2 || !reflect.DeepEqual(capture.Args,call.Children()){t.Fatal("capture lost source order",err)}}
  spec,err:=call.Render();if err!=nil || len(spec.Args)!=2 || spec.Args[0].TransportType!=call.Formals[0].Usage() || spec.Args[1].TransportType!=call.Formals[1].Usage(){t.Fatal("formal rendering lost",err)}
  if call.IntrinsicVolatility()!=expr.Immutable || call.PlanningFold()!=expr.FoldConstant || call.StrictNull()!=expr.StrictAfter{t.Fatal("planning policy")}
  next,changed,err:=stored.RetagTypes(view,node,identity);if err!=nil || changed || !reflect.DeepEqual(next,node){t.Fatal("identity retag",changed,err)}
  next,err=stored.RemapExpression(node,func(ref ids.ObjectRef)ids.ObjectRef{return ref},nil);if err!=nil || !reflect.DeepEqual(next,node){t.Fatal("identity reference remap",err)}
  for _,corrupt:=range []func(*functions.LtreeArray){
   func(n *functions.LtreeArray){n.Member++},func(n *functions.LtreeArray){n.Operation^=1},
   func(n *functions.LtreeArray){foreign:=*n.Formals[0];n.Formals[0]=&foreign},
   func(n *functions.LtreeArray){foreign:=*n.Usage.Type;n.Usage.Type=&foreign},
   func(n *functions.LtreeArray){foreign:=*n.RoutineReference.(*catalog.Procedure);foreign.Display.Source="ltree_out";n.RoutineReference=&foreign},
   func(n *functions.LtreeArray){n.Ref.Epoch++},
  } {
   bad:=call;corrupt(&bad);if _,_,err:=stored.RetagTypes(view,bad,identity);err==nil{t.Fatal("forged captured provider admitted")}
   if !reflect.DeepEqual(ltreeArrayOwnershipCall(t,node),call){t.Fatal("failed admission mutated captured source")}
  }
  other:=ltreeArrayOwnershipCall(t,project.Expressions[(i+1)%14]);if call.SameGroup(other,func(a,b expr.Expr)bool{return true},false){t.Fatal("different role grouped")}
 }
 }
}
func TestLtreeArrayLifetime(t *testing.T) {
 source,target:=engine.New(),engine.New()
 target.Mu.Lock();_,err:=target.Catalog.AllocateOIDLocked();target.Mu.Unlock();if err!=nil{t.Fatal(err)}
 ss,ts:=ltreeArrayOwnershipSetup(t,source),ltreeArrayOwnershipSetup(t,target)
 bound:=ltreeArrayOwnershipBinding(t,source,ss,false);_ = ts
 sv,tv:=source.CatalogRead(),target.CatalogRead()
 installed:=func(v catalog.Read)*catalog.Extension{return v.MustExtension(v.NameAddress(ids.NameKey{ClassID:ids.ExtensionClassID,Name:"ltree"}))}
 se,te:=installed(sv),installed(tv)
 mapping:=map[ids.ObjectAddress]ids.ObjectAddress{se.Address():te.Address()}
 for i,a:=range se.Members{mapping[a]=te.Members[i]}
 for _,slot:=range []int{0,3,6}{a,b:=sv.MustType(se.Members[slot]),tv.MustType(te.Members[slot]);mapping[ids.ObjectAddress{ClassID:ids.TypeClassID,ObjectID:ids.OID(a.Array)}]=ids.ObjectAddress{ClassID:ids.TypeClassID,ObjectID:ids.OID(b.Array)}}
 relation:=func(v catalog.Read)ids.ObjectAddress{return v.NameAddress(ids.NameKey{ClassID:ids.RelationClassID,Namespace:ids.ObjectAddress{ClassID:ids.NamespaceClassID,ObjectID:ids.PublicNamespaceOID},Name:"la_owner"})};mapping[relation(sv)]=relation(tv)
 address:=func(old ids.ObjectAddress)ids.ObjectAddress{if next,ok:=mapping[old];ok{return next};return old}
 reference:=func(old ids.ObjectRef)ids.ObjectRef{if old==(ids.ObjectRef{}){return old};return tv.Owner().Domain.Ref(tv.Epoch(),address(old.Address))}
 types:=func(old *sqltype.Descriptor)*sqltype.Descriptor{if old==nil{return nil};next,ok:=tv.LookupType(sqltype.ID(address(ids.ObjectAddress{ClassID:ids.TypeClassID,ObjectID:ids.OID(old.ID())}).ObjectID));if !ok{return old};return next}
 principal:=func(old authorization.PrincipalRef)(authorization.PrincipalRef,error){if old!=(authorization.PrincipalRef{}) && old!=sv.Owner().Principals.Principal(old.ID()){return authorization.PrincipalRef{},fmt.Errorf("foreign source principal")};return tv.Owner().Principals.Principal(old.ID()),nil}
 principals:=catalog.NewProcedureRemapper(catalog.PrincipalRemap{Principal:principal,SeededOwner:func(old authorization.PrincipalRef,_ bool)(authorization.PrincipalRef,error){return principal(old)}})
 procedure:=func(old *catalog.Procedure)(*catalog.Procedure,error){if old==nil{return nil,nil};if err:=extension.StockLtree.ValidateProcedure(sv,old);err!=nil{return nil,err};return principals(extension.RemapDescriptor(old,mapping).(*catalog.Procedure))}
 mapper:=stored.ViewQueryRemapper{Catalog:tv,Ref:reference,Type:types,Procedure:procedure}
 mapped,err:=mapper.Nested(bound);if err!=nil{t.Fatal("authorized source remap",err)}
 project,ok:=mapped.(*plan.ProjectNode);if !ok{t.Fatal("lost project")}
 for i,node:=range project.Expressions {
  old,next:=ltreeArrayOwnershipCall(t,bound.Expressions[i]),ltreeArrayOwnershipCall(t,node)
  if next.Ref!=reference(old.Ref) || next.Ref==old.Ref || next.Formals[0]!=types(old.Formals[0]) || next.Formals[1]!=types(old.Formals[1]) || next.Formals==old.Formals || next.Info.Source!=old.Info.Source || next.Info.Occurrence!=old.Info.Occurrence || next.Operation!=old.Operation{t.Fatal("remap lost exact ownership")}
  captured,err:=exec.ExpressionRoutineDescriptor(next);if err!=nil || captured==old.RoutineReference || !exec.SameRoutineDeclaration(tv.MustProcedure(captured.Address()),captured){t.Fatal("remapped captured declaration",err)}
  if old.Ref.Domain()!=sv.Owner().Domain || ltreeArrayOwnershipCall(t,bound.Expressions[i]).Formals!=old.Formals{t.Fatal("source changed")}
 }
 imports:=make(map[*sqltype.Descriptor]*sqltype.Descriptor)
 for _,slot:=range []int{0,3,6}{old:=sv.MustType(se.Members[slot]).SQLType;imports[old]=types(old)}
 for _,slot:=range []int{0,3}{
  oldBase:=sv.MustType(se.Members[slot]).SQLType
  oldArray,ok:=sv.LookupArray(oldBase);if !ok{t.Fatal("source companion absent")}
  nextArray:=types(oldArray)
  for _,text:=range []string{"{a,NULL}","{{a,b}}","{}","[7:8]={a,b}"}{
   value,err:=oldArray.Input(t.Context(),text,-1);if err!=nil{t.Fatal(err)}
   moved,err:=typedef.ReownDomainDatum(value,nextArray,nil,imports,nil);if err!=nil || moved.Type()!=nextArray || moved.ArrayRank()!=value.ArrayRank(){t.Fatal("recursive array remint",err)}
   for i:=range value.ArrayLen(){
    entry:=moved.ArrayElement(i);sourceEntry:=value.ArrayElement(i)
    if entry.Type()!=nextArray.Element() || entry.IsNull()!=sourceEntry.IsNull(){t.Fatal("element/typed NULL retained source generation")}
    var back sqltype.Value
    if slot==0{back,err=entry.RemapLquery(oldBase)}else{back,err=entry.RemapLtree(oldBase)}
    if err!=nil || back!=sourceEntry{t.Fatal("authorized import copied immutable compiled backing",err)}
   }
   if _,err:=typedef.ReownDomainDatum(value,nextArray,nil,nil,nil);value.ArrayLen()>0 && err==nil{t.Fatal("array import bypassed source scalar authorization")}
  }
 }
 for _,input:=range []*plan.ProjectNode{bound,ltreeArrayOwnershipBinding(t,source,ss,true)} {
  // A mapper owns a multi-root operation. A changed destination policy starts
  // a fresh operation so source/current validators authenticate it again.
  accepted,err:=mapper.Nested(input);if err!=nil{t.Fatal("reused authorized remap",err)}
  again,err:=mapper.Nested(input);if err!=nil || again!=accepted{t.Fatal("accepted root identity lost",err)}
  fresh:=mapper.Fresh()
  distinct,err:=fresh.Nested(input);if err!=nil || distinct==accepted{t.Fatal("fresh operation inherited accepted root",err)}
  for _,slot:=range []int{0,3,6} {
   scalar:=sv.MustType(se.Members[slot]).SQLType
   candidates:=[]*sqltype.Descriptor{scalar}
   if slot!=6 {array,ok:=sv.LookupArray(scalar);if !ok{t.Fatal("source companion absent")};candidates=append(candidates,array)}
   for _,candidate:=range candidates {
    foreign:=*types(candidate)
    bad:=mapper.Fresh();bad.Type=func(old *sqltype.Descriptor)*sqltype.Descriptor{next:=types(old);if next.ID()==foreign.ID(){return &foreign};return next}
    if _,err:=bad.Nested(input);err==nil{t.Fatal("foreign same-OID target generation admitted")}
   }
  }
  bad:=mapper.Fresh();bad.Ref=func(ref ids.ObjectRef)ids.ObjectRef{next:=reference(ref);if next==(ids.ObjectRef{}){return next};return sv.Owner().Domain.Ref(next.Epoch,next.Address)}
  if _,err:=bad.Nested(input);err==nil{t.Fatal("foreign reference domain admitted")}
  bad=mapper.Fresh();bad.Ref=func(ref ids.ObjectRef)ids.ObjectRef{next:=reference(ref);if next!=(ids.ObjectRef{}){next.Epoch=0};return next}
  if _,err:=bad.Nested(input);err==nil{t.Fatal("foreign reference epoch admitted")}
  state:=catalog.CloneState(tv.State())
  broken:=*tv.MustProcedure(te.Members[extension.LtreeArrayProcedureStart]);broken.Result=25
  state.SetDescriptor(broken.Address(),&broken)
  bad=mapper.Fresh();bad.Catalog=target.Catalog.Read(state)
  if _,err:=bad.Nested(input);err==nil{t.Fatal("changed destination procedure declaration admitted")}
  bad=mapper.Fresh();bad.Procedure=func(old *catalog.Procedure)(*catalog.Procedure,error){return nil,fmt.Errorf("rejected destination procedure policy")}
  if _,err:=bad.Nested(input);err==nil{t.Fatal("changed procedure policy bypassed")}
  // A failure must not publish placeholders into a later valid operation or
  // mutate the accepted graph in the original operation.
  recovered:=bad.Fresh();recovered.Procedure=procedure
  if _,err:=recovered.Nested(input);err!=nil{t.Fatal("failed operation poisoned fresh valid remap",err)}
  retained,err:=mapper.Nested(input);if err!=nil || retained!=accepted{t.Fatal("rejected remap poisoned authorized traversal",err)}
 }
 if installed(sv)!=se || installed(tv)!=te{t.Fatal("rejected remap published mutation")}
}
