package stored

import (
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/sqltype"
)

func validateCapturedPgTrgm(call functions.PgTrgm, captured *catalog.Procedure) error {
	if err := call.Validate(); err != nil {
		return err
	}
	member := functions.PgTrgmMembers[call.Member]
	if captured == nil || call.RoutineReference != captured || captured.Revision() == 0 || call.Ref.Address != captured.Address() || captured.Implementation != extension.StockPgTrgm || captured.Builtin != nil || captured.SQL != nil || captured.Foreign != nil || captured.RoutineKind != catalog.FunctionRoutine || !captured.Strict || captured.Volatility != member.Volatility || string(captured.Signature) != member.Signature || captured.Result != member.Result || captured.Display != (catalog.RoutineDisplay{Language: catalog.CRoutineLanguage, Source: member.Name, Binary: "$libdir/pg_trgm", HasBinary: true}) || captured.Installed != (catalog.InstalledRoutineDeclaration{Parallel: 's'}) || !captured.Origin.Valid(captured.Address()) {
		return fmt.Errorf("stuntgres: invalid captured pg_trgm declaration")
	}
	origin := captured.Origin
	if origin.Declaration != int(call.Member) || origin.Installation.Implementation != extension.StockPgTrgm || origin.Installation.Provider != "pg_trgm" || origin.Installation.Version != "1.6" || len(origin.Installation.Members) != extension.PgTrgmMemberCount {
		return fmt.Errorf("stuntgres: invalid captured pg_trgm installation")
	}
	return nil
}

func validatePgTrgmDeclaration(view catalog.Read, original *catalog.Procedure, call functions.PgTrgm, types func(*sqltype.Descriptor) *sqltype.Descriptor) (*catalog.Procedure, error) {
	invalid := func() (*catalog.Procedure, error) {
		return nil, fmt.Errorf("stuntgres: invalid stored pg_trgm authority")
	}
	if int(call.Member) >= len(functions.PgTrgmMembers) || call.Ref.Epoch != view.Epoch() {
		return invalid()
	}
	current, symbol, err := currentInstalledProcedure(view, call.Ref, original)
	if err != nil {
		return nil, err
	}
	member := functions.PgTrgmMembers[call.Member]
	installation, ok := catalog.OriginInstallation(current)
	if !ok || current.Implementation != extension.StockPgTrgm || symbol != member.Name || len(installation.Members) != extension.PgTrgmMemberCount || installation.Members[call.Member] != current.Address() || current.Origin.Declaration != int(call.Member) {
		return invalid()
	}
	text, ok := view.LookupType(sqltype.Text)
	if !ok || types(call.Formal) != text {
		return invalid()
	}
	for _, arg := range call.Args {
		if types(arg.ResultUsage().Type) != text {
			return invalid()
		}
	}
	result, ok := view.LookupType(sqltype.ID(member.Result))
	if !ok || types(call.Usage.Type) != result {
		return invalid()
	}
	if err := extension.StockPgTrgm.ValidateProcedure(view, current); err != nil {
		return nil, err
	}
	return current, nil
}

func remapPgTrgmRoutine(view catalog.Read, reader catalog.DefinitionReader, captured, original *catalog.Procedure, call functions.PgTrgm, types func(*sqltype.Descriptor) *sqltype.Descriptor) error {
	if captured == nil || original == nil || captured.Revision() != original.Revision() {
		return fmt.Errorf("stuntgres: invalid remapped pg_trgm original")
	}
	source := call
	source.Ref.Address = captured.Address()
	if err := validateCapturedPgTrgm(source, captured); err != nil {
		return err
	}
	descriptor, _ := catalog.DescriptorAt(view, reader, call.Ref.Address)
	current, ok := descriptor.(*catalog.Procedure)
	if !ok || !exec.SameRoutineDeclaration(current, original) {
		return fmt.Errorf("stuntgres: missing remapped pg_trgm declaration")
	}
	_, err := validatePgTrgmDeclaration(view, original, call, types)
	return err
}

func currentPgTrgm(view catalog.Read, call functions.PgTrgm) (*catalog.Procedure, error) {
	original, ok := call.RoutineReference.(*catalog.Procedure)
	if !ok {
		return nil, fmt.Errorf("stuntgres: invalid pg_trgm original")
	}
	if err := validateCapturedPgTrgm(call, original); err != nil {
		return nil, err
	}
	return validatePgTrgmDeclaration(view, original, call, func(t *sqltype.Descriptor) *sqltype.Descriptor { return t })
}
