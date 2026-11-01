package extension

import (
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
)

// BloomProvider installs released logical declarations, not a C ABI loader.
// Its nonzero size keeps independently constructed providers distinct.
type BloomProvider struct{ identity byte }

var StockBloom = &BloomProvider{}

func (*BloomProvider) Control() catalog.ExtensionControl {
	return catalog.ExtensionControl{
		Version: "1.0", Superuser: true, Relocatable: true,
		Comment: new("bloom access method - signature file based index"),
	}
}

func (*BloomProvider) Versions() catalog.ExtensionVersions {
	return catalog.ExtensionVersions{Bases: []string{"1.0"}}
}

func (p *BloomProvider) ValidateExtension(v catalog.Read, e *catalog.Extension) error {
	if p != StockBloom {
		return InvalidProviderDescriptor()
	}
	return ValidateCurrentInstallation(v, e, p)
}

func (p *BloomProvider) validateRecipe(v catalog.Read, e *catalog.Extension) error {
	if p != StockBloom || !currentManifest(v, e) || e.Version != "1.0" || len(e.Members) != 6 {
		return InvalidProviderDescriptor()
	}
	return p.ValidateExtension(v, e)
}

// ValidateBloomClassBindings checks genuine handler authority and real typed
// support even on empty/NULL tables. Stock declarations retain their exact seal;
// ordinary alias classes acquire no installed origin or name-based capability.
func ValidateBloomClassBindings(v catalog.Read, c *catalog.OperatorClass) error {
	invalid := func() error { return fmt.Errorf("stuntgres: invalid bloom class binding") }
	if c == nil || catalog.ValidateInstalledClassOrigin(v, c) != nil || c.InputType != 23 && c.InputType != 25 || c.StorageType != 0 && c.StorageType != c.InputType {
		return invalid()
	}
	method, ok := v.AccessMethod(c.Method)
	if !ok || method.Native != catalog.BloomAccessMethod {
		return invalid()
	}
	family, ok := v.OperatorFamily(ids.ObjectAddress{ClassID: ids.OperatorFamilyClassID, ObjectID: c.Family})
	if !ok || family.Method != c.Method {
		return invalid()
	}
	equality, hash := ids.OID(96), ids.OID(450)
	if c.InputType == 25 {
		equality, hash = 98, 400
	}
	op, ok := v.FamilyOperator(c.Family, c.InputType, c.InputType, 1, 's')
	if !ok || op.Operator != equality || op.Method != c.Method {
		return invalid()
	}
	support, ok := v.FamilySupport(c.Family, c.InputType, c.InputType, 1)
	if !ok || support.Function != hash {
		return invalid()
	}
	if c.Origin != nil {
		if !c.Origin.Valid(c.Address()) || c.Origin.Installation.Implementation != StockBloom {
			return invalid()
		}
		installation := c.Origin.Installation
		slot := c.Origin.Declaration
		if len(installation.Members) != 6 || slot != 3 && slot != 5 || c.Method != installation.Members[1].ObjectID || c.Family != installation.Members[slot-1].ObjectID || !catalog.SameInstallation(method, installation) || !catalog.SameInstallation(family, installation) || op.InternalOwner != c.Address() || support.InternalOwner != c.Address() {
			return invalid()
		}
	} else if method.Origin != nil || family.Origin != nil {
		// Ordinary classes are supported through genuine handler aliases, not
		// as substituted declarations in the stock installed method's recipe.
		return invalid()
	}
	for _, d := range [...]catalog.Descriptor{
		method, family, c, op, support,
		v.Get(ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: hash}),
		v.Get(ids.ObjectAddress{ClassID: ids.OperatorClassID, ObjectID: equality}),
	} {
		if d == nil || v.Owner() == nil {
			return invalid()
		}
		provider := v.Owner().Provider(d.Address().ClassID)
		if provider == nil {
			return invalid()
		}
		if err := provider.Validate(v, d); err != nil {
			return err
		}
	}
	return nil
}
