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

func validateCapturedHstoreJSON(call functions.HstoreJSON, captured *catalog.Procedure) error {
	if err := call.Validate(); err != nil {
		return err
	}
	symbol := "hstore_to_json"
	if call.Loose {
		symbol = "hstore_to_json_loose"
	}
	if captured == nil || call.RoutineReference != captured || captured.Revision() == 0 || call.Ref.Address != captured.Address() || captured.Implementation != extension.StockHstore || captured.Builtin != nil || captured.SQL != nil || captured.Foreign != nil || captured.RoutineKind != catalog.FunctionRoutine || !captured.Strict || captured.Volatility != expr.Immutable || captured.Signature != extension.HstoreSignature(call.Formal.ID()) || captured.Result != 114 || captured.Display != (catalog.RoutineDisplay{Language: catalog.CRoutineLanguage, Source: symbol}) || captured.Installed != (catalog.InstalledRoutineDeclaration{}) {
		return fmt.Errorf("stuntgres: invalid captured hstore JSON declaration")
	}
	return nil
}
func validateHstoreJSONDeclaration(view catalog.Read, original *catalog.Procedure, call functions.HstoreJSON, types func(*sqltype.Descriptor) *sqltype.Descriptor) (*catalog.Procedure, error) {
	invalid := func() (*catalog.Procedure, error) {
		return nil, fmt.Errorf("stuntgres: invalid stored hstore JSON authority")
	}
	if call.Ref.Epoch != view.Epoch() {
		return invalid()
	}
	current, symbol, err := currentInstalledProcedure(view, call.Ref, original)
	if err != nil {
		return nil, err
	}
	want := "hstore_to_json"
	if call.Loose {
		want = "hstore_to_json_loose"
	}
	if current.Implementation != extension.StockHstore || symbol != want {
		return invalid()
	}
	installation, ok := view.Extension(current.Extension)
	if !ok || len(installation.Members) != extension.HstoreMemberCount || installation.Members[call.Member] != current.Address() {
		return invalid()
	}
	base, ok := view.Type(installation.Members[extension.HstoreTypeSlot])
	if !ok || base.SQLType != types(call.Formal) || base.SQLType != types(call.Arg.ResultUsage().Type) {
		return invalid()
	}
	result, ok := view.LookupType(114)
	if !ok || types(call.Usage.Type) != result || current.Signature != extension.HstoreSignature(base.SQLType.ID()) {
		return invalid()
	}
	if err := extension.StockHstore.ValidateProcedure(view, current); err != nil {
		return nil, err
	}
	return current, nil
}
func remapHstoreJSONRoutine(view catalog.Read, reader catalog.DefinitionReader, captured, original *catalog.Procedure, call functions.HstoreJSON, types func(*sqltype.Descriptor) *sqltype.Descriptor) error {
	if captured == nil || original == nil || captured.Revision() != original.Revision() {
		return fmt.Errorf("stuntgres: invalid remapped hstore JSON original")
	}
	source := call
	source.Ref.Address = captured.Address()
	if err := validateCapturedHstoreJSON(source, captured); err != nil {
		return err
	}
	descriptor, _ := catalog.DescriptorAt(view, reader, call.Ref.Address)
	current, ok := descriptor.(*catalog.Procedure)
	if !ok || !exec.SameRoutineDeclaration(current, original) {
		return fmt.Errorf("stuntgres: missing remapped hstore JSON declaration")
	}
	_, err := validateHstoreJSONDeclaration(view, original, call, types)
	return err
}
func currentHstoreJSON(view catalog.Read, call functions.HstoreJSON) (*catalog.Procedure, error) {
	original, ok := call.RoutineReference.(*catalog.Procedure)
	if !ok {
		return nil, fmt.Errorf("stuntgres: invalid hstore JSON original")
	}
	if err := validateCapturedHstoreJSON(call, original); err != nil {
		return nil, err
	}
	return validateHstoreJSONDeclaration(view, original, call, func(t *sqltype.Descriptor) *sqltype.Descriptor { return t })
}
