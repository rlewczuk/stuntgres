package stored

import (
	"fmt"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

func hstoreRecordDeclaration(call functions.HstoreRecord, hstore *sqltype.Descriptor) (catalog.ProcedureSignature, ids.OID) {
	if call.Arity() == 1 {
		return "2249", ids.OID(hstore.ID())
	}
	return catalog.ProcedureSignature("2283," + string(extension.HstoreSignature(hstore.ID()))), 2283
}
func validateCapturedHstoreRecord(call functions.HstoreRecord, captured *catalog.Procedure) error {
	if err := call.Validate(); err != nil {
		return err
	}
	signature, result := hstoreRecordDeclaration(call, call.Hstore)
	if captured == nil || call.RoutineReference != captured || captured.Revision() == 0 || call.Ref.Address != captured.Address() || captured.Implementation != extension.StockHstore || captured.Builtin != nil || captured.SQL != nil || captured.Foreign != nil || captured.RoutineKind != catalog.FunctionRoutine || captured.Strict || captured.Volatility != expr.Immutable || captured.Signature != signature || captured.Result != result || captured.Display != (catalog.RoutineDisplay{Language: catalog.CRoutineLanguage, Source: call.Symbol(), Binary: "$libdir/hstore", HasBinary: true}) || captured.Installed != (catalog.InstalledRoutineDeclaration{}) || captured.Language != (ids.ObjectAddress{}) || captured.SQLBody != nil || captured.SQLDefaults != nil || len(captured.Body) != 0 {
		return fmt.Errorf("stuntgres: invalid captured hstore record declaration")
	}
	return nil
}
func validateHstoreRecordDeclaration(view catalog.Read, original *catalog.Procedure, call functions.HstoreRecord, types func(*sqltype.Descriptor) *sqltype.Descriptor) (*catalog.Procedure, error) {
	invalid := func() (*catalog.Procedure, error) {
		return nil, fmt.Errorf("stuntgres: invalid stored hstore record authority")
	}
	if call.Ref.Epoch != view.Epoch() {
		return invalid()
	}
	current, symbol, err := currentInstalledProcedure(view, call.Ref, original)
	if err != nil {
		return nil, err
	}
	if current.Implementation != extension.StockHstore || symbol != call.Symbol() {
		return invalid()
	}
	installation, ok := view.Extension(current.Extension)
	if !ok || len(installation.Members) != extension.HstoreMemberCount || int(call.Member) >= len(installation.Members) || installation.Members[call.Member] != current.Address() {
		return invalid()
	}
	base, ok := view.Type(installation.Members[extension.HstoreTypeSlot])
	if !ok || base.SQLType != types(call.Hstore) {
		return invalid()
	}
	for i := range call.Arity() {
		mapped := types(call.Formals[i])
		usage := call.Args[i].ResultUsage()
		usage.Type = types(usage.Type)
		if mapped == nil || mapped != usage.Type || !ValidExpressionUsage(view, usage) {
			return invalid()
		}
	}
	resultUsage := call.Usage
	resultUsage.Type = types(resultUsage.Type)
	if !ValidExpressionUsage(view, resultUsage) {
		return invalid()
	}
	signature, result := hstoreRecordDeclaration(call, base.SQLType)
	if current.Signature != signature || current.Result != result {
		return invalid()
	}
	if err := extension.StockHstore.ValidateProcedure(view, current); err != nil {
		return nil, err
	}
	return current, nil
}
func remapHstoreRecordRoutine(view catalog.Read, reader catalog.DefinitionReader, captured, original *catalog.Procedure, call functions.HstoreRecord, types func(*sqltype.Descriptor) *sqltype.Descriptor) error {
	if captured == nil || original == nil || captured.Revision() != original.Revision() {
		return fmt.Errorf("stuntgres: invalid remapped hstore record original")
	}
	source := call
	source.Ref.Address = captured.Address()
	if err := validateCapturedHstoreRecord(source, captured); err != nil {
		return err
	}
	descriptor, _ := catalog.DescriptorAt(view, reader, call.Ref.Address)
	current, ok := descriptor.(*catalog.Procedure)
	if !ok || !exec.SameRoutineDeclaration(current, original) {
		return fmt.Errorf("stuntgres: missing remapped hstore record declaration")
	}
	_, err := validateHstoreRecordDeclaration(view, original, call, types)
	return err
}
func currentHstoreRecord(view catalog.Read, call functions.HstoreRecord) (*catalog.Procedure, error) {
	original, ok := call.RoutineReference.(*catalog.Procedure)
	if !ok {
		return nil, fmt.Errorf("stuntgres: invalid hstore record original")
	}
	if err := validateCapturedHstoreRecord(call, original); err != nil {
		return nil, err
	}
	return validateHstoreRecordDeclaration(view, original, call, func(t *sqltype.Descriptor) *sqltype.Descriptor { return t })
}
