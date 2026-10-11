package engine

import (
	"fmt"
	"stuntgres/internal/sqltype"
)

type collationDescriptor struct {
	oid                                    OID
	name                                   string
	encoding                               int32
	namespace                              ObjectAddress
	revision                               DefinitionRevision
	owner                                  PrincipalRef
	provider                               string
	deterministic                          bool
	collate, ctype, locale, rules, version *string
	semantics                              uint32
}
type conversionDescriptor struct {
	oid  OID
	name string
}

func (d *collationDescriptor) Address() ObjectAddress {
	return ObjectAddress{ClassID: CollationClassID, ObjectID: d.oid}
}
func (d *collationDescriptor) Revision() DefinitionRevision {
	if d.revision == 0 {
		return 1
	}
	return d.revision
}
func (d *collationDescriptor) Namespace() ObjectAddress {
	if d.namespace != (ObjectAddress{}) {
		return d.namespace
	}
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

func collationText(s string) *string { return &s }

var builtinCollations = [...]collationDescriptor{
	{oid: 100, name: "default", encoding: -1, provider: "d", deterministic: true, semantics: 950},
	{oid: 950, name: "C", encoding: -1, provider: "c", deterministic: true, collate: collationText("C"), ctype: collationText("C"), semantics: 950},
	{oid: 951, name: "POSIX", encoding: -1, provider: "c", deterministic: true, collate: collationText("POSIX"), ctype: collationText("POSIX"), semantics: 950},
	{oid: 962, name: "ucs_basic", encoding: 6, provider: "b", deterministic: true, locale: collationText("C"), version: collationText("1")},
	{oid: 963, name: "unicode", encoding: -1, provider: "i", deterministic: true, locale: collationText("und")},
	{oid: 811, name: "pg_c_utf8", encoding: 6, provider: "b", deterministic: true, locale: collationText("C.UTF-8"), version: collationText("1"), semantics: 811},
	{oid: 6411, name: "pg_unicode_fast", encoding: 6, provider: "b", deterministic: true, locale: collationText("PG_UNICODE_FAST"), version: collationText("1"), semantics: 6411},
}

func (d *collationDescriptor) seeded() bool {
	for i := range builtinCollations {
		if d.oid == builtinCollations[i].oid {
			return true
		}
	}
	return false
}
func (d *collationDescriptor) usage() (sqltype.CollationRef, error) {
	return sqltype.RegisteredCollation(uint32(d.oid), d.semantics)
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
	if d.seeded() {
		found := false
		for _, fixed := range builtinCollations {
			if d.oid != fixed.oid {
				continue
			}
			invariant := *d
			invariant.name, invariant.namespace, invariant.revision, invariant.owner = fixed.name, fixed.namespace, fixed.revision, fixed.owner
			found = invariant == fixed && d.name != ""
			break
		}
		if !found {
			return fmt.Errorf("stuntgres: unsupported collation identity")
		}
	} else {
		if d.oid == 0 || d.name == "" || d.revision == 0 || d.encoding != 6 && d.encoding != -1 || !d.deterministic || d.rules != nil {
			return fmt.Errorf("stuntgres: invalid collation properties")
		}
		if d.provider == "c" && d.version != nil || d.provider == "b" && d.version == nil {
			return fmt.Errorf("stuntgres: invalid recorded collation version")
		}
		mode, err := collationSemantics(d)
		if err != nil || mode != d.semantics {
			return fmt.Errorf("stuntgres: invalid collation provider")
		}
		if _, err = d.usage(); err != nil {
			return err
		}
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
	if view.state.names[descriptorNameKey(d)] != d.Address() {
		return fmt.Errorf("stuntgres: invalid inquiry identity")
	}
	return nil
}

// A namespace's usable exact encoding beats its fallback. Callers visit
// namespaces in path order, so an earlier fallback beats a later exact entry.
// This helper also covers synthetic multi-encoding candidates.
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
