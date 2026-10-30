package core_test

import (
 "testing"

 "stuntgres/internal/engine/catalog"
 "stuntgres/internal/engine/enginetest"
 "stuntgres/internal/engine/proc"
 "stuntgres/internal/engine/stored"
)

func TestTransformRefreshMalformedSource(t *testing.T) {
 for _,kind:=range []string{"signature","zero-revision","foreign-type","epoch","tombstone"} {
  t.Run(kind,func(t *testing.T){
   e,original:=transformDefinitionOwner(t,nil)
   enginetest.Run(t,e,"CREATE FUNCTION owner_checked_capture(v hstore) RETURNS hstore LANGUAGE SQL RETURN owner_transform(v)")
   source:=e.CatalogRead()
   if err:=catalog.ValidateHostProcedure(source,original);err!=nil {t.Fatal("invalid positive source before private corruption",err)}
   enginetest.Run(t,e,transformOwnerReplacement)
   published:=e.CatalogRead();pair:=published.Get(original.Transforms.At(0).Pair).(*catalog.Transform)
   sourceState,targetState:=catalog.CloneState(source.State()),catalog.CloneState(published.State())
   // Reconstruct only the private pre-consumer-refresh boundary from real
   // descriptors. Malformed originals must never become successful publishers.
   for address,d:=range source.Descriptors(){if _,ok:=d.(*catalog.Procedure);ok {targetState.SetDescriptor(address,d)}}
   bad:=*original
   switch kind {
   case "signature":bad.Signature="23"
   case "zero-revision":bad.ObjectRevision=0
   case "foreign-type":
    foreign,other:=transformDefinitionOwner(t,nil)
    typ:=foreign.CatalogRead().Get(other.Transforms.At(0).Pair).(*catalog.Transform).SQLType
    if typ==original.Host.Layout.Inputs[0].Type {t.Fatal("foreign fixture did not supply a distinct genuine type generation")}
    layout:=*original.Host.Layout;formals:=append([]proc.Formal(nil),layout.Formals...)
    // Inject a genuine foreign generation into a coherent private layout;
    // Usage() preserves its valid modifier/collation declaration shape.
    // The original accepted source stays valid and the refresh must reject.
    formals[0].Usage=typ.Usage()
    if err:=layout.SetFormals(formals);err!=nil {t.Fatal(err)}
    host:=*original.Host;if err:=host.SetLayout(&layout);err!=nil {t.Fatal(err)};bad.Host=&host
   case "epoch":sourceState.SetEpoch(source.Epoch()+1);targetState.SetEpoch(source.Epoch()+1)
   case "tombstone":sourceState.RemoveDescriptor(original.Address())
   }
   if kind!="tombstone" {sourceState.SetDescriptor(bad.Address(),&bad);targetState.SetDescriptor(bad.Address(),&bad)}
   e.Mu.Lock()
   result,err:=stored.RefreshTransformRoutines(source.WithState(sourceState),published.WithState(targetState),pair,e.Catalog.RevisionLocked)
   e.Mu.Unlock()
   if err==nil || result!=nil {t.Fatal("malformed original repaired by mapping",kind)}
   if e.CatalogRead().State()!=published.State() || source.Get(original.Address())!=original || published.Get(pair.Address())!=pair {t.Fatal("negative source validation mutated actual published/retained authority")}
   if original.Revision()==0 {t.Fatal("negative fixture mutated real source revision")}
  })
 }
}
