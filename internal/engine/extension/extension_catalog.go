package extension

import (
	"fmt"

	"stuntgres/internal/engine/catalog"
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
	a := d.Address()
	var ext ids.ObjectAddress
	switch d := d.(type) {
	case *catalog.Extension:
		if v.Owner() == nil || v.Owner().Principals == nil || v.Get(d.Address()) != d || d.Owner.ID() == 0 || d.Owner != v.Owner().Principals.Principal(d.Owner.ID()) {
			return invalid()
		}
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
		if err := implementation.ValidateExtension(v, d); err != nil {
			return err
		}
	case *catalog.Procedure:
		var err error
		if ext, err = validateInstalledProcedure(v, d); err != nil {
			return err
		}
	case *catalog.Operator:
		if d.Owner.ID() == 0 || d.Owner != v.Owner().Principals.Principal(d.Owner.ID()) {
			return invalid()
		}
		if err := catalog.ValidateInstalledClassOrigin(v, d); err != nil {
			return err
		}
		proc, ok := v.Procedure(ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: d.Function})
		if !ok || proc.Origin == nil || !catalog.SameInstallation(d, proc.Origin.Installation) {
			return invalid()
		}
		ext = d.Extension
		if _, err := ProcedureSymbol(v, proc); err != nil {
			return err
		}
	case *catalog.TSTemplate:
		if err := validateTemplate(v, d); err != nil {
			return err
		}
		ext = d.Extension
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
		if !ok {
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

// validateInstalledProcedure validates the immutable declaration independently
// of current SQL membership. Unaccent's dictionary is resolved at execution,
// not a dependency on the original installation's incidental default dictionary.
func validateInstalledProcedure(v catalog.Read, d *catalog.Procedure) (ids.ObjectAddress, error) {
	if _, err := ProcedureSymbol(v, d); err != nil {
		return ids.ObjectAddress{}, err
	}
	if provider := PgstattupleProviderOf(d); provider != nil {
		return d.Extension, provider.ValidateProcedure(v, d)
	}
	if provider := PageinspectProviderOf(d); provider != nil {
		return d.Extension, provider.ValidateProcedure(v, d)
	}
	return d.Extension, nil
}
