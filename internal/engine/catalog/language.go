package catalog

import (
	"fmt"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
)

type PLHandler uint8

const (
	NoPLHandler PLHandler = iota
	PLCallHandler
	PLInlineHandler
	PLValidator
)

func (h PLHandler) Declaration() (name string, signature ProcedureSignature, result ids.OID) {
	switch h {
	case PLCallHandler:
		return "plpgsql_call_handler", "", 2280
	case PLInlineHandler:
		return "plpgsql_inline_handler", "2281", 2278
	case PLValidator:
		return "plpgsql_validator", "26", 2278
	}
	return "", "", 0
}

// ReleasedProcedure returns the stock PL/pgSQL 1.0 C declaration, before
// catalog identity, ownership, membership and installation origin are assigned.
// Bootstrap and CREATE EXTENSION must produce the same released metadata.
func (h PLHandler) ReleasedProcedure() Procedure {
	name, signature, result := h.Declaration()
	return Procedure{
		ObjectName: name, Signature: signature, Result: result,
		RoutineKind: FunctionRoutine, Volatility: expr.Volatile,
		Display: RoutineDisplay{Language: CRoutineLanguage, Source: name, Binary: "$libdir/plpgsql", HasBinary: true},
		// The shipped C declarations omit PARALLEL: CREATE FUNCTION defaults
		// to UNSAFE, independently of any later mutable Parallel override.
		Installed: InstalledRoutineDeclaration{Parallel: 'u'},
		Strict:    h != PLCallHandler, PLHandler: h,
	}
}

type Language struct {
	ObjectAddress           ids.ObjectAddress
	Extension               ids.ObjectAddress
	Origin                  *InstalledOrigin
	ObjectRevision          ids.DefinitionRevision
	ObjectName              string
	Owner                   authorization.PrincipalRef
	ACL                     authorization.ACLValue
	Procedural, Trusted     bool
	Call, Inline, Validator ids.ObjectAddress
}

func (d *Language) Address() ids.ObjectAddress       { return d.ObjectAddress }
func (d *Language) Revision() ids.DefinitionRevision { return d.ObjectRevision }
func (*Language) Namespace() ids.ObjectAddress       { return ids.ObjectAddress{} }
func (d *Language) Name() string                     { return d.ObjectName }
func (*Language) Kind() ids.ObjectKind               { return ids.LanguageObject }

type LanguageProvider struct{}

func (LanguageProvider) ClassID() ids.OID { return ids.LanguageClassID }
func (LanguageProvider) Validate(v Read, descriptor Descriptor) error {
	d, ok := descriptor.(*Language)
	invalid := func() error { return fmt.Errorf("stuntgres: invalid language descriptor authority") }
	if !ok || d == nil || v.State() == nil || v.Get(d.Address()) != d || d.Address().ClassID != ids.LanguageClassID || d.Address().SubID != 0 || d.Revision() == 0 || v.NameAddress(DescriptorNameKey(d)) != d.Address() {
		return invalid()
	}
	if v.Owner() == nil || v.Owner().Principals == nil || d.Owner.ID() == 0 || d.Owner != v.Owner().Principals.Principal(d.Owner.ID()) {
		return invalid()
	}
	switch d.Name() {
	case "internal", "c", "sql":
		oid, validator := ids.OID(12), ids.OID(2246)
		if d.Name() == "c" {
			oid, validator = 13, 2247
		}
		if d.Name() == "sql" {
			oid, validator = 14, 2248
		}
		if d.Address().ObjectID != oid || d.Procedural || d.Trusted != (d.Name() == "sql") || d.Call != (ids.ObjectAddress{}) || d.Inline != (ids.ObjectAddress{}) || d.Validator != (ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: validator}) {
			return invalid()
		}
	case "plpgsql":
		if !d.Procedural || !d.Trusted || !d.Origin.Valid(d.Address()) || d.Origin.Installation.Provider != "plpgsql" || d.Origin.Declaration != 3 || v.Owner().Extensions["plpgsql"] != d.Origin.Installation.Implementation {
			return invalid()
		}
		for _, route := range []struct {
			ref  ids.ObjectAddress
			kind PLHandler
		}{{d.Call, PLCallHandler}, {d.Inline, PLInlineHandler}, {d.Validator, PLValidator}} {
			handler, ok := v.Get(route.ref).(*Procedure)
			if !ok || handler.PLHandler != route.kind || ValidatePLHandler(v, handler) != nil {
				return invalid()
			}
		}
	default:
		return invalid()
	}
	return ValidateExtensionMembership(v, d)
}

func ValidatePLHandler(v Read, d *Procedure) error {
	if d == nil || v.State() == nil || v.Owner() == nil || v.Owner().Principals == nil || d.Owner.ID() == 0 || d.Owner != v.Owner().Principals.Principal(d.Owner.ID()) {
		return fmt.Errorf("stuntgres: foreign PL handler authority")
	}
	name, signature, result := d.PLHandler.Declaration()
	if name == "" || v.Get(d.Address()) != d || d.Address().ClassID != ids.ProcedureClassID || d.Address().SubID != 0 || d.Revision() == 0 || v.NameAddress(DescriptorNameKey(d)) != d.Address() || d.Name() != name || d.Signature != signature || d.Result != result || d.Namespace() != (ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PGCatalogNamespaceOID}) || d.RoutineKind != FunctionRoutine || d.Builtin != nil || d.SQL != nil || d.SQLBody != nil || d.SQLDefaults != nil || d.Foreign != nil || d.Implementation != nil || d.Aggregate != nil || d.AccessMethod != NoAccessMethod || d.Conversion.Name() != "" || len(d.Body) != 0 || d.Dictionary != (ids.ObjectAddress{}) {
		return fmt.Errorf("stuntgres: invalid PL handler capability")
	}
	if !d.Origin.Valid(d.Address()) || d.Origin.Installation.Provider != "plpgsql" || d.Origin.Declaration != int(d.PLHandler)-1 || v.Owner().Extensions["plpgsql"] != d.Origin.Installation.Implementation {
		return fmt.Errorf("stuntgres: invalid PL handler origin")
	}
	if len(d.DeclarationDependencies) != 0 {
		return fmt.Errorf("stuntgres: PL handler carries declaration replay metadata")
	}
	released := d.PLHandler.ReleasedProcedure()
	canonical, ok := d.Origin.Installation.Declarations[d.Origin.Declaration].(*Procedure)
	if !ok || canonical.Display != released.Display || canonical.Installed != released.Installed || d.Display != canonical.Display || d.Installed != canonical.Installed {
		return fmt.Errorf("stuntgres: invalid PL handler released metadata")
	}
	if d.Volatility != expr.Volatile || d.Strict != (d.PLHandler != PLCallHandler) || d.Language != (ids.ObjectAddress{}) {
		return fmt.Errorf("stuntgres: invalid PL handler declaration")
	}
	return ValidateExtensionMembership(v, d)
}

func LookupLanguage(v Read, name string) (*Language, error) {
	d, _ := v.Get(v.NameAddress(ids.NameKey{ClassID: ids.LanguageClassID, Name: name})).(*Language)
	if d == nil {
		return nil, fmt.Errorf("stuntgres: missing language descriptor")
	}
	if err := (LanguageProvider{}).Validate(v, d); err != nil {
		return nil, err
	}
	return d, nil
}
