package engine

import "fmt"

// Fixed PostgreSQL 18 identities are inquiry-only. Comparison and conversion
// execution retain their existing, narrower contracts.
type collationDescriptor struct {
	oid      OID
	name     string
	encoding int32
}
type conversionDescriptor struct {
	oid  OID
	name string
}

func (d *collationDescriptor) Address() ObjectAddress {
	return ObjectAddress{ClassID: CollationClassID, ObjectID: d.oid}
}
func (*collationDescriptor) Revision() DefinitionRevision { return 1 }
func (*collationDescriptor) Namespace() ObjectAddress {
	return ObjectAddress{ClassID: NamespaceClassID, ObjectID: PGCatalogNamespaceOID}
}
func (d *collationDescriptor) Name() string   { return d.name }
func (*collationDescriptor) Kind() ObjectKind { return CollationObject }
func (d *conversionDescriptor) Address() ObjectAddress {
	return ObjectAddress{ClassID: ConversionClassID, ObjectID: d.oid}
}
func (*conversionDescriptor) Revision() DefinitionRevision { return 1 }
func (*conversionDescriptor) Namespace() ObjectAddress {
	return ObjectAddress{ClassID: NamespaceClassID, ObjectID: PGCatalogNamespaceOID}
}
func (d *conversionDescriptor) Name() string   { return d.name }
func (*conversionDescriptor) Kind() ObjectKind { return ConversionObject }

var builtinCollations = [...]collationDescriptor{
	{100, "default", -1}, {950, "C", -1}, {951, "POSIX", -1},
	{962, "ucs_basic", 6}, {963, "unicode", -1}, {811, "pg_c_utf8", 6}, {6411, "pg_unicode_fast", 6},
}

type collationProvider struct{}
type conversionProvider struct{}

func (collationProvider) ClassID() OID  { return CollationClassID }
func (conversionProvider) ClassID() OID { return ConversionClassID }
func (collationProvider) Validate(view CatalogRead, descriptor Descriptor) error {
	d, ok := descriptor.(*collationDescriptor)
	if !ok || d == nil {
		return fmt.Errorf("stuntgres: invalid collation descriptor")
	}
	found := false
	for _, fixed := range builtinCollations {
		if *d == fixed {
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("stuntgres: unsupported collation identity")
	}
	return validateInquiryIdentity(view, d)
}
func (conversionProvider) Validate(view CatalogRead, descriptor Descriptor) error {
	d, ok := descriptor.(*conversionDescriptor)
	if !ok || d == nil {
		return fmt.Errorf("stuntgres: invalid conversion descriptor")
	}
	if d.oid < 4402 || d.oid > 4531 || d.oid == 4450 || d.oid == 4451 {
		return fmt.Errorf("stuntgres: unsupported conversion identity")
	}
	// The release inventory is in fixed OID order, with two documented holes.
	// Validate in constant time without a secondary registry allocation.
	index := int(d.oid - 4402)
	if d.oid >= 4452 {
		index -= 2
	}
	if *d != builtinConversions[index] {
		return fmt.Errorf("stuntgres: unsupported conversion identity")
	}
	return validateInquiryIdentity(view, d)
}
func validateInquiryIdentity(view CatalogRead, d Descriptor) error {
	namespace, ok := view.Descriptor(d.Namespace())
	if !ok || namespace.Kind() != NamespaceObject {
		return fmt.Errorf("stuntgres: inquiry namespace is missing")
	}
	if view.state.names[NameKey{ClassID: d.Address().ClassID, Namespace: d.Namespace(), Name: d.Name()}] != d.Address() {
		return fmt.Errorf("stuntgres: invalid inquiry identity")
	}
	return nil
}

// A namespace's usable exact encoding beats its fallback. Callers visit
// namespaces in path order, so an earlier fallback beats a later exact entry.
// This helper also covers synthetic multi-encoding candidates; SQL collation
// DDL and multi-encoding native catalogs remain unsupported.
func selectCollation(candidates []collationCandidate, encoding int32) ObjectAddress {
	var fallback ObjectAddress
	for _, candidate := range candidates {
		if candidate.encoding == encoding {
			return candidate.address
		}
		if candidate.encoding == -1 {
			fallback = candidate.address
		}
	}
	return fallback
}

type collationCandidate struct {
	address  ObjectAddress
	encoding int32
}

// Native fixed names are unique. Keep the encoding-bearing lookup inline,
// avoiding an index copy, catalog scan or allocation during row evaluation.
func visibleCollation(view CatalogRead, path []ObjectAddress, d Descriptor) bool {
	for _, namespace := range path {
		a := view.state.names[NameKey{ClassID: CollationClassID, Namespace: namespace, Name: d.Name()}]
		candidate, ok := view.state.descriptors[a].(*collationDescriptor)
		if !ok {
			continue
		}
		one := [1]collationCandidate{{a, candidate.encoding}}
		if selected := selectCollation(one[:], 6); selected != (ObjectAddress{}) {
			return selected == d.Address()
		}
	}
	return false
}
