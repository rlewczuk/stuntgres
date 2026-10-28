package stored

import (
 "fmt"
 "strings"
 "stuntgres/internal/engine/catalog"
 "stuntgres/internal/engine/exec"
 "stuntgres/internal/engine/expr"
 "stuntgres/internal/engine/extension"
 "stuntgres/internal/engine/functions"
 "stuntgres/internal/sqltype"
)

func ltreePathFormalSignature(formals [8]*sqltype.Descriptor, count int) catalog.ProcedureSignature {
 var signature strings.Builder
 for i, formal := range formals[:count] { if i != 0 { signature.WriteByte(',') }; fmt.Fprintf(&signature,"%d",formal.ID()) }
 return catalog.ProcedureSignature(signature.String())
}
func validateCapturedLtreePath(call functions.LtreePath, captured *catalog.Procedure) error {
 if err := call.Validate(); err != nil { return err }
 symbol := extension.LtreePathProcedureSymbol(call.Operation)
 if captured == nil || call.RoutineReference != captured || captured.Revision() == 0 || call.Ref.Address != captured.Address() || captured.Implementation != extension.StockLtree || captured.Builtin != nil || captured.SQL != nil || captured.Foreign != nil || captured.RoutineKind != catalog.FunctionRoutine || !captured.Strict || captured.Volatility != expr.Immutable || captured.Signature != ltreePathFormalSignature(call.Formals,call.Count) || sqltype.ID(captured.Result) != call.Usage.Type.ID() || captured.Display != (catalog.RoutineDisplay{Language:catalog.CRoutineLanguage,Source:symbol,Binary:"$libdir/ltree",HasBinary:true}) || captured.Installed != (catalog.InstalledRoutineDeclaration{}) { return fmt.Errorf("stuntgres: invalid captured ltree path declaration") }
 return nil
}
func validateLtreePathDeclaration(view catalog.Read, original *catalog.Procedure, call functions.LtreePath, types func(*sqltype.Descriptor)*sqltype.Descriptor) (*catalog.Procedure,error) {
 invalid := func() (*catalog.Procedure,error) { return nil,fmt.Errorf("stuntgres: invalid stored ltree path authority") }
 if call.Ref.Epoch != view.Epoch() { return invalid() }
 current,symbol,err := currentInstalledProcedure(view,call.Ref,original); if err != nil { return nil,err }
 if current.Implementation != extension.StockLtree || symbol != extension.LtreePathProcedureSymbol(call.Operation) { return invalid() }
 if err := extension.StockLtree.ValidateProcedure(view,current); err != nil { return nil,err }
 operation,ok := extension.LtreePathProcedure(view,current); if !ok || operation != call.Operation { return invalid() }
 installation,ok := view.Extension(current.Extension)
 if !ok || int(call.Member) >= len(installation.Members) || installation.Members[call.Member] != current.Address() { return invalid() }
 var formals [8]*sqltype.Descriptor
 for i, formal := range call.Formals[:call.Count] {
  mapped := types(formal); if mapped == nil { return invalid() }
  actual,ok := view.LookupType(mapped.ID()); if !ok || actual != mapped || types(call.Args[i].ResultUsage().Type) != mapped { return invalid() }
  formals[i] = mapped
 }
 result,ok := view.LookupType(sqltype.ID(current.Result))
 if !ok || result != types(call.Usage.Type) || call.Operation.Validate(formals,call.Count,result) != nil || current.Signature != ltreePathFormalSignature(formals,call.Count) { return invalid() }
 return current,nil
}
func remapLtreePathRoutine(view catalog.Read, reader catalog.DefinitionReader, captured,original *catalog.Procedure, call functions.LtreePath, types func(*sqltype.Descriptor)*sqltype.Descriptor) error {
 if captured == nil || original == nil || captured.Revision() != original.Revision() { return fmt.Errorf("stuntgres: invalid remapped ltree path original") }
 source := call; source.Ref.Address = captured.Address()
 if err := validateCapturedLtreePath(source,captured); err != nil { return err }
 descriptor,_ := catalog.DescriptorAt(view,reader,call.Ref.Address)
 current,ok := descriptor.(*catalog.Procedure)
 if !ok || !exec.SameRoutineDeclaration(current,original) { return fmt.Errorf("stuntgres: missing remapped ltree path declaration") }
 unchanged := original == captured
 for _, formal := range call.Formals[:call.Count] { unchanged = unchanged && types(formal) == formal }
 if unchanged && original.Revision() > current.Revision() { return fmt.Errorf("stuntgres: future ltree path declaration") }
 _,err := validateLtreePathDeclaration(view,original,call,types); return err
}
func currentLtreePath(view catalog.Read, call functions.LtreePath) (*catalog.Procedure,error) {
 original,ok := call.RoutineReference.(*catalog.Procedure); if !ok { return nil,fmt.Errorf("stuntgres: invalid ltree path original") }
 if err := validateCapturedLtreePath(call,original); err != nil { return nil,err }
 return validateLtreePathDeclaration(view,original,call,func(t *sqltype.Descriptor)*sqltype.Descriptor { return t })
}
