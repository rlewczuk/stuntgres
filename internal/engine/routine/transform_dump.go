package routine

import (
 "fmt"
 "strings"

 "stuntgres/internal/engine/catalog"
 "stuntgres/internal/engine/dump"
 "stuntgres/internal/engine/ids"
)

// TransformDumpStatement renders catalog choices, never runtime tokens. Exact
// authentic bootstrap selections are supplied by installation; importantly,
// their unannotated VOLATILE procedures must not pass through public CREATE.
func TransformDumpStatement(view catalog.Read,pair *catalog.Transform,replace bool)(dump.Statement,error) {
 if err:=(catalog.TransformProvider{}).Validate(view,pair); err!=nil { return dump.Statement{},err }
 if catalog.TransformBootstrapEquivalent(view,pair) { return dump.Statement{Owner:"postgres"},nil }
 language,ok:=view.Get(pair.Language).(*catalog.Language)
 if !ok { return dump.Statement{},fmt.Errorf("stuntgres: missing transform language") }
 var sql strings.Builder
 sql.WriteString("CREATE ")
 if replace { sql.WriteString("OR REPLACE ") }
 sql.WriteString("TRANSFORM FOR ")
 sql.WriteString(catalog.TypeSpelling(view,nil,pair.Type.ObjectID))
 sql.WriteString(" LANGUAGE "+catalog.QuoteIdentifier(language.Name())+" (")
 written:=false
 for _,direction:=range [...]struct{address ids.ObjectAddress;name string}{{pair.From,"FROM"},{pair.To,"TO"}} {
  if direction.address.ObjectID==0 { continue }
  p,ok:=view.Procedure(direction.address)
  if !ok { return dump.Statement{},fmt.Errorf("stuntgres: missing transform callback") }
  ns,ok:=view.Namespace(p.Namespace())
  if !ok { return dump.Statement{},fmt.Errorf("stuntgres: missing transform callback namespace") }
  if written { sql.WriteString(", ") }; written=true
  sql.WriteString(direction.name+" SQL WITH FUNCTION "+catalog.QuoteIdentifier(ns.Name())+"."+catalog.QuoteIdentifier(p.Name())+"(pg_catalog.internal)")
 }
 sql.WriteString(");\n")
 return dump.Statement{Owner:"postgres",SQL:sql.String()},nil
}
