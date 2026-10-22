package stored

import (
	"strconv"
	"strings"
	"stuntgres/internal/engine/extension"

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
	if ok && d.Implementation != nil {
		return extension.CatalogProvider{ids.ProcedureClassID}.Validate(v, d)
	}
	invalid := extension.InvalidProviderDescriptor
	if err := extension.ValidateProviderHeader(v, descriptor, ids.ProcedureClassID); err != nil {
		return err
	}
	if !ok {
		return invalid()
	}
	switch {
	case d.SQL != nil:
		return validateSQLProcedure(v, d)
	case d.Aggregate != nil:
		return invalid()
	case d.Foreign != nil:
		p := d.Foreign
		if p.Name.Name != d.Name() || d.Extension != (ids.ObjectAddress{}) || d.Conversion.Name() != "" || d.Builtin != nil {
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
		if routine.BuiltinRoutine(d.Address().ObjectID) != declared || d.Namespace() != (ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PGCatalogNamespaceOID}) || d.Name() != declared.Identity.Name || d.Signature != declared.Identity.Signature || d.RoutineKind != declared.Kind || d.Result != declared.Result || d.Strict != declared.Strict || d.Volatility != declared.Volatility || d.Conversion.Name() != "" || d.Extension != (ids.ObjectAddress{}) || d.Dictionary != (ids.ObjectAddress{}) || d.Foreign != nil {
			return invalid()
		}
		return nil
	case d.RoutineKind != catalog.FunctionRoutine:
		return invalid()
	case d.Conversion.Name() != "":
		registered, ok := sqltype.RegisteredEncodingConversion(d.Name())
		if !ok || d.Conversion != registered || d.Signature != catalog.ConversionSignature || d.Result != 23 || d.Volatility != expr.Immutable || !d.Strict || d.Extension != (ids.ObjectAddress{}) || d.Dictionary != (ids.ObjectAddress{}) || d.Namespace() != (ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PGCatalogNamespaceOID}) || typedef.ConversionRoutineName(d.Address().ObjectID) != d.Name() {
			return invalid()
		}
		return nil
	}
	return invalid()
}

func validateSQLProcedure(v catalog.Read, d *catalog.Procedure) error {
	invalid := extension.InvalidProviderDescriptor
	if !d.SQL.Result.Valid() || d.SQL.Standard != (d.SQLBody != nil) || len(d.SQL.Names) != len(d.SQL.Inputs) {
		return invalid()
	}
	result, ok := v.LookupType(d.SQL.Result.Type.ID())
	if !ok || !result.Concrete() {
		return invalid()
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
		if !input.Valid() {
			return invalid()
		}
		typ, ok := v.LookupType(input.Type.ID())
		if !ok || !typ.Concrete() {
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
	if d.Builtin != nil || d.Aggregate != nil || d.Foreign != nil || d.Conversion.Name() != "" || d.Extension != (ids.ObjectAddress{}) || d.Dictionary != (ids.ObjectAddress{}) || len(d.Body) != 0 || d.RoutineKind != catalog.FunctionRoutine && d.RoutineKind != catalog.ProcedureRoutine || d.RoutineKind == catalog.ProcedureRoutine && !d.SQL.Void() || d.Strict || d.Volatility != expr.Volatile || ids.OID(d.SQL.Result.Type.ID()) != d.Result {
		return invalid()
	}
	return nil
}
