package core_test

import (
 "bytes"
 "context"
 "testing"

 "stuntgres/internal/engine/catalog"
 "stuntgres/internal/engine/enginetest"
 "stuntgres/internal/engine/exec"
 "stuntgres/internal/engine/ids"
 "stuntgres/internal/engine"
)

func TestRoutineTransformAssociationOwnership(t *testing.T) {
 sourceCalls,targetCalls:=0,0
 source,original:=hostOwnerEngine(t,func()error{sourceCalls++;return nil})
 enginetest.Run(t,source,"CREATE OR REPLACE FUNCTION owner_host_fn(v hstore) RETURNS hstore LANGUAGE owner_host TRANSFORM FOR TYPE hstore, FOR TYPE hstore[] AS 'increment'")
 enginetest.Run(t,source,"CREATE PROCEDURE owner_transform_call(INOUT v hstore) LANGUAGE owner_host TRANSFORM FOR TYPE hstore AS 'increment'")
 read:=source.CatalogRead()
 current,_:=read.Procedure(original.Address())
 if current.Transforms.Len()!=2||current.Display.TransformTypes.Len()!=2||current.Transforms.At(0)!=current.Transforms.At(1){t.Fatal("normalized duplicate association order lost")}
 if original.Transforms.Len()!=1||original.Display.TransformTypes.Len()!=1{t.Fatal("replacement mutated retained association")}
 entries:=[]catalog.RoutineTransform{current.Transforms.At(0),current.Transforms.At(1)}
 detached:=catalog.NewRoutineTransforms(entries)
 entries[0].Pair=ids.ObjectAddress{}
 if detached.At(0)!=current.Transforms.At(0){t.Fatal("association constructor borrowed caller backing")}
 for _,corrupt:=range []func(*catalog.Procedure){
  func(p *catalog.Procedure){p.Display.TransformTypes=nil},
  func(p *catalog.Procedure){p.Transforms=nil},
  func(p *catalog.Procedure){bad:=p.Transforms.At(0);bad.Pair=ids.ObjectAddress{};p.Transforms=catalog.NewRoutineTransforms([]catalog.RoutineTransform{bad,p.Transforms.At(1)})},
  func(p *catalog.Procedure){bad:=p.Transforms.At(0);bad.Revision++;p.Transforms=catalog.NewRoutineTransforms([]catalog.RoutineTransform{bad,p.Transforms.At(1)})},
  func(p *catalog.Procedure){bad:=p.Transforms.At(0);bad.Type=read.MustType(ids.ObjectAddress{ClassID:ids.TypeClassID,ObjectID:23}).SQLType;p.Transforms=catalog.NewRoutineTransforms([]catalog.RoutineTransform{bad,p.Transforms.At(1)})},
 } {
  bad:=*current;corrupt(&bad)
  state:=catalog.CloneState(read.State());state.SetDescriptor(bad.Address(),&bad)
  if err:=catalog.ValidateHostProcedure(source.Catalog.Read(state),&bad);err==nil{t.Fatal("forged association accepted")}
 }
 var stream bytes.Buffer
 if err:=engine.Dump(context.Background(),source,&stream);err!=nil{t.Fatal(err)}
 target,previous:=hostOwnerEngine(t,func()error{targetCalls++;return nil})
 oldView:=target.CatalogRead()
 for range 2 {
  if err:=engine.Restore(context.Background(),target,bytes.NewReader(stream.Bytes()));err!=nil{t.Fatal(err)}
  destination:=target.CatalogRead()
  var restored *catalog.Procedure
  for _,descriptor:=range destination.Descriptors(){if p,ok:=descriptor.(*catalog.Procedure);ok&&p.Name()==current.Name(){restored=p;break}}
  if restored==nil||restored.Transforms==current.Transforms||restored.Transforms==previous.Transforms||restored.Display.TransformTypes==current.Display.TransformTypes||restored.Transforms.Len()!=2{t.Fatal("restore borrowed source/retained association backing")}
  for i:=range restored.Transforms.Len(){
   entry:=restored.Transforms.At(i)
   pair,ok:=destination.Get(entry.Pair).(*catalog.Transform)
   if !ok||entry.Type!=restored.Host.Layout.Inputs[0].Type||entry.Type==current.Transforms.At(i).Type||pair.SQLType!=entry.Type||pair.Runtime!=restored.Host.Program.Runtime()||entry.Revision!=pair.Revision(){t.Fatal("association lost destination type/pair/token authority")}
   if restored.Display.TransformTypes.At(i)!=ids.OID(entry.Type.ID()){t.Fatal("display diverged from authority")}
  }
  if _,err:=exec.ValidateStoredRoutineDescriptor(destination,restored);err!=nil{t.Fatal(err)}
  if oldView.Get(previous.Address())!=previous||previous.Transforms.Len()==0{t.Fatal("restore changed retained catalog")}
  values:=hostSnapshotRow(t,target,"CALL owner_transform_call('a=>7,b=>NULL'::hstore)")
  if len(values)!=1||values[0].IsNull(){t.Fatal("CALL did not return a nonnull output member")}
  entries,err:=values[0].OwnedHstoreEntries(context.Background(),values[0].Type());if err!=nil{t.Fatal(err)}
  want:=map[string]string{"a":"8","c":"3"};nulls:=0
  for _,entry:=range entries{if entry.Key=="b"&&entry.Null{nulls++;continue};if want[entry.Key]!=entry.Value||entry.Null{t.Fatal("wrong genuine CALL map",entries)};delete(want,entry.Key)}
  if len(want)!=0||nulls!=1||len(entries)!=3{t.Fatal("CALL map multiplicity",entries)}
  if sourceCalls!=0||targetCalls==0{t.Fatal("execution leaked source tokens or omitted destination callbacks")}
  previous,oldView=restored,destination
 }
}
