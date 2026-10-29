package enginetests_test

import (
 "bytes"
 "errors"
 "slices"
 "testing"

 "stuntgres/internal/engine"
 "stuntgres/internal/engine/catalog"
 "stuntgres/internal/engine/enginetest"
 "stuntgres/internal/engine/extension"
 "stuntgres/internal/engine/ids"
 "stuntgres/internal/engine/proc"
 "stuntgres/internal/engine/stored"
)

const declarationLongitudeGolden="RETURN degrees(atan2(cube_ll_coord(($1)::cube, 2), cube_ll_coord(($1)::cube, 1)))\n"

func declarationLongitudeTarget(t *testing.T, read catalog.Read) (*catalog.Extension,*catalog.Procedure) {
 t.Helper()
 ext,ok:=read.Extension(read.NameAddress(ids.NameKey{ClassID:ids.ExtensionClassID,Name:"earthdistance"}))
 if !ok || !ext.Installation.Valid() {t.Fatal("missing genuine Earth installation")}
 target,ok:=read.Procedure(ext.Installation.Members[7]);if !ok {t.Fatal("missing longitude producer")}
 return ext,target
}
func assertDeclarationLongitude(t *testing.T,read catalog.Read,target *catalog.Procedure) {
 t.Helper()
 got,err:=stored.RoutineDeclarationSQL(t.Context(),read,target,declarationRecipePolicy())
 if err!=nil || got!=declarationLongitudeGolden {t.Fatalf("producer body=%q, %v; want %q",got,err,declarationLongitudeGolden)}
}

func TestRoutineDeclarationProducerIdentity(t *testing.T) {
 e:=engine.New()
 session:=enginetest.StartSession(t,e,nil)
 enginetest.SessionSQL(t,session,"CREATE EXTENSION cube")
 enginetest.SessionSQL(t,session,"CREATE EXTENSION earthdistance")
 base:=e.CatalogRead()
 earth,target:=declarationLongitudeTarget(t,base)
 cube:=base.MustExtension(base.NameAddress(ids.NameKey{ClassID:ids.ExtensionClassID,Name:"cube"}))
 coordinate:=base.MustProcedure(cube.Installation.Members[26])
 assertDeclarationLongitude(t,base,target)

 t.Run("reordered_membership",func(t *testing.T) {
  state:=catalog.CloneState(base.State())
  for _,ext:=range []*catalog.Extension{cube,earth} {
   changed:=*ext;changed.Members=slices.Clone(ext.Members);slices.Reverse(changed.Members)
   state.SetDescriptor(changed.Address(),&changed)
  }
  assertDeclarationLongitude(t,base.WithState(state),target)
 })
 t.Run("shared_producer_remap",func(t *testing.T) {
  mapping:=make(map[ids.ObjectAddress]ids.ObjectAddress)
  next:=ids.OID(970000)
  for _,ext:=range []*catalog.Extension{cube,earth} {
   mapping[ext.Address()]=ids.ObjectAddress{ClassID:ids.ExtensionClassID,ObjectID:next};next++
   for _,address:=range ext.Members {
    if address.ClassID==ids.ProcedureClassID {mapping[address]=ids.ObjectAddress{ClassID:address.ClassID,ObjectID:next};next++}
   }
  }
  remap:=extension.DescriptorRemapper(mapping)
  state:=catalog.CloneState(base.State())
  for _,ext:=range []*catalog.Extension{cube,earth} {
   descriptors:=append(slices.Clone(ext.Members),ext.Address())
   for _,address:=range descriptors {
    original:=base.Get(address);current:=remap(original)
    state.UnbindName(catalog.DescriptorNameKey(original))
    if current.Address()!=address {state.RemoveDescriptor(address)}
    if installed,ok:=current.(*catalog.Extension);ok {copy:=*installed;copy.Members=slices.Clone(installed.Members);slices.Reverse(copy.Members);current=&copy}
    state.SetDescriptor(current.Address(),current)
    state.BindName(catalog.DescriptorNameKey(current),current.Address())
   }
  }
  state.SetProcedureNames(catalog.RebuildProcedureNames(state.Descriptors()))
  read:=base.WithState(state)
  mapped:=read.MustProcedure(mapping[target.Address()])
  if mapped.Origin.Installation==target.Origin.Installation || mapped.Address()==target.Address() {t.Fatal("fixture did not remap genuine producer identity")}
  assertDeclarationLongitude(t,read,mapped)
  assertDeclarationLongitude(t,base,target)
 })
 t.Run("detached_current_membership",func(t *testing.T) {
  state:=catalog.CloneState(base.State())
  detached:=*coordinate;detached.Extension=ids.ObjectAddress{}
  changed:=*cube;changed.Members=slices.DeleteFunc(slices.Clone(cube.Members),func(address ids.ObjectAddress)bool{return address==coordinate.Address()})
  state.SetDescriptor(detached.Address(),&detached);state.SetDescriptor(changed.Address(),&changed)
  assertDeclarationLongitude(t,base.WithState(state),target)
 })
 for _,mode:=range []string{"missing_origin","copied_installation","wrong_producer_slot","missing_reciprocal_member","wrong_signature","foreign_body_owner"} {
  t.Run(mode,func(t *testing.T) {
   state:=catalog.CloneState(base.State())
   current:=*coordinate
   switch mode {
   case "missing_origin":current.Origin=nil
   case "copied_installation":copied:=*current.Origin.Installation;origin:=*current.Origin;origin.Installation=&copied;current.Origin=&origin
   case "wrong_producer_slot":origin:=*current.Origin;origin.Declaration++;current.Origin=&origin
   case "missing_reciprocal_member":changed:=*cube;changed.Members=slices.DeleteFunc(slices.Clone(cube.Members),func(address ids.ObjectAddress)bool{return address==coordinate.Address()});state.SetDescriptor(changed.Address(),&changed)
   case "wrong_signature":current.Signature="701,23"
   case "foreign_body_owner":
    changed:=*target
    recipe,ok:=proc.RoutineDeclarationRecipe(target.DeclarationBody);if !ok {t.Fatal("missing recipe")}
    parts:=make([]proc.DeclarationPart,recipe.Len());for i:=range recipe.Len() {parts[i]=recipe.At(i)}
    var err error;changed.DeclarationBody,err=proc.NewRoutineDeclarationBody(proc.DeclarationRecipe{Return:recipe.Return(),Parts:parts});if err!=nil {t.Fatal(err)}
    state.SetDescriptor(changed.Address(),&changed)
   }
   state.SetDescriptor(current.Address(),&current)
   read:=base.WithState(state)
   _,err:=stored.RoutineDeclarationSQL(t.Context(),read,read.MustProcedure(target.Address()),declarationRecipePolicy())
   if !errors.Is(err,ids.ErrCatalogReplaced) {t.Fatalf("invalid producer %s accepted: %v",mode,err)}
  })
 }
}

func TestRoutineDeclarationProducerRestore(t *testing.T) {
 source:=engine.New()
 session:=enginetest.StartSession(t,source,nil)
 enginetest.SessionSQL(t,session,"CREATE EXTENSION cube")
 enginetest.SessionSQL(t,session,"CREATE EXTENSION earthdistance")
 _,original:=declarationLongitudeTarget(t,source.CatalogRead())
 var dump bytes.Buffer
 if err:=engine.Dump(t.Context(),source,&dump);err!=nil {t.Fatal(err)}
 for _,phase:=range []string{"reset_restore","fresh_engine_restore"} {
  t.Run(phase,func(t *testing.T) {
   target:=source
   if phase=="reset_restore" {if err:=engine.Reset(t.Context(),target);err!=nil {t.Fatal(err)}} else {target=engine.New()}
   if err:=engine.Restore(t.Context(),target,bytes.NewReader(dump.Bytes()));err!=nil {t.Fatal(err)}
   read:=target.CatalogRead();_,routine:=declarationLongitudeTarget(t,read)
   if routine==original || routine.Origin.Installation==original.Origin.Installation {t.Fatal("restore reused source declaration authority")}
   assertDeclarationLongitude(t,read,routine)
  })
 }
}
