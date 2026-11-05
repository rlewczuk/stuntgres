package stored

import (
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/sqltype"
)

func intarraySliceSignature(formals []*sqltype.Descriptor) catalog.ProcedureSignature {
	switch len(formals) {
	case 1:
		return catalog.ProcedureSignature(fmt.Sprint(formals[0].ID()))
	case 2:
		return catalog.ProcedureSignature(fmt.Sprintf("%d,%d", formals[0].ID(), formals[1].ID()))
	case 3:
		return catalog.ProcedureSignature(fmt.Sprintf("%d,%d,%d", formals[0].ID(), formals[1].ID(), formals[2].ID()))
	}
	return ""
}

func validateCapturedIntarraySlice(call functions.IntarraySlice, captured *catalog.Procedure) error {
	if err := call.Validate(); err != nil { return err }
	symbol := extension.IntarraySliceProcedureSymbol(call.Operation)
	signature := intarraySliceSignature(call.Formals[:call.Operation.Arity()])
	if captured == nil || call.RoutineReference != captured || captured.Revision() == 0 || call.Ref.Address != captured.Address() || captured.Implementation != extension.StockIntarray || captured.Builtin != nil || captured.SQL != nil || captured.Foreign != nil || captured.RoutineKind != catalog.FunctionRoutine || !captured.Strict || captured.Volatility != expr.Immutable || captured.Signature != signature || sqltype.ID(captured.Result) != call.Usage.Type.ID() || captured.Display != (catalog.RoutineDisplay{Language: catalog.CRoutineLanguage, Source: symbol, Binary: "$libdir/_int", HasBinary: true}) || captured.Installed != (catalog.InstalledRoutineDeclaration{}) {
		return fmt.Errorf("stuntgres: invalid captured intarray slice declaration")
	}
	return nil
}

func validateIntarraySliceDeclaration(view catalog.Read, original *catalog.Procedure, call functions.IntarraySlice, types func(*sqltype.Descriptor) *sqltype.Descriptor) (*catalog.Procedure, error) {
	invalid := func() (*catalog.Procedure, error) { return nil, fmt.Errorf("stuntgres: invalid stored intarray slice authority") }
	if call.Ref.Epoch != view.Epoch() { return invalid() }
	current, symbol, err := currentInstalledProcedure(view, call.Ref, original)
	if err != nil { return nil, err }
	if current.Implementation != extension.StockIntarray || symbol != extension.IntarraySliceProcedureSymbol(call.Operation) { return invalid() }
	if err := extension.StockIntarray.ValidateProcedure(view, current); err != nil { return nil, err }
	operation, ok := extension.IntarraySliceProcedure(view, current)
	if !ok || operation != call.Operation { return invalid() }
	installation, ok := catalog.OriginInstallation(current)
	if !ok || int(call.Member) != extension.IntarraySliceProcedureStart+int(call.Operation) || current.Origin.Declaration != int(call.Member) || installation.Members[call.Member] != current.Address() { return invalid() }
	var formals [3]*sqltype.Descriptor
	n := call.Operation.Arity()
	for i := range n {
		mapped := types(call.Formals[i])
		if mapped == nil { return invalid() }
		actual, ok := view.LookupType(mapped.ID())
		if !ok || actual != mapped || types(call.Args[i].ResultUsage().Type) != mapped { return invalid() }
		formals[i] = mapped
	}
	result, ok := view.LookupType(sqltype.ID(current.Result))
	if !ok || result != types(call.Usage.Type) || call.Operation.Validate(formals[:n], result) != nil || current.Signature != intarraySliceSignature(formals[:n]) { return invalid() }
	return current, nil
}

func remapIntarraySliceRoutine(view catalog.Read, reader catalog.DefinitionReader, captured, original *catalog.Procedure, call functions.IntarraySlice, types func(*sqltype.Descriptor) *sqltype.Descriptor) error {
	if captured == nil || original == nil || captured.Revision() != original.Revision() { return fmt.Errorf("stuntgres: invalid remapped intarray slice original") }
	source := call
	source.Ref.Address = captured.Address()
	if err := validateCapturedIntarraySlice(source, captured); err != nil { return err }
	descriptor, _ := catalog.DescriptorAt(view, reader, call.Ref.Address)
	current, ok := descriptor.(*catalog.Procedure)
	if !ok || !exec.SameRoutineDeclaration(current, original) { return fmt.Errorf("stuntgres: missing remapped intarray slice declaration") }
	_, err := validateIntarraySliceDeclaration(view, original, call, types)
	return err
}

func currentIntarraySlice(view catalog.Read, call functions.IntarraySlice) (*catalog.Procedure, error) {
	original, ok := call.RoutineReference.(*catalog.Procedure)
	if !ok { return nil, fmt.Errorf("stuntgres: invalid intarray slice original") }
	if err := validateCapturedIntarraySlice(call, original); err != nil { return nil, err }
	return validateIntarraySliceDeclaration(view, original, call, func(t *sqltype.Descriptor) *sqltype.Descriptor { return t })
}
