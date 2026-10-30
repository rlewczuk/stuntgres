package enginetests

import (
 "strings"
 "testing"
 "stuntgres/internal/engine/host"
)

func TestHostTransformManifest(t *testing.T){
 d:=registryDefinition()
 extra:=d.Programs[0];extra.ID="second";d.Programs=append(d.Programs,extra)
 d.Defaults=[]host.DefaultDefinition{{Type:host.Int8,FromID:"integer",ToID:"integer"}}
 r,err:=host.New(d);if err!=nil{t.Fatal(err)}
 reordered:=registryDefinition()
 reordered.Programs=[]host.ProgramDefinition{extra,reordered.Programs[0]}
 reordered.Defaults=append([]host.DefaultDefinition{},d.Defaults...)
 other,err:=host.New(reordered);if err!=nil{t.Fatal(err)}
 if r.Fingerprint()!=other.Fingerprint(){t.Fatal("declaration order changed logical manifest")}
 comment:=strings.TrimPrefix(strings.TrimSpace(r.ReplayComment()),"-- ")
 if !strings.HasPrefix(comment,"stuntgres-host-runtime-v2 "){t.Fatal("missing strict current version",comment)}
 required,recognized,err:=host.DecodeRequirement(comment)
 if err!=nil||!recognized{t.Fatal("current prerequisite",recognized,err)}
 if err=host.ValidateRequirement(required,[]*host.Runtime{other});err!=nil{t.Fatal(err)}
 old:=strings.Replace(comment,"stuntgres-host-runtime-v2 ","stuntgres-host-runtime-v1 ",1)
 if _,recognized,err=host.DecodeRequirement(old);!recognized||err==nil{t.Fatal("old selector-bearing envelope accepted",recognized,err)}
 for _,modify:=range []func(*host.LanguageDefinition){
  func(d *host.LanguageDefinition){d.Provider="different"},
  func(d *host.LanguageDefinition){d.ABIVersion="different"},
  func(d *host.LanguageDefinition){d.Programs[0].Version="different"},
  func(d *host.LanguageDefinition){d.Programs[0].Inputs[0]=host.Text},
  func(d *host.LanguageDefinition){d.Conversions[0].FromSQL=nil;d.Conversions[0].FromName=host.Name{};d.Defaults[0].FromID=""},
  func(d *host.LanguageDefinition){d.Defaults[0].FromID=""},
 }{
  changed:=r.Definition();modify(&changed)
  incompatible,err:=host.New(changed);if err!=nil{t.Fatal(err)}
  if incompatible.Fingerprint()==r.Fingerprint(){t.Fatal("logical incompatibility lost")}
  if err=host.ValidateRequirement(required,[]*host.Runtime{incompatible});err==nil{t.Fatal("incompatible prerequisite accepted")}
 }
}
