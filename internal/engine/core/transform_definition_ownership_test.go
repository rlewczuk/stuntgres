package core_test

import (
 "bytes"
 "context"
 "errors"
 "reflect"
 "slices"
 "testing"

 "stuntgres/internal/engine"
 "stuntgres/internal/engine/catalog"
 "stuntgres/internal/engine/core"
 "stuntgres/internal/engine/enginetest"
 "stuntgres/internal/engine/host"
 "stuntgres/internal/engine/ids"
)

func transformDefinitionOwner(t *testing.T,check func()error)(*core.Engine,*catalog.Procedure) {
 t.Helper()
 definition:=hostOwnerDefinition(check)
 extra:=definition.Conversions[0]
 extra.ID,extra.Bootstrap="independent",false
 extra.FromName=host.Name{Schema:"public",Name:"owner_other_from"}
 extra.ToName=host.Name{Schema:"public",Name:"owner_other_to"}
 definition.Conversions=append(definition.Conversions,extra)
 runtime,err:=host.New(definition); if err!=nil { t.Fatal(err) }
 e:=engine.New()
 if err=e.InstallHostDefinitions([]*host.Runtime{runtime}); err!=nil { t.Fatal(err) }
 for _,q:=range []string{
  "CREATE EXTENSION hstore",
  "ALTER FUNCTION owner_host_from(internal) IMMUTABLE",
  "ALTER FUNCTION owner_host_to(internal) IMMUTABLE",
  "ALTER FUNCTION owner_other_from(internal) STABLE",
  "ALTER FUNCTION owner_other_to(internal) STABLE",
  "CREATE FUNCTION owner_transform(v hstore DEFAULT 'a=>7,b=>NULL') RETURNS hstore LANGUAGE owner_host TRANSFORM FOR TYPE hstore, FOR TYPE hstore AS 'increment'",
  "CREATE PROCEDURE owner_transform_call(INOUT v hstore) LANGUAGE owner_host TRANSFORM FOR TYPE hstore AS 'increment'",
 } { enginetest.Run(t,e,q) }
 return e,checkedDefaultOwnerProcedure(t,e.CatalogRead(),"owner_transform")
}
const transformOwnerReplacement="CREATE OR REPLACE TRANSFORM FOR hstore LANGUAGE owner_host (FROM SQL WITH FUNCTION owner_other_from(internal), TO SQL WITH FUNCTION owner_other_to(internal))"

func TestTransformReplacementOwnership(t *testing.T) {
 callbacks:=0
 e,old:=transformDefinitionOwner(t,func()error{callbacks++; return nil})
 read:=e.CatalogRead()
 pair:=read.Get(old.Transforms.At(0).Pair).(*catalog.Transform)
 association:=old.Transforms
 hostOwner,layout,defaults:=old.Host,old.Host.Layout,old.SQLDefaults
 converters,pairs,tokens:=slices.Clone(hostOwner.Converters),slices.Clone(hostOwner.Pairs),slices.Clone(hostOwner.Tokens)
 formals:=slices.Clone(layout.Formals)
 deps:=slices.Clone(defaults.Dependencies())
 entries:=[]catalog.RoutineTransform{association.At(0),association.At(1)}
 s:=enginetest.StartSession(t,e,nil)
 enginetest.SessionSQL(t,s,"BEGIN")
 defer enginetest.SessionSQL(t,s,"ROLLBACK")
 cycle:=s.NewProtocolCycle()
 command:=func(fn func(*core.StatementScope)error){preparedFoldProtocolCommand(t,e,s,cycle,fn)}
 command(func(scope *core.StatementScope)error{_,err:=scope.ParseProtocol("transform_owned","SELECT COALESCE(-(7::integer),0) AS folded,owner_transform() AS v",nil);return err})
 bind:=func(name string){command(func(scope *core.StatementScope)error{_,err:=scope.BindProtocolPortal(name,"transform_owned",&protocolUnitInput{data:[]byte{0,0,0,0,0,0}});return err})}
 bind("before")
 registry:=core.PayloadOf(s).PreparedSQL
 definition:=registry.Lookup("transform_owned")
 owner,analysis:=definition.Description.Folds,definition.Description.Analysis
 if owner==nil || analysis==nil { t.Fatal("missing actual prepared owners") }
 accepted:=owner.Current()
 if len(accepted.Entries)==0 { t.Fatal("missing actual accepted fold backing") }
 savedFolds:=slices.Clone(accepted.Entries)
 savedDeps:=slices.Clone(analysis.Dependencies)
 portal:=registry.Binding("before")
 graph,group:=portal.Resource.Root,*portal.Folds
 capture:=func()(catalog.Read,core.PreparedEnvironment){
  op,err:=s.Acquire(t.Context()); if err!=nil { t.Fatal(err) }; defer op.Done()
  e.Mu.Lock(); defer e.Mu.Unlock()
  auth,err:=e.AuthorizationLocked(op); if err!=nil { t.Fatal(err) }
  env,err:=e.PreparedEnvironmentLocked(auth,true); if err!=nil { t.Fatal(err) }
  return auth.Catalog,env
 }
 current,environment:=capture()
 stale,err:=core.BeginFoldPublication(&definition.Description,definition.Body,environment,current,nil)
 if err!=nil || stale==nil { t.Fatal("missing candidate publication",err) }
 enginetest.SessionSQL(t,s,transformOwnerReplacement)
 current,environment=capture()
 next,_:=current.Procedure(old.Address())
 nextPair:=current.Get(pair.Address()).(*catalog.Transform)
 if next==old || next.Revision()==old.Revision() || nextPair==pair || nextPair.Revision()==pair.Revision() { t.Fatal("replacement did not advance actual pair/consumer owners") }
 if next.Transforms==association || next.Host==hostOwner || next.Host.Layout==layout || next.SQLDefaults==defaults { t.Fatal("replacement borrowed immutable predecessor payload owner") }
 if &next.Host.Converters[0]==&hostOwner.Converters[0] || &next.Host.Pairs[0]==&hostOwner.Pairs[0] || &next.Host.Tokens[0]==&hostOwner.Tokens[0] || &next.Host.Layout.Formals[0]==&layout.Formals[0] || &next.Host.Layout.Inputs[0]==&layout.Inputs[0] { t.Fatal("replacement shares mutable declaration/converter backing") }
 for i:=range next.Transforms.Len() { if next.Transforms.At(i).Pair!=pair.Address() || next.Transforms.At(i).Revision!=nextPair.Revision() { t.Fatal("duplicate association failed to refresh") } }
 if err=catalog.ValidateHostProcedure(current,next); err!=nil { t.Fatal(err) }
 if err=next.SQLDefaults.Validate(current); err!=nil { t.Fatal(err) }
 if next.Host.Program!=hostOwner.Program || next.Owner!=old.Owner || !reflect.DeepEqual(next.ACL,old.ACL) { t.Fatal("refresh changed unrelated program/authority") }
 if !slices.Equal(hostOwner.Converters,converters) || !slices.Equal(hostOwner.Pairs,pairs) || !slices.Equal(hostOwner.Tokens,tokens) || !slices.Equal(layout.Formals,formals) || !slices.Equal(defaults.Dependencies(),deps) || association.At(0)!=entries[0] || association.At(1)!=entries[1] { t.Fatal("replacement mutated retained predecessor backing") }
 if analysis.Valid(current,environment) { t.Fatal("old accepted analysis retained changed procedure authority") }
 if err=stale.Accept(t.Context(),current,environment,current.Epoch()); !errors.Is(err,ids.ErrCatalogReplaced) { t.Fatal("stale fold candidate published",err) }
 bind("after")
 fresh:=registry.Binding("after")
 if fresh.Resource.Root==graph || fresh.Entry.Description.Analysis==analysis || !fresh.Entry.Description.Analysis.Valid(current,environment) { t.Fatal("new portal failed to adopt current declaration authority") }
 if portal.Resource.Root!=graph || portal.Folds.Generation!=group.Generation || !reflect.DeepEqual(portal.Folds.Entries,group.Entries) || !reflect.DeepEqual(analysis.Dependencies,savedDeps) || !reflect.DeepEqual(accepted.Entries,savedFolds) || &accepted.Entries[0]!=&group.Entries[0] { t.Fatal("refresh/rejection mutated actual old accepted graph/fold backing") }
 if callbacks!=0 { t.Fatal("metadata replacement invoked application callbacks") }
}

func TestTransformDefinitionPublication(t *testing.T) {
 e,old:=transformDefinitionOwner(t,nil)
 before:=e.CatalogRead()
 pair:=before.Get(old.Transforms.At(0).Pair)
 for _,q:=range []string{
  "CREATE OR REPLACE TRANSFORM FOR hstore LANGUAGE owner_host (FROM SQL WITH FUNCTION dsimple_init(internal), TO SQL WITH FUNCTION owner_other_to(internal))",
  "CREATE OR REPLACE TRANSFORM FOR hstore LANGUAGE owner_host (TO SQL WITH FUNCTION owner_other_to(internal))",
  "CREATE OR REPLACE TRANSFORM FOR hstore LANGUAGE owner_host (TO SQL WITH FUNCTION owner_other_from(internal))",
  "DROP TRANSFORM FOR hstore LANGUAGE owner_host RESTRICT",
 } {
  result,err:=enginetest.Execute(e,t.Context(),enginetest.Statement(t,q),nil)
  if result.Rows!=nil { result.Rows.Close() }
  if err==nil { t.Fatal("invalid candidate published",q) }
  current:=e.CatalogRead()
  if current.State()!=before.State() || current.Epoch()!=before.Epoch() || current.Get(pair.Address())!=pair || current.Get(old.Address())!=old { t.Fatal("rejection partially published pair/consumer/catalog generation") }
 }
 ctx,cancel:=context.WithCancel(t.Context()); cancel()
 _,err:=enginetest.Execute(e,ctx,enginetest.Statement(t,transformOwnerReplacement),nil)
 if !errors.Is(err,context.Canceled) || e.CatalogRead().State()!=before.State() { t.Fatal("canceled stage published",err) }
}

func TestTransformDefinitionRemap(t *testing.T) {
 source,original:=transformDefinitionOwner(t,nil)
 enginetest.Run(t,source,transformOwnerReplacement)
 sourceView:=source.CatalogRead()
 original,_=sourceView.Procedure(original.Address())
 sourcePair:=sourceView.Get(original.Transforms.At(0).Pair).(*catalog.Transform)
 var stream bytes.Buffer
 if err:=engine.Dump(t.Context(),source,&stream); err!=nil { t.Fatal(err) }
 destination,previous:=transformDefinitionOwner(t,nil)
 retained:=destination.CatalogRead()
 for range 2 {
  if err:=engine.Restore(t.Context(),destination,bytes.NewReader(stream.Bytes())); err!=nil { t.Fatal(err) }
  view:=destination.CatalogRead()
  next:=checkedDefaultOwnerProcedure(t,view,"owner_transform")
  pair:=view.Get(next.Transforms.At(0).Pair).(*catalog.Transform)
  if pair==sourcePair || next==original || next.Host==original.Host || next.Host.Layout==original.Host.Layout || next.Transforms==original.Transforms || next.SQLDefaults==original.SQLDefaults || next.Host.Program==original.Host.Program || pair.Runtime==sourcePair.Runtime || pair.SQLType==sourcePair.SQLType { t.Fatal("destination borrowed source catalog/type/runtime/layout/default owner") }
  if next==previous || next.Host.Layout==previous.Host.Layout || next.Transforms==previous.Transforms || &next.Host.Converters[0]==&previous.Host.Converters[0] || &next.Host.Tokens[0]==&previous.Host.Tokens[0] { t.Fatal("destination reused retained prior-generation mutable backing") }
  for i:=range next.Host.Converters {
   callback,_:=view.Procedure(next.Host.Converters[i])
   if callback.HostConversion.Token!=next.Host.Tokens[i] || callback.HostConversion.Token.Runtime()!=pair.Runtime || callback.HostConversion.Type!=pair.SQLType { t.Fatal("direction lost exact destination authority") }
  }
  if err:=catalog.ValidateHostProcedure(view,next); err!=nil { t.Fatal(err) }
  if err:=next.SQLDefaults.Validate(view); err!=nil { t.Fatal(err) }
  if retained.Get(previous.Address())!=previous || sourceView.Get(original.Address())!=original || sourceView.Get(sourcePair.Address())!=sourcePair { t.Fatal("remap mutated retained source/destination descriptors") }
  previous,retained=next,view
 }
}
