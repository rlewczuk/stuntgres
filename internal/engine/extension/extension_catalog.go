package extension

import (
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
)

// CatalogProvider validates extensions, their text search template
// and dictionary, and installed member procedures. The procedure class is
// registered as stored.ProcedureProvider, which delegates members here.
type CatalogProvider struct{ Class ids.OID }

func (p CatalogProvider) ClassID() ids.OID { return p.Class }

func InvalidProviderDescriptor() error {
	return fmt.Errorf("stuntgres: invalid extension provider descriptor")
}

// ValidateProviderHeader checks the identity shared by every descriptor that
// the extension and procedure providers validate.
func ValidateProviderHeader(v catalog.Read, d catalog.Descriptor, class ids.OID) error {
	a := d.Address()
	if a.ClassID != class || a.ObjectID == 0 || a.SubID != 0 || d.Revision() == 0 || v.NameAddress(catalog.DescriptorNameKey(d)) != a {
		return InvalidProviderDescriptor()
	}
	if namespace := d.Namespace(); namespace != (ids.ObjectAddress{}) {
		if _, ok := v.Namespace(namespace); !ok {
			return InvalidProviderDescriptor()
		}
	}
	return nil
}

func (p CatalogProvider) Validate(v catalog.Read, d catalog.Descriptor) error {
	invalid := InvalidProviderDescriptor
	if err := ValidateProviderHeader(v, d, p.Class); err != nil {
		return err
	}
	a, namespace := d.Address(), d.Namespace()
	var ext ids.ObjectAddress
	switch d := d.(type) {
	case *catalog.Extension:
		implementation, registered := v.Owner().Extensions[d.Name()]
		if !registered || implementation.Versions().InstallPath(d.Version) == nil || d.Namespace() != (ids.ObjectAddress{}) {
			return invalid()
		}
		if _, ok := v.Namespace(d.TargetNamespace); !ok {
			return invalid()
		}
		if _, err := catalog.ExtensionPrerequisites(v, d); err != nil {
			return err
		}
		seen := map[ids.ObjectAddress]bool{}
		for _, a := range d.Members {
			if seen[a] {
				return invalid()
			}
			seen[a] = true
			member := v.Get(a)
			if member == nil || catalog.ExtensionMembership(member) != d.Address() {
				return invalid()
			}
			switch member.(type) {
			case *catalog.Type, *catalog.Procedure, *catalog.TSDictionary:
				// Their public namespace may change independently of the
				// installation. Provider manifests retain identity authority.
			default:
				if member.Namespace() != d.TargetNamespace {
					return invalid()
				}
			}
		}
		if err := implementation.ValidateExtension(v, d); err != nil {
			return err
		}
	case *catalog.Procedure:
		var err error
		if ext, err = validateInstalledProcedure(v, d); err != nil {
			return err
		}
	case *catalog.Operator:
		proc, ok := v.Procedure(ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: d.Function})
		if !ok || proc.Extension != d.Extension {
			return invalid()
		}
		ext = d.Extension
		switch {
		case CitextProviderOf(proc) == StockCitext:
			if err := StockCitext.ValidateProcedure(v, proc); err != nil {
				return err
			}
		case CubeProviderOf(proc) == StockCube:
			if err := StockCube.ValidateProcedure(v, proc); err != nil {
				return err
			}
		case EarthdistanceProviderOf(proc) == StockEarthdistance:
			if v.Get(d.Address()) != d {
				return invalid()
			}
			if err := StockEarthdistance.ValidateProcedure(v, proc); err != nil {
				return err
			}
		default:
			return invalid()
		}
	case *catalog.TSTemplate:
		if SimpleTemplate(d) {
			for _, address := range [2]ids.ObjectAddress{d.Init, d.Lexize} {
				procedure, ok := v.Procedure(address)
				if !ok || procedure.Builtin == nil {
					return invalid()
				}
			}
			return nil
		}
		if d.Name() != "unaccent" || UnaccentTemplateProviderOf(d) != StockUnaccent {
			return invalid()
		}
		ext = d.Extension
		for i, a := range [2]ids.ObjectAddress{d.Init, d.Lexize} {
			proc, ok := v.Procedure(a)
			if !ok || proc.Extension != ext || proc.Signature != [2]catalog.ProcedureSignature{catalog.UnaccentInitSignature, catalog.UnaccentLexizeSignature}[i] {
				return invalid()
			}
		}
	case *catalog.TSDictionary:
		if err := validateDictionary(v, d); err != nil {
			return err
		}
		ext = d.Extension
	default:
		return invalid()
	}
	if ext != (ids.ObjectAddress{}) {
		parent, ok := v.Extension(ext)
		if !ok || d.Address().ClassID != ids.ProcedureClassID && d.Address().ClassID != ids.TSDictionaryClassID && parent.TargetNamespace != namespace {
			return invalid()
		}
		found := false
		for _, member := range parent.Members {
			found = found || member == a
		}
		if !found {
			return invalid()
		}
	}
	return nil
}

// validateInstalledProcedure checks a procedure installed by an extension and
// returns the extension it claims. Builtin, SQL, foreign and conversion
// routines carry no implementation and are never members.
func validateInstalledProcedure(v catalog.Read, d *catalog.Procedure) (ids.ObjectAddress, error) {
	invalid := func() (ids.ObjectAddress, error) { return ids.ObjectAddress{}, InvalidProviderDescriptor() }
	if d.Implementation == nil || d.SQL != nil || d.Aggregate != nil && IntaggProviderOf(d) != StockIntagg || d.Foreign != nil || d.Builtin != nil {
		return invalid()
	}
	if IntaggProviderOf(d) != nil {
		return d.Extension, IntaggProviderOf(d).ValidateProcedure(v, d)
	}
	if d.RoutineKind != catalog.FunctionRoutine {
		return invalid()
	}
	switch {
	case AmcheckProviderOf(d) != nil:
		return d.Extension, AmcheckProviderOf(d).ValidateProcedure(v, d)
	case PgrowlocksProviderOf(d) != nil:
		return d.Extension, PgrowlocksProviderOf(d).ValidateProcedure(v, d)
	case UUIDOsspProviderOf(d) != nil:
		if d.Conversion.Name() != "" || d.Dictionary != (ids.ObjectAddress{}) {
			return invalid()
		}
		return d.Extension, UUIDOsspProviderOf(d).ValidateProcedure(v, d)
	case FuzzystrmatchProviderOf(d) != nil:
		return d.Extension, FuzzystrmatchProviderOf(d).ValidateProcedure(v, d)
	case PgcryptoProviderOf(d) != nil:
		return d.Extension, PgcryptoProviderOf(d).ValidateProcedure(v, d)
	case HstoreProviderOf(d) != nil:
		return d.Extension, HstoreProviderOf(d).ValidateProcedure(v, d)
	case IntarrayProviderOf(d) != nil:
		return d.Extension, IntarrayProviderOf(d).ValidateProcedure(v, d)
	case LtreeProviderOf(d) != nil:
		return d.Extension, LtreeProviderOf(d).ValidateProcedure(v, d)
	case EarthdistanceProviderOf(d) != nil:
		return d.Extension, EarthdistanceProviderOf(d).ValidateProcedure(v, d)
	case CubeProviderOf(d) != nil:
		return d.Extension, CubeProviderOf(d).ValidateProcedure(v, d)
	case CitextProviderOf(d) != nil:
		return d.Extension, CitextProviderOf(d).ValidateProcedure(v, d)
	case IsnProviderOf(d) != nil:
		return d.Extension, IsnProviderOf(d).ValidateProcedure(v, d)
	}
	if d.Conversion.Name() != "" || UnaccentProviderOf(d) != StockUnaccent {
		return invalid()
	}
	ext := d.Extension
	symbol, err := procedureSymbol(v, d, true)
	if err != nil {
		return invalid()
	}
	switch d.Signature {
	case catalog.UnaccentTextSignature, catalog.UnaccentDictionarySignature:
		if symbol != "unaccent" || d.Result != 25 || d.Volatility != expr.Stable || !d.Strict {
			return invalid()
		}
		if ext != (ids.ObjectAddress{}) {
			dict, ok := v.TSDictionary(d.Dictionary)
			if !ok || dict.Extension != ext {
				return invalid()
			}
		}
	case catalog.UnaccentInitSignature:
		if symbol != "unaccent_init" || d.Result != 2281 || d.Volatility != expr.Volatile || d.Strict {
			return invalid()
		}
	case catalog.UnaccentLexizeSignature:
		if symbol != "unaccent_lexize" || d.Result != 2281 || d.Volatility != expr.Volatile || d.Strict {
			return invalid()
		}
	default:
		return invalid()
	}
	return ext, nil
}
