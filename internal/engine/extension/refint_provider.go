package extension

import (
 "fmt"
 "stuntgres/internal/engine/authorization"
 "stuntgres/internal/engine/catalog"
 "stuntgres/internal/engine/expr"
 "stuntgres/internal/engine/ids"
 "stuntgres/internal/sqlstate"
)

// RefintProvider owns the two immutable stock declarations, not their SQL names.
type RefintProvider struct{ control catalog.ExtensionControl }
var StockRefint = &RefintProvider{control: catalog.ExtensionControl{Version: "1.0", Superuser: true, Relocatable: true, Comment: new("functions for implementing referential integrity (obsolete)")}}
var refintSymbols = [...]string{"check_primary_key", "check_foreign_key"}
func (p *RefintProvider) Control() catalog.ExtensionControl { return p.control.Detached() }
func (*RefintProvider) Versions() catalog.ExtensionVersions { return catalog.ExtensionVersions{Bases: []string{"1.0"}} }
func RefintProviderOf(d *catalog.Procedure) *RefintProvider { if d == nil { return nil }; p, _ := d.Implementation.(*RefintProvider); return p }
func (p *RefintProvider) ValidateProcedure(v catalog.Read, d *catalog.Procedure) error { return ValidateProcedureOrigin(v,d,p) }
func (p *RefintProvider) ValidateExtension(v catalog.Read, e *catalog.Extension) error { return ValidateCurrentInstallation(v,e,p) }
func (p *RefintProvider) SupportsDMLTrigger(d *catalog.Procedure) bool {
 return p == StockRefint && d != nil && d.Implementation == p && d.Origin.Valid(d.Address()) && d.Origin.Installation.Implementation == p && d.Origin.Declaration < len(refintSymbols) && d.SQL == nil && d.Builtin == nil && d.RoutineKind == catalog.FunctionRoutine && d.Result == 2279 && d.Signature == ""
}
func (p *RefintProvider) validateRecipe(v catalog.Read, e *catalog.Extension) error {
 if p != StockRefint || !currentManifest(v,e) || e.Name() != "refint" || e.Version != "1.0" || len(e.Members) != 2 { return fmt.Errorf("stuntgres: invalid refint manifest") }
 for _, a := range e.Members { d, ok := v.Procedure(a); if !ok { return fmt.Errorf("stuntgres: invalid refint member") }; if _, err := ProcedureSymbol(v,d); err != nil { return err } }
 return nil
}
func (p *RefintProvider) Stage(s Stage, e *catalog.Extension, owner authorization.PrincipalRef) error {
 for slot, name := range refintSymbols {
  oid, err := s.AllocateOID(); if err != nil { return err }
  revision, err := s.Revision(); if err != nil { return err }
  a := ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: oid}
  d := &catalog.Procedure{ObjectAddress:a, ObjectNamespace:e.TargetNamespace, Extension:e.Address(), ObjectRevision:revision, ObjectName:name, RoutineKind:catalog.FunctionRoutine, Owner:owner, Result:2279, Volatility:expr.Volatile, Installed:catalog.InstalledRoutineDeclaration{Parallel:'u'}, Display:catalog.RoutineDisplay{Language:catalog.CRoutineLanguage, Source:name, Binary:"$libdir/refint", HasBinary:true}, Implementation:p}
  if s.LookupName(catalog.DescriptorNameKey(d)) != (ids.ObjectAddress{}) {
   return &sqlstate.Error{Severity:"ERROR", SeverityUnlocalized:"ERROR", Code:"42723", Message:fmt.Sprintf("function %q already exists with same argument types",name), Context:fmt.Sprintf("SQL statement \"CREATE FUNCTION %s()\nRETURNS trigger\nAS '$libdir/refint'\nLANGUAGE C\"\nextension script file \"refint--1.0.sql\", near line %d",name,6+5*slot)}
  }
  if err := s.Create(d); err != nil { return err }; e.Members = append(e.Members,a)
 }
 return nil
}
