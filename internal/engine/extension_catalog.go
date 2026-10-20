package engine

import (
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
)

// extensionCatalogProvider validates extensions, their text search template
// and dictionary, and installed member procedures. The procedure class is
// registered as procedureProvider, which delegates members here.
type extensionCatalogProvider struct{ class ids.OID }

func (p extensionCatalogProvider) ClassID() ids.OID { return p.class }

func invalidProviderDescriptor() error {
	return fmt.Errorf("stuntgres: invalid extension provider descriptor")
}

// validateProviderHeader checks the identity shared by every descriptor that
// the extension and procedure providers validate.
func validateProviderHeader(v catalog.Read, d catalog.Descriptor, class ids.OID) error {
	a := d.Address()
	if a.ClassID != class || a.ObjectID == 0 || a.SubID != 0 || d.Revision() == 0 || v.NameAddress(catalog.DescriptorNameKey(d)) != a {
		return invalidProviderDescriptor()
	}
	if namespace := d.Namespace(); namespace != (ids.ObjectAddress{}) {
		if _, ok := v.Namespace(namespace); !ok {
			return invalidProviderDescriptor()
		}
	}
	return nil
}

func (p extensionCatalogProvider) Validate(v catalog.Read, d catalog.Descriptor) error {
	invalid := invalidProviderDescriptor
	if err := validateProviderHeader(v, d, p.class); err != nil {
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
		seen := map[ids.ObjectAddress]bool{}
		for _, a := range d.Members {
			if seen[a] {
				return invalid()
			}
			seen[a] = true
			member := v.Get(a)
			if member == nil || member.Namespace() != d.TargetNamespace || extensionMembership(member) != d.Address() {
				return invalid()
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
	case *catalog.TSTemplate:
		if d.Name() != "unaccent" || unaccentTemplateProviderOf(d) != stockUnaccent {
			return invalid()
		}
		ext = d.Extension
		for i, a := range [2]ids.ObjectAddress{d.Init, d.Lexize} {
			proc, ok := v.Procedure(a)
			if !ok || ext != (ids.ObjectAddress{}) && proc.Namespace() != d.Namespace() || proc.Extension != ext || proc.Signature != [2]catalog.ProcedureSignature{catalog.UnaccentInitSignature, catalog.UnaccentLexizeSignature}[i] {
				return invalid()
			}
		}
	case *catalog.TSDictionary:
		if d.Name() != "unaccent" || unaccentDictionaryProviderOf(d) != stockUnaccent {
			return invalid()
		}
		ext = d.Extension
		tmpl, ok := v.TSTemplate(d.Template)
		if !ok || ext != (ids.ObjectAddress{}) && tmpl.Namespace() != d.Namespace() || tmpl.Extension != ext {
			return invalid()
		}
	default:
		return invalid()
	}
	if ext != (ids.ObjectAddress{}) {
		parent, ok := v.Extension(ext)
		if !ok || parent.TargetNamespace != namespace {
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
func extensionMembership(d catalog.Descriptor) ids.ObjectAddress {
	switch d := d.(type) {
	case *catalog.Type:
		return d.Extension
	case *catalog.Procedure:
		return d.Extension
	case *catalog.TSTemplate:
		return d.Extension
	case *catalog.TSDictionary:
		return d.Extension
	}
	return ids.ObjectAddress{}
}

// validateInstalledProcedure checks a procedure installed by an extension and
// returns the extension it claims. Builtin, SQL, foreign and conversion
// routines carry no implementation and are never members.
func validateInstalledProcedure(v catalog.Read, d *catalog.Procedure) (ids.ObjectAddress, error) {
	invalid := func() (ids.ObjectAddress, error) { return ids.ObjectAddress{}, invalidProviderDescriptor() }
	if d.Implementation == nil || d.SQL != nil || d.Aggregate != nil && intaggProviderOf(d) != stockIntagg || d.Foreign != nil || d.Builtin != nil {
		return invalid()
	}
	if intaggProviderOf(d) != nil {
		return d.Extension, intaggProviderOf(d).validateProcedure(v, d)
	}
	if d.RoutineKind != catalog.FunctionRoutine {
		return invalid()
	}
	switch {
	case uuidOsspProviderOf(d) != nil:
		if d.Conversion.Name() != "" || d.Dictionary != (ids.ObjectAddress{}) {
			return invalid()
		}
		return d.Extension, uuidOsspProviderOf(d).validateProcedure(v, d)
	case fuzzystrmatchProviderOf(d) != nil:
		return d.Extension, fuzzystrmatchProviderOf(d).validateProcedure(v, d)
	case pgcryptoProviderOf(d) != nil:
		return d.Extension, pgcryptoProviderOf(d).validateProcedure(v, d)
	case intarrayProviderOf(d) != nil:
		return d.Extension, intarrayProviderOf(d).validateProcedure(v, d)
	case cubeProviderOf(d) != nil:
		return d.Extension, cubeProviderOf(d).validateProcedure(v, d)
	}
	if d.Conversion.Name() != "" || unaccentProviderOf(d) != stockUnaccent {
		return invalid()
	}
	ext := d.Extension
	switch d.Signature {
	case catalog.UnaccentTextSignature, catalog.UnaccentDictionarySignature:
		if d.Name() != "unaccent" || d.Result != 25 || d.Volatility != expr.Stable || !d.Strict {
			return invalid()
		}
		if ext != (ids.ObjectAddress{}) {
			dict, ok := v.TSDictionary(d.Dictionary)
			if !ok || dict.Namespace() != d.Namespace() || dict.Extension != ext {
				return invalid()
			}
		}
	case catalog.UnaccentInitSignature:
		if d.Name() != "unaccent_init" || d.Result != 2281 || d.Volatility != expr.Volatile || d.Strict {
			return invalid()
		}
	case catalog.UnaccentLexizeSignature:
		if d.Name() != "unaccent_lexize" || d.Result != 2281 || d.Volatility != expr.Volatile || d.Strict {
			return invalid()
		}
	default:
		return invalid()
	}
	return ext, nil
}
