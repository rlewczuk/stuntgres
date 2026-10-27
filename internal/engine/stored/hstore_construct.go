package stored

import (
	"fmt"
	"strconv"
	"strings"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

func hstoreConstructSignature(call functions.HstoreConstruct, types func(*sqltype.Descriptor) *sqltype.Descriptor) catalog.ProcedureSignature {
	var parts [2]string
	for i := range call.Arity() {
		parts[i] = strconv.FormatUint(uint64(types(call.Formals[i]).ID()), 10)
	}
	return catalog.ProcedureSignature(strings.Join(parts[:call.Arity()], ","))
}
func validateCapturedHstoreConstruct(call functions.HstoreConstruct, captured *catalog.Procedure) error {
	if err := call.Validate(); err != nil {
		return err
	}
	identity := func(t *sqltype.Descriptor) *sqltype.Descriptor { return t }
	if captured == nil || call.RoutineReference != captured || captured.Revision() == 0 || call.Ref.Address != captured.Address() || captured.Implementation != extension.StockHstore || captured.Builtin != nil || captured.SQL != nil || captured.Foreign != nil || captured.RoutineKind != catalog.FunctionRoutine || captured.Strict != call.Strict() || captured.Volatility != expr.Immutable || captured.Signature != hstoreConstructSignature(call, identity) || captured.Result != ids.OID(call.Usage.Type.ID()) || captured.Display != (catalog.RoutineDisplay{Language: catalog.CRoutineLanguage, Source: call.Symbol(), Binary: "$libdir/hstore", HasBinary: true}) || captured.Installed != (catalog.InstalledRoutineDeclaration{}) || captured.Language != (ids.ObjectAddress{}) || captured.SQLBody != nil || captured.SQLDefaults != nil || len(captured.Body) != 0 {
		return fmt.Errorf("stuntgres: invalid captured hstore selected declaration")
	}
	return nil
}
func validateHstoreConstructDeclaration(view catalog.Read, original *catalog.Procedure, call functions.HstoreConstruct, types func(*sqltype.Descriptor) *sqltype.Descriptor) (*catalog.Procedure, error) {
	invalid := func() (*catalog.Procedure, error) {
		return nil, fmt.Errorf("stuntgres: invalid stored hstore selected authority")
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
		if mapped == nil {
			return invalid()
		}
		formal, ok := view.LookupType(mapped.ID())
		if !ok || formal != mapped || formal != types(call.Args[i].ResultUsage().Type) {
			return invalid()
		}
	}
	mappedResult := types(call.Usage.Type)
	if mappedResult == nil {
		return invalid()
	}
	result, ok := view.LookupType(mappedResult.ID())
	if !ok || result != mappedResult || current.Result != ids.OID(result.ID()) || current.Signature != hstoreConstructSignature(call, types) {
		return invalid()
	}
	if err := extension.StockHstore.ValidateProcedure(view, current); err != nil {
		return nil, err
	}
	return current, nil
}
func remapHstoreConstructRoutine(view catalog.Read, reader catalog.DefinitionReader, captured, original *catalog.Procedure, call functions.HstoreConstruct, types func(*sqltype.Descriptor) *sqltype.Descriptor) error {
	if captured == nil || original == nil || captured.Revision() != original.Revision() {
		return fmt.Errorf("stuntgres: invalid remapped hstore selected original")
	}
	source := call
	source.Ref.Address = captured.Address()
	if err := validateCapturedHstoreConstruct(source, captured); err != nil {
		return err
	}
	descriptor, _ := catalog.DescriptorAt(view, reader, call.Ref.Address)
	current, ok := descriptor.(*catalog.Procedure)
	if !ok || !exec.SameRoutineDeclaration(current, original) {
		return fmt.Errorf("stuntgres: missing remapped hstore selected declaration")
	}
	_, err := validateHstoreConstructDeclaration(view, original, call, types)
	return err
}
func currentHstoreConstruct(view catalog.Read, call functions.HstoreConstruct) (*catalog.Procedure, error) {
	original, ok := call.RoutineReference.(*catalog.Procedure)
	if !ok {
		return nil, fmt.Errorf("stuntgres: invalid hstore selected original")
	}
	if err := validateCapturedHstoreConstruct(call, original); err != nil {
		return nil, err
	}
	return validateHstoreConstructDeclaration(view, original, call, func(t *sqltype.Descriptor) *sqltype.Descriptor { return t })
}
