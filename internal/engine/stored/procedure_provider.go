package stored

import (
	"slices"
	"strconv"
	"strings"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/proc"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/routine"
	"stuntgres/internal/engine/typedef"
	"stuntgres/internal/sqltype"
)

// ProcedureProvider validates the procedure class. Routines installed by an
// extension carry their implementation and are validated as extension
// members; builtin, SQL, foreign and conversion routines are checked here.
type ProcedureProvider struct{}

func (ProcedureProvider) ClassID() ids.OID { return ids.ProcedureClassID }

func (ProcedureProvider) Validate(v catalog.Read, descriptor catalog.Descriptor) error {
	d, ok := descriptor.(*catalog.Procedure)
	if ok && d.RangeConstructor != nil && (d.Implementation != nil || d.AccessMethod != catalog.NoAccessMethod) {
		return extension.InvalidProviderDescriptor()
	}
	if ok && d.Implementation != nil {
		if d.AccessMethod != catalog.NoAccessMethod {
			return extension.InvalidProviderDescriptor()
		}
		return extension.CatalogProvider{ids.ProcedureClassID}.Validate(v, d)
	}
	invalid := extension.InvalidProviderDescriptor
	if err := extension.ValidateProviderHeader(v, descriptor, ids.ProcedureClassID); err != nil {
		return err
	}
	if !ok {
		return invalid()
	}
	if d.SQL == nil && len(d.DeclarationDependencies) != 0 {
		return invalid()
	}
	if d.PLHandler != catalog.NoPLHandler {
		return catalog.ValidatePLHandler(v, d)
	}
	if d.BrinSupport != 0 {
		return catalog.ValidateBrinSupport(d)
	}
	if d.AccessMethod != catalog.NoAccessMethod {
		identity, known := catalog.BuiltinRoutineIdentityByOID(d.Address().ObjectID)
		result, _ := catalog.BuiltinRoutineResultByOID(d.Address().ObjectID)
		if !known || catalog.NativeHandler(d.Address().ObjectID) != d.AccessMethod || d.Signature != identity.Signature || d.Result != result || d.RoutineKind != catalog.FunctionRoutine || d.Volatility != expr.Volatile || !d.Strict || d.Builtin != nil || d.SQL != nil || d.SQLBody != nil || d.SQLDefaults != nil || d.Aggregate != nil || d.Foreign != nil || d.Conversion.Name() != "" || d.Extension != (ids.ObjectAddress{}) || d.Dictionary != (ids.ObjectAddress{}) || len(d.Body) != 0 {
			return invalid()
		}
		return nil
	}
	switch {
	case d.RangeConstructor != nil:
		if err := catalog.ValidateRangeConstructor(v, d); err != nil {
			return err
		}
		owner, _ := v.Type(catalog.RangeConstructorOwner(d))
		return (TypeProvider{}).Validate(v, owner)
	case d.SQL != nil:
		return validateSQLProcedure(v, d)
	case d.Aggregate != nil:
		return routine.ValidateAggregateDefinition(v, d)
	case d.Foreign != nil:
		p := d.Foreign
		if p.Name.Name == "" || d.Extension != (ids.ObjectAddress{}) || d.Conversion.Name() != "" || d.Builtin != nil {
			return invalid()
		}
		if p.Wrapper != nil {
			if p.Validator != nil || d.Signature != catalog.ForeignHandlerSignature || d.Result != 3115 {
				return invalid()
			}
		} else if p.Validator == nil || d.Signature != catalog.ForeignValidatorSignature || d.Result != 2278 {
			return invalid()
		}
		return nil
	case d.Builtin != nil:
		declared := d.Builtin
		if routine.BuiltinRoutine(d.Address().ObjectID) != declared || d.Signature != declared.Identity.Signature || d.RoutineKind != declared.Kind || d.Result != declared.Result || d.Strict != declared.Strict || d.Volatility != declared.Volatility || d.Conversion.Name() != "" || d.Extension != (ids.ObjectAddress{}) || d.Dictionary != (ids.ObjectAddress{}) || d.Foreign != nil {
			return invalid()
		}
		return nil
	case d.RoutineKind != catalog.FunctionRoutine:
		return invalid()
	case d.Conversion.Name() != "":
		registered, ok := sqltype.RegisteredEncodingConversion(d.Conversion.Name())
		if !ok || d.Conversion != registered || d.Signature != catalog.ConversionSignature || d.Result != 23 || d.Volatility != expr.Immutable || !d.Strict || d.Extension != (ids.ObjectAddress{}) || d.Dictionary != (ids.ObjectAddress{}) || typedef.ConversionRoutineName(d.Address().ObjectID) != d.Conversion.Name() {
			return invalid()
		}
		return nil
	}
	return invalid()
}

func validateSQLProcedure(v catalog.Read, d *catalog.Procedure) error {
	invalid := extension.InvalidProviderDescriptor
	if !proc.DeclarationUsage(d.SQL.Result) || d.SQL.Standard != (d.SQLBody != nil) || len(d.SQL.Names) != len(d.SQL.Inputs) {
		return invalid()
	}
	language, validLanguage := v.Get(d.Language).(*catalog.Language)
	if !validLanguage || (catalog.LanguageProvider{}).Validate(v, language) != nil {
		return invalid()
	}
	if d.SQL.Kind == proc.PLpgSQLBody {
		// The checked lexical name belongs to the immutable definition, not
		// the descriptor's current public label after a routine ALTER.
		if language.Name() != "plpgsql" || d.SQL.Standard || len(d.SQL.Statements) != 0 || d.SQL.PL == nil || d.SQL.PLSyntax == nil || d.SQL.PL.Function != d.SQL.Name || d.SQL.PL.FormalCount != len(d.SQL.Formals) || d.SQL.PL.Procedure != (d.RoutineKind == catalog.ProcedureRoutine) {
			return invalid()
		}
		if err := d.SQL.PL.ValidateDefinition(d.SQL); err != nil {
			return err
		}
		seen := make(map[ids.ObjectAddress]bool, len(d.DeclarationDependencies))
		for _, dependency := range d.DeclarationDependencies {
			if dependency.ObjectID == 0 || seen[dependency] {
				return invalid()
			}
			seen[dependency] = true
			base := dependency
			base.SubID = 0
			switch dependency.ClassID {
			case ids.TypeClassID:
				if dependency.SubID != 0 {
					return invalid()
				}
				if _, ok := v.Get(base).(*catalog.Type); !ok {
					return invalid()
				}
			case ids.CollationClassID:
				if dependency.SubID != 0 {
					return invalid()
				}
				if _, ok := v.Get(base).(*catalog.Collation); !ok {
					return invalid()
				}
			case ids.RelationClassID:
				relation, ok := v.Get(base).(*catalog.Relation)
				if !ok || dependency.SubID < 0 || dependency.SubID > 0 && !slices.Contains(relation.Attributes, dependency.SubID) {
					return invalid()
				}
			default:
				return invalid()
			}
		}
		for _, variable := range d.SQL.PL.Variables {
			if variable.Record {
				continue
			}
			if !variable.Usage.Valid() && !(proc.DeclarationUsage(variable.Usage) && sqltype.PolymorphicFamily(variable.Usage.Type.ID()) != 0) {
				return invalid()
			}
			typ, exists := v.LookupType(variable.Usage.Type.ID())
			if !exists || typ != variable.Usage.Type {
				return invalid()
			}
			if err := ValidateUsageCollation(v, variable.Usage); err != nil {
				return err
			}
		}
	} else if d.SQL.Kind != proc.SQLBody || language.Name() != "sql" || d.SQL.PL != nil || d.SQL.PLSyntax != nil || len(d.DeclarationDependencies) != 0 {
		return invalid()
	}
	result := d.SQL.Result.Type
	if d.SQL.DerivedResult() {
		if !d.SQL.MatchesOutputTuple(result) || d.SQL.Result != result.Usage() {
			return invalid()
		}
	} else {
		owned, ok := v.LookupType(result.ID())
		if !ok || !proc.DeclarationUsage(owned.Usage()) || owned != result {
			return invalid()
		}
	}
	if (d.SQL.Invocation == proc.DMLTriggerInvocation) != (result.ID() == 2279) || (d.SQL.Invocation == proc.EventTriggerInvocation) != (result.ID() == 3838) || d.SQL.Invocation != proc.OrdinaryInvocation && (d.SQL.Kind != proc.PLpgSQLBody || len(d.SQL.Formals) != 0 || d.RoutineKind != catalog.FunctionRoutine) {
		return invalid()
	}
	layout := &proc.Definition{Procedure: d.SQL.Procedure}
	if err := layout.SetFormals(d.SQL.Formals); err != nil {
		return err
	}
	if !slices.Equal(layout.Inputs, d.SQL.Inputs) || !slices.Equal(layout.Names, d.SQL.Names) || !slices.Equal(layout.InputFormals, d.SQL.InputFormals) || !slices.Equal(layout.OutputFormals, d.SQL.OutputFormals) || !slices.Equal(layout.FormalInputs, d.SQL.FormalInputs) {
		return invalid()
	}
	if !d.SQL.DerivedResult() && len(layout.OutputFormals) == 1 && d.SQL.Result != layout.Formals[layout.OutputFormals[0]].Usage {
		return invalid()
	}
	if d.SQL.ReturnsSet && (d.SQL.Kind != proc.SQLBody || d.RoutineKind != catalog.FunctionRoutine) {
		return invalid()
	}
	outputs := []sqltype.Usage{d.SQL.Result}
	for _, slot := range d.SQL.OutputFormals {
		outputs = append(outputs, d.SQL.Formals[slot].Usage)
	}
	if err := proc.ValidatePseudoSignature(d.SQL.Inputs, outputs, d.SQL.Formals, language.Name(), d.SQL.Standard); err != nil {
		return err
	}
	for _, formal := range d.SQL.Formals {
		typ, ok := v.LookupType(formal.Usage.Type.ID())
		if !ok || typ != formal.Usage.Type {
			return invalid()
		}
		if err := ValidateUsageCollation(v, formal.Usage); err != nil {
			return err
		}
	}
	if d.SQL.TableResult {
		if !d.SQL.ReturnsSet || len(d.SQL.OutputFormals) == 0 {
			return invalid()
		}
		for _, index := range d.SQL.OutputFormals {
			if d.SQL.Formals[index].Mode != "t" {
				return invalid()
			}
		}
	} else {
		for _, formal := range d.SQL.Formals {
			if formal.Mode == "t" {
				return invalid()
			}
		}
	}
	if (d.SQLDefaults != nil) != (d.SQL.DefaultCount() > 0) {
		return invalid()
	}
	if d.SQLDefaults != nil {
		if err := d.SQLDefaults.ValidateDefinition(d); err != nil {
			return err
		}
		if err := d.SQLDefaults.Validate(v); err != nil {
			return err
		}
	}
	if d.SQLBody != nil {
		program, err := RoutineProgramOf(d)
		if err != nil {
			return invalid()
		}
		if err := program.ValidateDefinition(d); err != nil {
			return err
		}
		if err := d.SQLBody.Validate(v); err != nil {
			return err
		}
	}
	var signature strings.Builder
	for i, input := range d.SQL.Inputs {
		if !proc.DeclarationUsage(input) {
			return invalid()
		}
		typ, ok := v.LookupType(input.Type.ID())
		if !ok || typ != input.Type {
			return invalid()
		}
		if i != 0 {
			signature.WriteByte(',')
		}
		signature.WriteString(strconv.FormatUint(uint64(typ.ID()), 10))
	}
	if string(d.Signature) != signature.String() {
		return invalid()
	}
	if d.Builtin != nil || d.Aggregate != nil || d.Foreign != nil || d.Conversion.Name() != "" || d.Extension != (ids.ObjectAddress{}) || d.Dictionary != (ids.ObjectAddress{}) || len(d.Body) != 0 || d.RoutineKind != catalog.FunctionRoutine && d.RoutineKind != catalog.ProcedureRoutine || d.SQL.Procedure != (d.RoutineKind == catalog.ProcedureRoutine) || d.RoutineKind == catalog.ProcedureRoutine && (len(d.SQL.OutputFormals) == 0 && !d.SQL.Void() || d.Strict) || d.Volatility != expr.Volatile || ids.OID(d.SQL.Result.Type.ID()) != d.Result {
		return invalid()
	}
	return nil
}
