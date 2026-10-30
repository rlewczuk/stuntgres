package core_test

import (
 "testing"

 "stuntgres/internal/engine"
 "stuntgres/internal/engine/catalog"
 "stuntgres/internal/engine/enginetest"
 "stuntgres/internal/engine/host"
 "stuntgres/internal/engine/ids"
 "stuntgres/internal/sqltype"
)

func TestTransformDefinitionAuthority(t *testing.T) {
 definition:=hostOwnerDefinition(func() error { t.Error("metadata validation invoked a callback"); return nil })
 extra:=definition.Conversions[0]
 extra.ID,extra.Bootstrap="independent",false
 extra.FromName=host.Name{Schema:"public",Name:"owner_independent_from"}
 extra.ToName=host.Name{Schema:"public",Name:"owner_independent_to"}
 definition.Conversions=append(definition.Conversions,extra)
 runtime,err:=host.New(definition); if err!=nil { t.Fatal(err) }
 foreign,err:=host.New(definition); if err!=nil { t.Fatal(err) }
 e:=engine.New()
 if err=e.InstallHostDefinitions([]*host.Runtime{runtime}); err!=nil { t.Fatal(err) }
 enginetest.Run(t,e,"CREATE EXTENSION hstore")
 read:=e.CatalogRead()
 var pair *catalog.Transform
 var independent *catalog.Procedure
 for _,d:=range read.Descriptors() {
  if p,ok:=d.(*catalog.Transform); ok { pair=p }
  if p,ok:=d.(*catalog.Procedure); ok && p.Name()=="owner_independent_from" { independent=p }
 }
 if pair==nil || independent==nil { t.Fatal("real registered pair/callback absent") }
 if pair.From==independent.Address() { t.Fatal("nonbootstrap callback unexpectedly bootstrapped") }
 t.Run("independent-directions",func(t *testing.T) {
  state:=catalog.CloneState(read.State())
  replacement:=*pair
  replacement.From=independent.Address()
  state.SetDescriptor(replacement.Address(),&replacement)
  candidate:=e.Catalog.Read(state)
  if err:=(catalog.TransformProvider{}).Validate(candidate,&replacement); err!=nil { t.Fatalf("genuine same-runtime/type independent direction rejected: %v",err) }
  if current:=read.Get(pair.Address()); current!=pair || pair.From==replacement.From { t.Fatal("candidate mutated retained pair") }
  if current,_:=read.Procedure(independent.Address()); current!=independent { t.Fatal("candidate replaced retained callback") }
 })
 for _,tc:=range []struct{name string; mutate func(*catalog.Transform)}{
  {"foreign-runtime",func(p *catalog.Transform){p.Runtime=foreign}},
  {"wrong-direction",func(p *catalog.Transform){p.From=p.To}},
  {"missing-callback",func(p *catalog.Transform){p.From=ids.ObjectAddress{ClassID:ids.ProcedureClassID,ObjectID:999999}}},
  {"wrong-class",func(p *catalog.Transform){p.From.ClassID=ids.TypeClassID}},
  {"subobject",func(p *catalog.Transform){p.From.SubID=1}},
  {"no-direction",func(p *catalog.Transform){p.From=ids.ObjectAddress{}; p.To=ids.ObjectAddress{}}},
  {"cloned-type",func(p *catalog.Transform){typ,err:=sqltype.Hstore(p.SQLType.ID(),"hstore","public.hstore"); if err!=nil { t.Fatal(err) }; p.SQLType=typ}},
 } {
  t.Run(tc.name,func(t *testing.T) {
   state:=catalog.CloneState(read.State())
   forged:=*pair; tc.mutate(&forged)
   state.SetDescriptor(forged.Address(),&forged)
   if err:=(catalog.TransformProvider{}).Validate(e.Catalog.Read(state),&forged); err==nil { t.Fatal("forged directional authority accepted") }
   if read.Get(pair.Address())!=pair { t.Fatal("rejected candidate changed retained state") }
  })
 }
 t.Run("foreign-direction-token",func(t *testing.T) {
  state:=catalog.CloneState(read.State())
  callback,_:=read.Procedure(pair.From)
  retained:=callback.HostConversion
  token:=retained.Token
  forged:=*callback
  capability:=*callback.HostConversion
  capability.Token=foreign.Conversion(definition.Conversions[0].ID)
  if capability.Token==nil || capability.Token==token {t.Fatal("missing genuine foreign registered token witness")}
  forged.HostConversion=&capability
  state.SetDescriptor(forged.Address(),&forged)
  if err:=catalog.ValidateHostConversion(e.Catalog.Read(state),&forged); err==nil { t.Fatal("foreign registered token gained installed callback authority") }
  if callback.HostConversion!=retained || retained.Token!=token { t.Fatal("validation mutated retained capability") }
 })
}
