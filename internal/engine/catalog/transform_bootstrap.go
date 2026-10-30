package catalog

import "stuntgres/internal/engine/host"

// TransformBootstrapEquivalent recognizes only the actual registered directional
// selection already reconstructed by installation. Identity changes of callback
// names do not change their runtime capabilities. Mixed, cleared and nonbootstrap
// selections are never inferred from a language/type name or a Bootstrap label.
func TransformBootstrapEquivalent(v Read,d *Transform) bool {
 if d==nil || d.SQLDefined || d.Runtime==nil || d.From.ObjectID==0 || d.To.ObjectID==0 || (TransformProvider{}).Validate(v,d)!=nil { return false }
 var selected *host.Conversion
 for _,address:=range [...]struct{from bool; present bool}{{true,d.From.ObjectID!=0},{false,d.To.ObjectID!=0}} {
  if !address.present { continue }
  id:=d.To; if address.from { id=d.From }
  p,ok:=v.Procedure(id)
  if !ok || p.HostConversion==nil || ValidateHostConversion(v,p)!=nil { return false }
  c:=p.HostConversion
  if c.Token.Runtime()!=d.Runtime || !c.Token.Definition().Bootstrap || c.Type!=d.SQLType || c.From!=address.from { return false }
  if selected!=nil && selected!=c.Token { return false }
  selected=c.Token
 }
 if selected==nil { return false }
 definition:=selected.Definition()
 return (d.From.ObjectID!=0)==(definition.FromSQL!=nil) && (d.To.ObjectID!=0)==(definition.ToSQL!=nil)
}
