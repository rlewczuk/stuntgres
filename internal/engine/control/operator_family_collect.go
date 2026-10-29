package control

import (
	"context"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
)

func isDumpOperatorFamily(d catalog.Descriptor) bool {
	switch d.(type) {
	case *catalog.OperatorClass, *catalog.OperatorFamily, *catalog.OperatorFamilyOperator, *catalog.OperatorFamilySupport:
		return true
	}
	return false
}
func collectOperatorFamilyDump(_ context.Context, c *dumpCollection, _ ids.ObjectAddress, d catalog.Descriptor) error {
	if catalog.DescriptorOrigin(d) != nil {
		return nil
	}
	var family ids.OID
	switch d := d.(type) {
	case *catalog.OperatorFamilyOperator:
		family = d.Family
	case *catalog.OperatorFamilySupport:
		family = d.Family
	}
	if family != 0 {
		owner, ok := c.CatalogView.OperatorFamily(ids.ObjectAddress{ClassID: ids.OperatorFamilyClassID, ObjectID: family})
		if ok && catalog.DescriptorOrigin(owner) != nil {
			return nil
		}
		if ok {
			if ns, ok := c.CatalogView.Namespace(owner.Namespace()); ok && ns.Temporary != (ids.SessionToken{}) {
				return nil
			}
		}
	}
	if class, ok := d.(*catalog.OperatorClass); ok {
		if err := catalog.Validate(catalog.OperatorClassProvider{}, c.CatalogView, class); err != nil {
			return err
		}
		if !catalog.OperatorClassSeeded(class) || catalog.OperatorClassSeedModified(class) {
			c.OperatorFamilies = append(c.OperatorFamilies, class)
		}
		return nil
	}
	if err := catalog.Validate(catalog.OperatorFamilyProvider{Class: d.Address().ClassID}, c.CatalogView, d); err != nil {
		return err
	}
	include := !catalog.OperatorFamilySeeded(d)
	if family, ok := d.(*catalog.OperatorFamily); ok {
		include = include || catalog.OperatorFamilySeedModified(family)
	}
	if include {
		c.OperatorFamilies = append(c.OperatorFamilies, d)
	}
	return nil
}
