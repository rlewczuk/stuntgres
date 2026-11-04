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

func validateCapturedIntarraySet(call functions.IntarraySet, captured *catalog.Procedure) error {
	if err := call.Validate(); err != nil {
		return err
	}
	symbol := extension.IntarraySetProcedureSymbol(call.Operation)
	signature := catalog.ProcedureSignature(fmt.Sprintf("%d,%d", call.Formals[0].ID(), call.Formals[1].ID()))
	if captured == nil || call.RoutineReference != captured || captured.Revision() == 0 || call.Ref.Address != captured.Address() || captured.Implementation != extension.StockIntarray || captured.Builtin != nil || captured.SQL != nil || captured.Foreign != nil || captured.RoutineKind != catalog.FunctionRoutine || !captured.Strict || captured.Volatility != expr.Immutable || captured.Signature != signature || sqltype.ID(captured.Result) != call.Usage.Type.ID() || captured.Display != (catalog.RoutineDisplay{Language: catalog.CRoutineLanguage, Source: symbol, Binary: "$libdir/_int", HasBinary: true}) || captured.Installed != (catalog.InstalledRoutineDeclaration{}) {
		return fmt.Errorf("stuntgres: invalid captured intarray set declaration")
	}
	return nil
}

func validateIntarraySetDeclaration(view catalog.Read, original *catalog.Procedure, call functions.IntarraySet, types func(*sqltype.Descriptor) *sqltype.Descriptor) (*catalog.Procedure, error) {
	invalid := func() (*catalog.Procedure, error) {
		return nil, fmt.Errorf("stuntgres: invalid stored intarray set authority")
	}
	if call.Ref.Epoch != view.Epoch() {
		return invalid()
	}
	current, symbol, err := currentInstalledProcedure(view, call.Ref, original)
	if err != nil {
		return nil, err
	}
	if current.Implementation != extension.StockIntarray || symbol != extension.IntarraySetProcedureSymbol(call.Operation) {
		return invalid()
	}
	if err := extension.StockIntarray.ValidateProcedure(view, current); err != nil {
		return nil, err
	}
	operation, ok := extension.IntarraySetProcedure(view, current)
	if !ok || operation != call.Operation {
		return invalid()
	}
	installation, ok := catalog.OriginInstallation(current)
	if !ok || int(call.Member) != extension.IntarraySetProcedureStart+int(call.Operation) || current.Origin.Declaration != int(call.Member) || installation.Members[call.Member] != current.Address() {
		return invalid()
	}
	for i, formal := range call.Formals {
		mapped := types(formal)
		actual, ok := view.LookupType(mapped.ID())
		if !ok || actual != mapped || types(call.Args[i].ResultUsage().Type) != mapped {
			return invalid()
		}
	}
	result, ok := view.LookupType(sqltype.ID(current.Result))
	if !ok || result != types(call.Usage.Type) || call.Operation.Validate(types(call.Formals[0]), types(call.Formals[1]), result) != nil {
		return invalid()
	}
	signature := catalog.ProcedureSignature(fmt.Sprintf("%d,%d", types(call.Formals[0]).ID(), types(call.Formals[1]).ID()))
	if current.Signature != signature {
		return invalid()
	}
	return current, nil
}

func remapIntarraySetRoutine(view catalog.Read, reader catalog.DefinitionReader, captured, original *catalog.Procedure, call functions.IntarraySet, types func(*sqltype.Descriptor) *sqltype.Descriptor) error {
	if captured == nil || original == nil || captured.Revision() != original.Revision() {
		return fmt.Errorf("stuntgres: invalid remapped intarray set original")
	}
	source := call
	source.Ref.Address = captured.Address()
	if err := validateCapturedIntarraySet(source, captured); err != nil {
		return err
	}
	descriptor, _ := catalog.DescriptorAt(view, reader, call.Ref.Address)
	current, ok := descriptor.(*catalog.Procedure)
	if !ok || !exec.SameRoutineDeclaration(current, original) {
		return fmt.Errorf("stuntgres: missing remapped intarray set declaration")
	}
	_, err := validateIntarraySetDeclaration(view, original, call, types)
	return err
}

func currentIntarraySet(view catalog.Read, call functions.IntarraySet) (*catalog.Procedure, error) {
	original, ok := call.RoutineReference.(*catalog.Procedure)
	if !ok {
		return nil, fmt.Errorf("stuntgres: invalid intarray set original")
	}
	if err := validateCapturedIntarraySet(call, original); err != nil {
		return nil, err
	}
	return validateIntarraySetDeclaration(view, original, call, func(t *sqltype.Descriptor) *sqltype.Descriptor { return t })
}
