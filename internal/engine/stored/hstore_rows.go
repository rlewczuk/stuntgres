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

func hstoreRowsIdentity(call functions.HstoreRows) (int, string) {
	switch call.Plan.Kind() {
	case sqltype.HstoreRowKeys:
		return extension.HstoreSkeysSlot, "hstore_skeys"
	case sqltype.HstoreRowValues:
		return extension.HstoreSvalsSlot, "hstore_svals"
	case sqltype.HstoreRowPairs:
		return extension.HstoreEachSlot, "hstore_each"
	default:
		return -1, ""
	}
}

func validateCapturedHstoreRows(call functions.HstoreRows, captured *catalog.Procedure) error {
	if err := call.Validate(); err != nil {
		return err
	}
	member, symbol := hstoreRowsIdentity(call)
	if captured == nil || member != int(call.Member) || call.RoutineReference != captured || captured.Revision() == 0 || call.Ref.Address != captured.Address() || captured.Implementation != extension.StockHstore || captured.Builtin != nil || captured.SQL != nil || captured.Foreign != nil || captured.RoutineKind != catalog.FunctionRoutine || !captured.Strict || captured.Volatility != expr.Immutable || captured.Signature != extension.HstoreSignature(call.Formal.ID()) || captured.Result != ids.OID(call.Usage.Type.ID()) || captured.Display != (catalog.RoutineDisplay{Language: catalog.CRoutineLanguage, Source: symbol, Binary: "$libdir/hstore", HasBinary: true}) || !captured.Installed.ReturnsSet || captured.Installed.Rows != 1000 || captured.Installed.Parallel != 's' {
		return fmt.Errorf("stuntgres: invalid captured hstore rows declaration")
	}
	outputs := captured.OutputDeclaration()
	if call.Plan.Kind() == sqltype.HstoreRowPairs {
		if outputs == nil || outputs.Len() != 2 || outputs.Inputs() != 1 || outputs.Name(0) != "hs" || outputs.Name(1) != "key" || outputs.Name(2) != "value" || outputs.Type(0) != 25 || outputs.Type(1) != 25 || captured.Installed.ArgumentNames != [4]string{"hs"} {
			return fmt.Errorf("stuntgres: invalid captured hstore rows outputs")
		}
	} else if outputs != nil || captured.Installed.ArgumentNames != [4]string{} {
		return fmt.Errorf("stuntgres: invalid captured hstore rows inputs")
	}
	return nil
}

func validateHstoreRowsDeclaration(view catalog.Read, original *catalog.Procedure, call functions.HstoreRows, types func(*sqltype.Descriptor) *sqltype.Descriptor) (*catalog.Procedure, error) {
	invalid := func() (*catalog.Procedure, error) {
		return nil, fmt.Errorf("stuntgres: invalid stored hstore rows authority")
	}
	if call.Ref.Epoch != view.Epoch() {
		return invalid()
	}
	current, symbol, err := currentInstalledProcedure(view, call.Ref, original)
	if err != nil {
		return nil, err
	}
	member, want := hstoreRowsIdentity(call)
	if member != int(call.Member) || current.Implementation != extension.StockHstore || symbol != want {
		return invalid()
	}
	installation, ok := catalog.OriginInstallation(current)
	if !ok || len(installation.Members) != extension.HstoreMemberCount || installation.Members[member] != current.Address() {
		return invalid()
	}
	base, ok := view.Type(installation.Members[extension.HstoreTypeSlot])
	if !ok || base.SQLType != types(call.Formal) || base.SQLType != types(call.Arg.ResultUsage().Type) {
		return invalid()
	}
	text, ok := view.LookupType(sqltype.Text)
	if !ok {
		return invalid()
	}
	result := types(call.Usage.Type)
	if call.Plan.Kind() == sqltype.HstoreRowPairs {
		if result == nil || result.ID() != 2249 || result.CompositeLen() != 2 {
			return invalid()
		}
		for i, name := range []string{"key", "value"} {
			m := result.CompositeMember(i)
			if m.Attribute != int32(i+1) || m.Name != name || m.Usage != text.Usage() {
				return invalid()
			}
		}
	} else if result != text {
		return invalid()
	}
	if err := extension.StockHstore.ValidateProcedure(view, current); err != nil {
		return nil, err
	}
	return current, nil
}

func remapHstoreRowsRoutine(view catalog.Read, reader catalog.DefinitionReader, captured, original *catalog.Procedure, call functions.HstoreRows, types func(*sqltype.Descriptor) *sqltype.Descriptor) error {
	if captured == nil || original == nil || captured.Revision() != original.Revision() {
		return fmt.Errorf("stuntgres: invalid remapped hstore rows original")
	}
	source := call
	source.Ref.Address = captured.Address()
	if err := validateCapturedHstoreRows(source, captured); err != nil {
		return err
	}
	descriptor, _ := catalog.DescriptorAt(view, reader, call.Ref.Address)
	current, ok := descriptor.(*catalog.Procedure)
	if !ok || !exec.SameRoutineDeclaration(current, original) {
		return fmt.Errorf("stuntgres: missing remapped hstore rows declaration")
	}
	_, err := validateHstoreRowsDeclaration(view, original, call, types)
	return err
}

func currentHstoreRows(view catalog.Read, call functions.HstoreRows) (*catalog.Procedure, error) {
	original, ok := call.RoutineReference.(*catalog.Procedure)
	if !ok {
		return nil, fmt.Errorf("stuntgres: invalid hstore rows original")
	}
	if err := validateCapturedHstoreRows(call, original); err != nil {
		return nil, err
	}
	return validateHstoreRowsDeclaration(view, original, call, func(t *sqltype.Descriptor) *sqltype.Descriptor { return t })
}
