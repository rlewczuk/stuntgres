package engine

import (
	"fmt"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

type collationDescriptor struct {
	oid                                    ids.OID
	name                                   string
	encoding                               int32
	namespace                              ids.ObjectAddress
	revision                               ids.DefinitionRevision
	owner                                  authorization.PrincipalRef
	provider                               string
	deterministic                          bool
	collate, ctype, locale, rules, version *string
	semantics                              uint32
}
type conversionDescriptor struct {
	oid                 ids.OID
	name                string
	namespace           ids.ObjectAddress
	revision            ids.DefinitionRevision
	owner               authorization.PrincipalRef
	source, destination int
	handler             ids.ObjectAddress
	defaultConversion   bool
}

func (d *collationDescriptor) Address() ids.ObjectAddress {
	return ids.ObjectAddress{ClassID: ids.CollationClassID, ObjectID: d.oid}
}
func (d *collationDescriptor) Revision() ids.DefinitionRevision {
	if d.revision == 0 {
		return 1
	}
	return d.revision
}
func (d *collationDescriptor) Namespace() ids.ObjectAddress {
	if d.namespace != (ids.ObjectAddress{}) {
		return d.namespace
	}
	return ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PGCatalogNamespaceOID}
}
func (d *collationDescriptor) Name() string       { return d.name }
func (*collationDescriptor) Kind() ids.ObjectKind { return ids.CollationObject }
func (d *conversionDescriptor) Address() ids.ObjectAddress {
	return ids.ObjectAddress{ClassID: ids.ConversionClassID, ObjectID: d.oid}
}
func (d *conversionDescriptor) Revision() ids.DefinitionRevision { return d.revision }
func (d *conversionDescriptor) Namespace() ids.ObjectAddress     { return d.namespace }
func (d *conversionDescriptor) Name() string                     { return d.name }
func (*conversionDescriptor) Kind() ids.ObjectKind               { return ids.ConversionObject }

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

func (collationProvider) ClassID() ids.OID  { return ids.CollationClassID }
func (conversionProvider) ClassID() ids.OID { return ids.ConversionClassID }
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
	if d.oid == 0 || d.name == "" || d.revision == 0 || d.source <= 0 || d.destination <= 0 || sqltype.EncodingName(d.source) == "" || sqltype.EncodingName(d.destination) == "" {
		return fmt.Errorf("stuntgres: invalid conversion properties")
	}
	if fixed := conversionSeed(d.oid); fixed != nil {
		if d.source != fixed.source || d.destination != fixed.destination || d.handler != fixed.handler || d.defaultConversion != fixed.defaultConversion {
			return fmt.Errorf("stuntgres: unsupported seeded conversion capability")
		}
	}
	procedure, ok := view.state.descriptors[d.handler].(*procedureDescriptor)
	if !ok || procedure.signature != conversionSignature || procedure.result != 23 || procedure.conversion.Name() == "" {
		return fmt.Errorf("stuntgres: unavailable conversion routine")
	}
	registered, ok := sqltype.RegisteredEncodingConversion(procedure.name)
	if !ok || registered != procedure.conversion {
		return fmt.Errorf("stuntgres: invalid conversion routine capability")
	}
	if !registered.Supports(d.source, d.destination) {
		return fmt.Errorf("stuntgres: conversion pair does not match real routine")
	}
	if err := validateInquiryIdentity(view, d); err != nil {
		return err
	}
	namespace := view.state.descriptors[d.Namespace()].(*namespaceDescriptor)
	if namespace.temporary == (ids.SessionToken{}) {
		handlerNamespace := view.state.descriptors[procedure.Namespace()].(*namespaceDescriptor)
		if handlerNamespace.temporary != (ids.SessionToken{}) {
			return fmt.Errorf("stuntgres: durable conversion has temporary routine")
		}
	}
	return nil
}
func validateInquiryIdentity(view CatalogRead, d Descriptor) error {
	namespace, ok := view.Descriptor(d.Namespace())
	if !ok || namespace.Kind() != ids.NamespaceObject {
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
func selectCollation(candidates []collationCandidate, encoding int32) ids.ObjectAddress {
	var fallback ids.ObjectAddress
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
	address  ids.ObjectAddress
	encoding int32
}

// Native fixed names are unique. Keep the encoding-bearing lookup inline,
// avoiding an index copy, catalog scan or allocation during row evaluation.
func visibleCollation(view CatalogRead, path []ids.ObjectAddress, d Descriptor) bool {
	for _, namespace := range path {
		a := view.state.names[ids.NameKey{ClassID: ids.CollationClassID, Namespace: namespace, Name: d.Name()}]
		candidate, ok := view.state.descriptors[a].(*collationDescriptor)
		if !ok {
			continue
		}
		one := [1]collationCandidate{{a, candidate.encoding}}
		if selected := selectCollation(one[:], 6); selected != (ids.ObjectAddress{}) {
			return selected == d.Address()
		}
	}
	return false
}
