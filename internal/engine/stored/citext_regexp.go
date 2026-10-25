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

func citextRegexpCallOf(node expr.Expr) (functions.RegexpCall, bool) {
	var call functions.RegexpCall
	switch n := node.(type) {
	case functions.Regexp:
		call = n.RegexpCall
	case functions.RegexpSet:
		call = n.RegexpCall
	}
	return call, call.Citext != nil
}

func validateCapturedCitextRegexp(call functions.RegexpCall, captured *catalog.Procedure) error {
	if err := (functions.Regexp{RegexpCall: call}).ValidateStoredProvider(); err != nil {
		return err
	}
	capture := call.Citext
	if capture == nil || captured == nil || call.RoutineReference != captured ||
  int(capture.Member) < extension.CitextRegexpProcedureStart || int(capture.Member) >= extension.CitextRegexpProcedureEnd {
		return fmt.Errorf("stuntgres: invalid captured citext regexp declaration")
	}
	member := extension.CitextRegexpMembers[int(capture.Member)-extension.CitextRegexpProcedureStart]
	canInline := !member.Declaration.ReturnsSet && member.Declaration.ArgumentNames[member.ArgCount-1] != "flags"
	if capture.Text != capture.Text.Type.Usage() || capture.CanInline != canInline ||
		capture.Inline && !canInline {
		return fmt.Errorf("stuntgres: invalid captured citext SQL body metadata")
	}
	if captured.Revision() == 0 || capture.Ref.Address != captured.Address() ||
		captured.Implementation != extension.StockCitext || captured.Builtin != nil || captured.SQL != nil || captured.Foreign != nil ||
		captured.RoutineKind != catalog.FunctionRoutine || !captured.Strict || captured.Volatility != expr.Immutable ||
		captured.Installed != member.Declaration || captured.Language != (ids.ObjectAddress{}) ||
		captured.Display != (catalog.RoutineDisplay{Language: catalog.SQLRoutineLanguage}) ||
		call.Plan.Name() != member.Name || len(call.Args) != member.ArgCount ||
		captured.Signature != member.Signature(call.Args[0].ResultUsage().Type.ID()) || captured.Result != member.Result {
		return fmt.Errorf("stuntgres: invalid captured citext regexp declaration")
	}
	return nil
}

func validateCitextRegexpDeclaration(view catalog.Read, original *catalog.Procedure, call functions.RegexpCall, types func(*sqltype.Descriptor) *sqltype.Descriptor) (*catalog.Procedure, error) {
	invalid := func() (*catalog.Procedure, error) {
		return nil, fmt.Errorf("stuntgres: invalid stored citext regexp authority")
	}
	capture := call.Citext
 if capture == nil || int(capture.Member) < extension.CitextRegexpProcedureStart || int(capture.Member) >= extension.CitextRegexpProcedureEnd {
		return invalid()
	}
	if capture.Ref.Epoch != view.Epoch() {
		return invalid()
	}
	current, symbol, err := currentInstalledProcedure(view, capture.Ref, original)
	if err != nil {
		return nil, err
	}
	member := extension.CitextRegexpMembers[int(capture.Member)-extension.CitextRegexpProcedureStart]
	if current.Implementation != extension.StockCitext || symbol != member.Name || len(call.Args) != member.ArgCount || current.Installed != member.Declaration {
		return invalid()
	}
	installation, ok := view.Extension(current.Extension)
	if !ok || len(installation.Members) != extension.CitextMemberCount || installation.Members[capture.Member] != current.Address() {
		return invalid()
	}
	base, ok := view.Type(installation.Members[0])
	if !ok || base.SQLType != types(call.Args[0].ResultUsage().Type) || base.SQLType != types(call.Args[1].ResultUsage().Type) {
		return invalid()
	}
	text, ok := view.LookupType(sqltype.Text)
	if !ok || types(capture.Text.Type) != text {
		return invalid()
	}
	for i := 2; i < len(call.Args); i++ {
		if types(call.Args[i].ResultUsage().Type) != text {
			return invalid()
		}
	}
	result, ok := view.LookupType(sqltype.ID(member.Result))
	if !ok || types(call.Usage.Type) != result || current.Signature != member.Signature(base.SQLType.ID()) || current.Result != member.Result {
		return invalid()
	}
	if err := extension.StockCitext.ValidateProcedure(view, current); err != nil {
		return nil, err
	}
	return current, nil
}

// References have already moved. The callback's mapped ORIGINAL must agree
// with the destination; a current same-name declaration is never a substitute.
func remapCitextRegexpRoutine(view catalog.Read, reader catalog.DefinitionReader, captured, original *catalog.Procedure, call functions.RegexpCall, types func(*sqltype.Descriptor) *sqltype.Descriptor) error {
	if captured == nil || original == nil || captured.Revision() != original.Revision() {
		return fmt.Errorf("stuntgres: invalid remapped citext regexp original")
	}
	// Check the source declaration with its original address, without replacing
	// its formal descriptors, selected member or routine reference.
	source := call
	capture := *call.Citext
	capture.Ref.Address = captured.Address()
	source.Citext = &capture
	if err := validateCapturedCitextRegexp(source, captured); err != nil {
		return err
	}
	descriptor, _ := catalog.DescriptorAt(view, reader, call.Citext.Ref.Address)
	current, ok := descriptor.(*catalog.Procedure)
	if !ok || !exec.SameRoutineDeclaration(current, original) {
		return fmt.Errorf("stuntgres: missing remapped citext regexp declaration")
	}
	_, err := validateCitextRegexpDeclaration(view, original, call, types)
	return err
}

func currentCitextRegexp(view catalog.Read, call functions.RegexpCall) (*catalog.Procedure, error) {
	original, ok := call.RoutineReference.(*catalog.Procedure)
	if !ok {
		return nil, fmt.Errorf("stuntgres: invalid citext regexp original")
	}
	if err := validateCapturedCitextRegexp(call, original); err != nil {
		return nil, err
	}
	return validateCitextRegexpDeclaration(view, original, call, func(t *sqltype.Descriptor) *sqltype.Descriptor { return t })
}

func remapCitextRegexpReference(call functions.RegexpCall, ref func(ids.ObjectRef) ids.ObjectRef) functions.RegexpCall {
	if call.Citext != nil && ref != nil {
		capture := *call.Citext
		capture.Ref = ref(capture.Ref)
		call.Citext = &capture
	}
	return call
}
